// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

type timekitLoader struct{}

func (timekitLoader) LoadSkill(context.Context, string, *tool.Context) (map[string]any, error) {
	return map[string]any{
		"instructions": "Tell the time.",
		"tools":        []any{map[string]any{"tool_type": "builtin", "tool_name": "get_current_time"}},
	}, nil
}

type sessionSkills struct{ loaded map[string]bool }

func (s *sessionSkills) IsLoaded(name string) bool { return s.loaded[name] }
func (s *sessionSkills) Load(name string)          { s.loaded[name] = true }
func (s *sessionSkills) Unload(name string)        { delete(s.loaded, name) }
func (s *sessionSkills) Version() int64            { return 0 }
func (s *sessionSkills) LoadedNames() []string {
	var names []string
	for name := range s.loaded {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func newTimekitManager() *skill.Manager {
	mgr := skill.NewManager()
	mgr.AddAvailable(&skill.SkillInfo{Name: "timekit"})
	return mgr
}

func TestARestartedAgentRehydratesASkillWithItsTools(t *testing.T) {
	ag, registrar := newSkillTestAgent(t)
	mgr := newTimekitManager()
	rehydrator := &skillRehydratorImpl{manager: mgr, loader: timekitLoader{}, registrar: registrar, logger: slog.Default()}

	failed := rehydrator.RehydrateSkills(t.Context(), &tool.Context{}, []string{"timekit"})

	if len(failed) != 0 || !mgr.IsHeld("timekit") {
		t.Fatalf("failed = %v, held = %v; want timekit restored", failed, mgr.IsHeld("timekit"))
	}
	if ag.Tools().GetTool("get_current_time") == nil {
		t.Error("the rehydrated skill's tool is not registered on the agent")
	}
	if owners := mgr.SkillToolOwners("get_current_time"); !slices.Equal(owners, []string{"timekit"}) {
		t.Errorf("owners = %v, want [timekit]", owners)
	}
}

func TestOneSessionsUnloadLeavesTheSkillsToolsForAnother(t *testing.T) {
	ag, registrar := newSkillTestAgent(t)
	mgr := newTimekitManager()
	sessionB := &sessionSkills{loaded: map[string]bool{"timekit": true}}
	rehydrator := &skillRehydratorImpl{manager: mgr, loader: timekitLoader{}, registrar: registrar, logger: slog.Default()}
	if failed := rehydrator.RehydrateSkills(t.Context(), &tool.Context{SkillState: sessionB}, sessionB.LoadedNames()); len(failed) != 0 {
		t.Fatalf("session B rehydration failed for %v", failed)
	}

	sessionA := &tool.Context{SkillState: &sessionSkills{loaded: map[string]bool{}}}
	if res, err := skill.NewLoadSkillTool(mgr, timekitLoader{}, registrar).Execute(t.Context(), map[string]any{"name": "timekit"}, sessionA); err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("session A load: %+v, %v", res, err)
	}
	if res, err := skill.NewUnloadSkillTool().Execute(t.Context(), map[string]any{"name": "timekit"}, sessionA); err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("session A unload: %+v, %v", res, err)
	}

	if ag.Tools().GetTool("get_current_time") == nil {
		t.Error("session A's unload removed the tool session B still uses")
	}
	if owners := mgr.SkillToolOwners("get_current_time"); !slices.Equal(owners, []string{"timekit"}) {
		t.Errorf("owners = %v, want [timekit]", owners)
	}
	if !sessionB.IsLoaded("timekit") || sessionA.SkillState.IsLoaded("timekit") {
		t.Errorf("B loaded = %v, A loaded = %v; want only B", sessionB.IsLoaded("timekit"), sessionA.SkillState.IsLoaded("timekit"))
	}
}

type recordingInvoker struct {
	mu      sync.Mutex
	corrIDs []string
}

func (r *recordingInvoker) Invoke(_ context.Context, corrID string, _ *remote.InvokeRequest, _ remote.StatusCallback) (*remote.InvokeResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.corrIDs = append(r.corrIDs, corrID)
	return &remote.InvokeResponse{Result: remotetool.InvokeResult{ToolResult: map[string]any{"instructions": "x"}}}, nil
}

func TestConcurrentLoadsOfOneSkillUseDistinctCorrelationIDs(t *testing.T) {
	invoker := &recordingInvoker{}
	loader := &strSkillLoader{executor: invoker}
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := loader.LoadSkill(t.Context(), "timekit", &tool.Context{}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	if distinct := len(slices.Compact(slices.Sorted(slices.Values(invoker.corrIDs)))); distinct != 16 {
		t.Errorf("16 loads used %d distinct correlation ids, want 16: a reply could reach the wrong load", distinct)
	}
}
