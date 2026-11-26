// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package awe

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skill"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/remote"
)

type tokenRequiredAuthenticator struct{ token string }

func (a tokenRequiredAuthenticator) Authenticate(_ context.Context, token, _ string) (*auth.Identity, error) {
	if token != a.token {
		return nil, errors.New("invalid token")
	}
	return &auth.Identity{UserID: "user-1", AuthToken: token}, nil
}

func TestRehydrationReachesAnSTRThatRequiresAToken(t *testing.T) {
	const (
		namespace = "rehydrate-auth"
		agentName = "docs-agent"
		marker    = "DOCS_REHYDRATE_MARKER"
	)
	brk := broker.NewDevBroker()
	svc := runtime.NewRuntime(brk, nil, runtime.WithAuthenticator(tokenRequiredAuthenticator{token: "user-token"})).Services()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	skillsDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skillsDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: docs\ndescription: docs skill\n---\n\n" + marker + "\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "docs", "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(manifest, []byte("version: 1\ntools: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	worker := str.NewWorker(svc, str.WorkerConfig{
		WorkerID:      "str-rehydrate-auth",
		Namespace:     namespace,
		ManifestPaths: []string{manifest},
		SkillsDir:     skillsDir,
		Sandbox:       str.SandboxConfig{Mode: "direct", MaxConcurrentExecutions: 1},
	})
	if err := worker.Init(t.Context(), nil); err != nil {
		t.Fatalf("worker init: %v", err)
	}
	if err := worker.Start(t.Context()); err != nil {
		t.Fatalf("worker start: %v", err)
	}
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	exec := remote.NewExecutor(brk, namespace, agentName)
	if err := exec.Start(t.Context()); err != nil {
		t.Fatalf("start executor: %v", err)
	}
	t.Cleanup(func() { exec.Stop() })

	mgr := skill.NewManager()
	mgr.AddAvailable(&skill.SkillInfo{Name: "docs"})
	r := &skillRehydratorImpl{manager: mgr, loader: &strSkillLoader{executor: exec}, logger: slog.Default()}

	anonymous := &tool.Context{AgentName: agentName, UserID: "user-1", SessionID: "s1"}
	if failed := r.RehydrateSkills(t.Context(), anonymous, []string{"docs"}); !slices.Equal(failed, []string{"docs"}) {
		t.Fatalf("failed = %v; a reload with no token must be refused", failed)
	}

	task := &tool.Context{AgentName: agentName, UserID: "user-1", SessionID: "s1",
		Identity: &auth.Identity{UserID: "user-1", AuthToken: "user-token"}}
	if failed := r.RehydrateSkills(t.Context(), task, []string{"docs"}); len(failed) != 0 {
		t.Fatalf("failed = %v; the reload as the task's user must succeed", failed)
	}
	held := mgr.GetLoaded("docs")
	if held == nil || !strings.Contains(held.InstructionContent, marker) {
		t.Errorf("held copy = %+v, want the skill's instructions from the STR", held)
	}
}
