// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/spf13/cobra"
)

func newToolsetTargetCmdForTest() (*cobra.Command, *bytes.Buffer) {
	cmd := newToolsetBuildTargetCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd, out
}

func TestToolsetTargetCmd_EnvOverride_Text(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	cmd, out := newToolsetTargetCmdForTest()

	if err := runToolsetBuildTarget(context.Background(), cmd, "", targetFormatText); err != nil {
		t.Fatalf("runToolsetBuildTarget: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "linux/amd64" {
		t.Errorf("text output = %q; want %q", got, "linux/amd64")
	}
}

func TestToolsetTargetCmd_PlatformJSON(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	authedTestToken(t)
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "str_init"})
	}))
	t.Cleanup(srv.Close)

	cmd, out := newToolsetTargetCmdForTest()
	if err := runToolsetBuildTarget(context.Background(), cmd, srv.URL, targetFormatJSON); err != nil {
		t.Fatalf("runToolsetBuildTarget: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode json: %v\n%s", err, out.String())
	}
	want := map[string]string{"os": "linux", "arch": "arm64", "source": "str-init"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("json[%s] = %q; want %q", k, got[k], v)
		}
	}
}

func TestToolsetTargetCmd_ShellFormat(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "arm64")
	cmd, out := newToolsetTargetCmdForTest()
	if err := runToolsetBuildTarget(context.Background(), cmd, "", targetFormatShell); err != nil {
		t.Fatalf("runToolsetBuildTarget: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "GOOS=linux GOARCH=arm64" {
		t.Errorf("shell output = %q; want %q", got, "GOOS=linux GOARCH=arm64")
	}
}

func TestToolsetTargetCmd_FallbackIsHardError(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	authedTestToken(t)
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "fallback"})
	}))
	t.Cleanup(srv.Close)

	cmd, _ := newToolsetTargetCmdForTest()
	err := runToolsetBuildTarget(context.Background(), cmd, srv.URL, targetFormatText)
	if err == nil {
		t.Fatal("expected error on source=fallback; got nil (would print a guessed target)")
	}
	if _, ok := errors.AsType[*reconcile.UndiscoverableTargetError](err); !ok {
		t.Fatalf("err = %v; want *reconcile.UndiscoverableTargetError", err)
	}
}

func TestToolsetTargetCmd_RequiresTargetWithoutEnv(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	cmd, _ := newToolsetTargetCmdForTest()
	err := runToolsetBuildTarget(context.Background(), cmd, "", targetFormatText)
	if err == nil || !strings.Contains(err.Error(), "--url (or --target) is required") {
		t.Errorf("err = %v; want '--url (or --target) is required' message", err)
	}
}

func TestToolsetTargetCmd_UnauthenticatedFallsBackToAnonymous(t *testing.T) {
	emptyCredentialCache(t)
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")

	var gotAuth string
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeV2Single(w, http.StatusOK, map[string]string{"os": "darwin", "arch": "arm64", "source": "str_init"})
	}))
	t.Cleanup(srv.Close)

	cmd, out := newToolsetTargetCmdForTest()
	if err := runToolsetBuildTarget(context.Background(), cmd, srv.URL, targetFormatText); err != nil {
		t.Fatalf("runToolsetBuildTarget: %v (unauthenticated query must fall back to anonymous)", err)
	}
	if got := strings.TrimSpace(out.String()); got != "darwin/arm64" {
		t.Errorf("text output = %q; want %q", got, "darwin/arm64")
	}
	if gotAuth != "" {
		t.Errorf("Authorization header = %q; want empty (anonymous fallback)", gotAuth)
	}
}

func TestToolsetTargetCmd_SendsAuthHeader(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	t.Setenv("SAM_PLATFORM_TOKEN", "tok-123")
	var gotAuth string
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		writeV2Single(w, http.StatusOK, map[string]string{"os": "linux", "arch": "arm64", "source": "str_init"})
	}))
	t.Cleanup(srv.Close)

	cmd, _ := newToolsetTargetCmdForTest()
	if err := runToolsetBuildTarget(context.Background(), cmd, srv.URL, targetFormatText); err != nil {
		t.Fatalf("runToolsetBuildTarget: %v", err)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization header = %q; want %q", gotAuth, "Bearer tok-123")
	}
}

func TestToolsetTargetCmd_SecuredPlatformHintsLogin(t *testing.T) {
	emptyCredentialCache(t)
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")

	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusUnauthorized, "unauthorized")
	}))
	t.Cleanup(srv.Close)

	cmd, _ := newToolsetTargetCmdForTest()
	err := runToolsetBuildTarget(context.Background(), cmd, srv.URL, targetFormatText)
	if err == nil {
		t.Fatal("expected an error on 401; got nil")
	}
	if !strings.Contains(err.Error(), "sam auth login") {
		t.Errorf("err = %v; want it to mention `sam auth login`", err)
	}
}

func TestToolsetBuildTargetCmd_NoNetwork(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	stdout, _, err := runRoot(t, "toolset", "build-target")
	if err != nil {
		t.Fatalf("toolset build-target: %v", err)
	}
	if !strings.Contains(stdout, "linux/amd64") {
		t.Errorf("expected target output on stdout, got %q", stdout)
	}
}
