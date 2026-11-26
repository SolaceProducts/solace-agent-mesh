// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/sdkvendor"
)

func TestRunInjectsMissingGoSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes `go build`; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	repo := scaffoldGoToolForTest(t)
	if err := os.RemoveAll(filepath.Join(repo, "_sdk")); err != nil {
		t.Fatalf("remove _sdk: %v", err)
	}

	plan, err := Resolve("scaffolded", repo)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if plan.Kind != KindGoConvention {
		t.Fatalf("expected KindGoConvention, got %v", plan.Kind)
	}

	var stderr bytes.Buffer
	if _, err := Run(context.Background(), plan, RunOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("Run: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "vendored samtoolsdk") {
		t.Errorf("expected vendored-samtoolsdk log line, got:\n%s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(repo, "_sdk", "samtoolsdk", "run.go")); err != nil {
		t.Errorf("_sdk not restored after build: %v", err)
	}
}

func TestRunRefreshesOnEnvOptIn(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes `go build`; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	repo := scaffoldGoToolForTest(t)
	stale := filepath.Join(repo, "_sdk", "samtoolsdk", "stale-marker.txt")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SAM_TOOL_SDK_REFRESH", "1")

	plan, err := Resolve("scaffolded", repo)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var stderr bytes.Buffer
	if _, err := Run(context.Background(), plan, RunOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("Run: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "vendored samtoolsdk") {
		t.Errorf("expected refresh log line, got:\n%s", stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale marker survived SAM_TOOL_SDK_REFRESH=1")
	}
}

func TestRunSilentWhenSDKPresent(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes `go build`; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	repo := scaffoldGoToolForTest(t)
	plan, err := Resolve("scaffolded", repo)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var stderr bytes.Buffer
	if _, err := Run(context.Background(), plan, RunOptions{Stderr: &stderr}); err != nil {
		t.Fatalf("Run: %v\n%s", err, stderr.String())
	}
	if strings.Contains(stderr.String(), "vendored samtoolsdk") {
		t.Errorf("expected no log line when SDK already present, got:\n%s", stderr.String())
	}
}

func scaffoldGoToolForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must := func(rel, body string) {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("main.go", `package main

import (
	"context"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type P struct {
	Name string `+"`json:\"name\"`"+`
}

func greet(ctx context.Context, p P, tc *sdk.ToolContext) (*sdk.Result, error) {
	_ = ctx
	_ = tc
	return sdk.OK("hi " + p.Name), nil
}

func main() { sdk.Run(sdk.NewTool("greet", "hi", greet)) }
`)
	must("go.mod", `module scaffolded

go 1.26

require github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk v0.0.0

replace github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk => ./_sdk/samtoolsdk
`)

	if _, err := sdkvendor.EnsureGoSDK(dir); err != nil {
		t.Fatalf("inject SDK: %v", err)
	}
	return dir
}
