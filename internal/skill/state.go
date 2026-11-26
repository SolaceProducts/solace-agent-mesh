// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skill

import (
	"slices"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func init() {
	agent.SkillStateFactory = func(sess *session.Session) tool.SkillStateProvider {
		return NewSkillState(sess)
	}
}

const sessionMetadataKey = "loaded_skills"

type SkillState struct {
	mu      sync.Mutex
	loaded  map[string]bool
	version int64
	sess    *session.Session
}

func NewSkillState(sess *session.Session) *SkillState {
	s := &SkillState{
		loaded: make(map[string]bool),
		sess:   sess,
	}
	if sess.Metadata != nil {
		if raw, ok := sess.Metadata[sessionMetadataKey]; ok {
			if names, ok := raw.([]any); ok {
				for _, n := range names {
					if name, ok := n.(string); ok && name != "" {
						s.loaded[name] = true
					}
				}
			}
			if names, ok := raw.([]string); ok {
				for _, name := range names {
					if name != "" {
						s.loaded[name] = true
					}
				}
			}
		}
	}
	if len(s.loaded) > 0 {
		s.version = 1
	}
	return s
}

func (s *SkillState) Load(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded[name] {
		return
	}
	s.loaded[name] = true
	s.version++
	s.syncToSession()
}

func (s *SkillState) Unload(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded[name] {
		return
	}
	delete(s.loaded, name)
	s.version++
	s.syncToSession()
}

func (s *SkillState) IsLoaded(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loaded[name]
}

func (s *SkillState) LoadedNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.loaded))
	for name := range s.loaded {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (s *SkillState) Version() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

func (s *SkillState) syncToSession() {
	if s.sess.Metadata == nil {
		s.sess.Metadata = make(map[string]any)
	}
	names := make([]string, 0, len(s.loaded))
	for name := range s.loaded {
		names = append(names, name)
	}
	slices.Sort(names)
	s.sess.Metadata[sessionMetadataKey] = names
}
