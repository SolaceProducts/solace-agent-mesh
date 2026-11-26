// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type stubSkillLoader struct {
	loaded   []string
	callers  []*tool.Context
	failWith error
}

func (l *stubSkillLoader) LoadSkill(_ context.Context, name string, toolCtx *tool.Context) (map[string]any, error) {
	l.loaded = append(l.loaded, name)
	l.callers = append(l.callers, toolCtx)
	if l.failWith != nil {
		return nil, l.failWith
	}
	return map[string]any{"instructions": name + " instructions"}, nil
}

func TestRehydrationRefusesASkillTheAgentIsNoLongerConfiguredWith(t *testing.T) {
	mgr := skill.NewManager()
	mgr.AddAvailable(&skill.SkillInfo{Name: "docs"})
	loader := &stubSkillLoader{}
	r := &skillRehydratorImpl{manager: mgr, loader: loader, logger: slog.Default()}

	failed := r.RehydrateSkills(t.Context(), &tool.Context{}, []string{"docs", "removed"})

	if !slices.Equal(failed, []string{"removed"}) {
		t.Errorf("failed = %v, want [removed]", failed)
	}
	if !slices.Equal(loader.loaded, []string{"docs"}) {
		t.Errorf("fetched %v from the STR, want only the configured skill", loader.loaded)
	}
	if mgr.GetLoaded("docs") == nil {
		t.Error("the configured skill was not restored")
	}
}

func TestRehydrationLoadsAsTheTasksUser(t *testing.T) {
	mgr := skill.NewManager()
	mgr.AddAvailable(&skill.SkillInfo{Name: "docs"})
	loader := &stubSkillLoader{}
	r := &skillRehydratorImpl{manager: mgr, loader: loader, logger: slog.Default()}
	taskCtx := &tool.Context{UserID: "user-1", Identity: &auth.Identity{UserID: "user-1", AuthToken: "user-token"}}

	r.RehydrateSkills(t.Context(), taskCtx, []string{"docs"})

	if len(loader.callers) != 1 || loader.callers[0] != taskCtx {
		t.Fatalf("load_skill callers = %v, want the task's own tool context", loader.callers)
	}
}

func TestRehydrationSkipsTheSTRForASkillTheAgentHolds(t *testing.T) {
	mgr := skill.NewManager()
	mgr.AddAvailable(&skill.SkillInfo{Name: "docs"})
	mgr.SetLoaded("docs", &skill.LoadedSkill{Name: "docs"})
	loader := &stubSkillLoader{}
	r := &skillRehydratorImpl{manager: mgr, loader: loader, logger: slog.Default()}

	failed := r.RehydrateSkills(t.Context(), &tool.Context{}, []string{"docs"})

	if len(failed) != 0 || len(loader.loaded) != 0 {
		t.Errorf("failed = %v, fetched = %v; want neither for a held skill", failed, loader.loaded)
	}
}

func TestRehydrationReportsASkillTheSTRDidNotServe(t *testing.T) {
	mgr := skill.NewManager()
	mgr.AddAvailable(&skill.SkillInfo{Name: "docs"})
	loader := &stubSkillLoader{failWith: errors.New("authentication failed: missing auth token")}
	r := &skillRehydratorImpl{manager: mgr, loader: loader, logger: slog.Default()}

	failed := r.RehydrateSkills(t.Context(), &tool.Context{}, []string{"docs"})

	if !slices.Equal(failed, []string{"docs"}) || mgr.IsHeld("docs") {
		t.Errorf("failed = %v, held = %v; want docs failed and not held", failed, mgr.IsHeld("docs"))
	}
}
