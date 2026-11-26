// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"cmp"
	"context"
	"reflect"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
)

type SkillSummary struct {
	Name        string
	Description string
}

type Manager struct {
	mu              sync.RWMutex
	available       map[string]*SkillInfo
	loaded          map[string]*LoadedSkill
	skillToolNames  map[string][]string
	toolOwners      map[string][]string
	registering     map[string]chan struct{}
	retries         map[string]*toolRetry
	hasAnyResources bool
	hasAnyAssets    bool

	now        func() time.Time
	retryAfter time.Duration
	retrying   sync.WaitGroup
}

type toolRetry struct {
	entries []map[string]any
	lastTry time.Time
	running bool
}

const toolRetryInterval = time.Minute

func NewManager() *Manager {
	return &Manager{
		available:      make(map[string]*SkillInfo),
		loaded:         make(map[string]*LoadedSkill),
		skillToolNames: make(map[string][]string),
		toolOwners:     make(map[string][]string),
		registering:    make(map[string]chan struct{}),
		retries:        make(map[string]*toolRetry),
		now:            time.Now,
		retryAfter:     toolRetryInterval,
	}
}

func (m *Manager) AddAvailable(info *SkillInfo) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.available[info.Name]
	changed := !ok || !reflect.DeepEqual(existing, info)
	m.available[info.Name] = info
	m.noteResourcesLocked(info.HasReferences, info.HasAssets)
	return changed
}

func (m *Manager) noteResourcesLocked(hasReferences, hasAssets bool) {
	if hasReferences || hasAssets {
		m.hasAnyResources = true
	}
	if hasAssets {
		m.hasAnyAssets = true
	}
}

func (m *Manager) noteLoadedResourcesLocked(ls *LoadedSkill) {
	if ls == nil {
		return
	}
	m.noteResourcesLocked(ls.HasReferences || ls.ResourceCount > 0, ls.HasAssets)
}

func (m *Manager) GetAvailable(name string) *SkillInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.available[name]
}

func (m *Manager) ListAvailable() []SkillSummary {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]SkillSummary, 0, len(m.available))
	for _, info := range m.available {
		out = append(out, SkillSummary{
			Name:        info.Name,
			Description: info.Description,
		})
	}
	slices.SortFunc(out, func(a, b SkillSummary) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

func (m *Manager) AvailableSkillsForCard() []agent.AvailableSkill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]agent.AvailableSkill, 0, len(m.available))
	for _, info := range m.available {
		out = append(out, agent.AvailableSkill{
			Name:        info.Name,
			Description: info.Description,
		})
	}
	slices.SortFunc(out, func(a, b agent.AvailableSkill) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

func (m *Manager) HasAnyResources() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hasAnyResources
}

func (m *Manager) HasResources(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	info := m.available[name]
	if info == nil {
		return false
	}
	return info.HasReferences || info.HasAssets
}

func (m *Manager) HasAnyAssets() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.hasAnyAssets
}

func (m *Manager) SetLoaded(name string, ls *LoadedSkill) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loaded[name] = ls
	m.noteLoadedResourcesLocked(ls)
}

func (m *Manager) Hold(ctx context.Context, name string, ls *LoadedSkill, registrar ToolRegistrar) (*LoadedSkill, error) {
	for {
		m.mu.Lock()
		if held := m.loaded[name]; held != nil {
			m.mu.Unlock()
			m.RetryTools(name, registrar)
			return held, nil
		}
		if done, busy := m.registering[name]; busy {
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		m.registering[name] = done
		m.mu.Unlock()
		m.registerHeld(name, ls, registrar, done)
		return ls, nil
	}
}

func (m *Manager) registerHeld(name string, ls *LoadedSkill, registrar ToolRegistrar, done chan struct{}) {
	registered := false
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if registered {
			m.loaded[name] = ls
			m.noteLoadedResourcesLocked(ls)
		}
		delete(m.registering, name)
		close(done)
	}()
	if retry := m.registerTools(name, ls, registrar); len(retry) > 0 {
		m.mu.Lock()
		m.retries[name] = &toolRetry{entries: retry, lastTry: m.now()}
		m.mu.Unlock()
	}
	registered = true
}

func (m *Manager) registerTools(name string, ls *LoadedSkill, registrar ToolRegistrar) []map[string]any {
	if registrar == nil {
		return nil
	}
	for _, bt := range ls.BundledTools {
		m.RegisterSkillTool(name, bt.ToolName)
		registrar.RegisterSkillBundledTool(bt)
	}
	names, retry := registrar.RegisterSkillTools(name, ls.Tools)
	for _, toolName := range names {
		m.RegisterSkillTool(name, toolName)
	}
	return retry
}

func (m *Manager) RetryTools(name string, registrar ToolRegistrar) {
	if registrar == nil {
		return
	}
	m.mu.Lock()
	r := m.retries[name]
	if r == nil || r.running || m.now().Sub(r.lastTry) < m.retryAfter {
		m.mu.Unlock()
		return
	}
	r.running = true
	entries := r.entries
	m.mu.Unlock()

	m.retrying.Go(func() {
		retry := entries
		defer func() {
			if p := recover(); p != nil {
				skillLoaderLogger().Error("panic recovered in skill tool retry",
					"skill", name, "panic", p, "stack", string(debug.Stack()))
			}
			m.mu.Lock()
			r.running = false
			r.lastTry = m.now()
			r.entries = retry
			if len(retry) == 0 {
				delete(m.retries, name)
			}
			m.mu.Unlock()
		}()
		var names []string
		names, retry = registrar.RegisterSkillTools(name, entries)
		for _, toolName := range names {
			m.RegisterSkillTool(name, toolName)
		}
	})
}

func (m *Manager) IsHeld(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loaded[name] != nil
}

func (m *Manager) GetLoaded(name string) *LoadedSkill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.loaded[name]
}

func (m *Manager) RegisterSkillTool(skillName, toolName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.Contains(m.skillToolNames[skillName], toolName) {
		m.skillToolNames[skillName] = append(m.skillToolNames[skillName], toolName)
	}
	if !slices.Contains(m.toolOwners[toolName], skillName) {
		m.toolOwners[toolName] = append(m.toolOwners[toolName], skillName)
	}
}

func (m *Manager) SkillToolNames(skillName string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.skillToolNames[skillName])
}

func (m *Manager) SkillToolOwners(toolName string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Clone(m.toolOwners[toolName])
}

func (m *Manager) GetInstructions(loadedNames []string) []agent.LoadedSkillInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []agent.LoadedSkillInfo
	for _, name := range loadedNames {
		ls := m.loaded[name]
		if ls != nil && ls.InstructionContent != "" {
			out = append(out, ls.ToLoadedSkillInfo())
		}
	}
	return out
}
