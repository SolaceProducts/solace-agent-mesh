// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	evalplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/eval"
)

func TestStreamNewResultsSkipsUnscored(t *testing.T) {
	pass := true
	scored := evalplatform.RunResultResponse{
		ID: "r-scored", CreatedTime: 1, InputPrompt: new("scored prompt"),
		Scores: []evalplatform.ScoreResponse{{EvaluatorID: "e1", Score: 1, Passed: &pass}},
	}
	unscored := evalplatform.RunResultResponse{
		ID: "r-unscored", CreatedTime: 2, InputPrompt: new("pending prompt"),
	}

	var buf bytes.Buffer
	lg := &lineLogger{mu: &sync.Mutex{}, w: &buf}
	seen := map[string]bool{}

	got := streamNewResults(lg, []evalplatform.RunResultResponse{scored, unscored}, seen, 0, 2, nil, false)
	if got != 1 {
		t.Fatalf("completed = %d, want 1 (only the scored result)", got)
	}
	if seen["r-unscored"] {
		t.Fatal("unscored result must not be marked seen on a normal tick")
	}
	if !strings.Contains(buf.String(), "scored prompt") || strings.Contains(buf.String(), "pending prompt") {
		t.Fatalf("unexpected normal-tick output:\n%s", buf.String())
	}

	buf.Reset()
	got = streamNewResults(lg, []evalplatform.RunResultResponse{scored, unscored}, seen, got, 2, nil, true)
	if got != 2 {
		t.Fatalf("completed = %d, want 2 after force flush", got)
	}
	if !strings.Contains(buf.String(), "pending prompt") {
		t.Fatalf("force flush should print the unscored result:\n%s", buf.String())
	}
}

func TestEvalCommandStructure(t *testing.T) {
	cmd := newEvalCmd()

	expected := []string{"run", "list"}
	for _, name := range expected {
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("eval subcommand %q not found", name)
		}
	}
}

func TestEvalRunRequiresTarget(t *testing.T) {
	isolateTargetEnv(t)
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"eval", "run", "nightly"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when no target can be resolved")
	}
}

func isolateTargetEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SAM_WEBUI_URL", "")
	t.Setenv("SAM_AUTH_TOKEN", "")
	t.Setenv("SAM_PLATFORM_TOKEN", "")
}

func TestEvalRunRequiresExperimentName(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"eval", "run", "--target", "http://example"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when experiment name is missing")
	}
}

func TestEvalListRequiresTarget(t *testing.T) {
	isolateTargetEnv(t)
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"eval", "list"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when no target can be resolved")
	}
}

func TestEvalRejectsStrayPositional(t *testing.T) {
	for _, args := range [][]string{
		{"eval", "tests/eval_suite.json"},
		{"eval", "suite", "tests/eval_suite.json"},
	} {
		cmd := newRootCmd()
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		cmd.SetArgs(args)
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("expected error for stray positional %v", args)
		}
		if !strings.Contains(err.Error(), "sam eval run") {
			t.Errorf("expected error to point at `sam eval run`; got %q", err.Error())
		}
	}
}
