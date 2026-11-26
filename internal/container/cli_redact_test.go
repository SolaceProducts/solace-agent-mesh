// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package container

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"
)

func TestRedactedRunArgs_DropsEnvValuesKeepsNames(t *testing.T) {
	const canary = "sk-live-MUST-NOT-APPEAR"
	r := &CLIRuntime{binary: "docker"}

	args := r.buildRunArgs(RunOptions{
		Detach:  true,
		Name:    "sam-tool-1",
		Image:   "sam/tool:latest",
		Network: "sam-net",
		Env:     []string{"BROKER_PASSWORD=" + canary, "OPENAI_API_KEY=" + canary, "LOG_LEVEL=debug"},
	})

	if !slices.Contains(args, "BROKER_PASSWORD="+canary) {
		t.Fatalf("fixture no longer exercises the leak; args=%v", args)
	}

	got := strings.Join(redactedRunArgs(args), " ")
	if strings.Contains(got, canary) {
		t.Errorf("credential survived redaction: %q", got)
	}
	for _, name := range []string{"BROKER_PASSWORD=[REDACTED]", "OPENAI_API_KEY=[REDACTED]", "LOG_LEVEL=[REDACTED]"} {
		if !strings.Contains(got, name) {
			t.Errorf("lost the variable name %q, which is the diagnostic: %q", name, got)
		}
	}
	for _, keep := range []string{"--detach", "sam-tool-1", "sam/tool:latest", "sam-net"} {
		if !strings.Contains(got, keep) {
			t.Errorf("redaction removed non-env argv %q: %q", keep, got)
		}
	}
}

func TestRedactedRunArgs_LeavesValuelessEnvAlone(t *testing.T) {
	got := redactedRunArgs([]string{"run", "--env", "HOME", "img"})
	if !slices.Contains(got, "HOME") {
		t.Errorf("rewrote a valueless --env: %v", got)
	}
}

func TestRedactedRunArgs_DoesNotMutateInput(t *testing.T) {
	in := []string{"run", "--env", "K=secret", "img"}
	_ = redactedRunArgs(in)
	if in[2] != "K=secret" {
		t.Errorf("mutated the args actually handed to exec: %v", in)
	}
}

func TestRun_LogsRedactedArgv(t *testing.T) {
	const canary = "sk-live-MUST-NOT-APPEAR"
	var buf bytes.Buffer
	r := newCLIRuntime("/nonexistent/docker", RuntimeDocker,
		slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	_, err := r.Run(context.Background(), RunOptions{
		Image: "sam/tool:latest",
		Env:   []string{"BROKER_PASSWORD=" + canary},
	})
	if err == nil {
		t.Fatal("expected the exec of a nonexistent binary to fail")
	}

	got := buf.String()
	if !strings.Contains(got, "starting container") {
		t.Fatalf("the argv log no longer fires before exec; fixture is stale: %q", got)
	}
	if strings.Contains(got, canary) {
		t.Errorf("credential reached the log: %q", got)
	}
	if !strings.Contains(got, "BROKER_PASSWORD=[REDACTED]") {
		t.Errorf("lost the variable name: %q", got)
	}
}
