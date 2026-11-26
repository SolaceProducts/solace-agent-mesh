// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func newClientAgainstServer(s *httptest.Server) *Client {
	return NewClient(target.Resolved{URL: s.URL})
}

func newBuildTargetClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/toolsetBuildTarget", h)
	return newV2PlatformClient(t, mux)
}

func TestResolver_EnvBothSet(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "linux")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")

	called := false
	r := NewPlatformBuildTargetResolver(newBuildTargetClient(t, func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	osS, archS, _, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve err: %v", err)
	}
	if osS != "linux" || archS != "amd64" {
		t.Errorf("Resolve = (%q, %q); want (linux, amd64)", osS, archS)
	}
	if called {
		t.Error("platform should not be consulted when both env vars are set")
	}
}

func TestResolver_PlatformSingleArch(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	r := NewPlatformBuildTargetResolver(newBuildTargetClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, apiv2.ToolsetBuildTarget{OS: "linux", Arch: "arm64", Source: "str_init", PythonVersion: new("3.11")})
	}))
	osS, archS, pyVer, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve err: %v", err)
	}
	if osS != "linux" || archS != "arm64" {
		t.Errorf("Resolve = (%q, %q); want (linux, arm64)", osS, archS)
	}
	if pyVer != "3.11" {
		t.Errorf("Resolve pythonVersion = %q; want 3.11 (from platform)", pyVer)
	}
}

func TestResolver_MixedFleet(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	var hits atomic.Int32
	r := NewPlatformBuildTargetResolver(newBuildTargetClient(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		writeV2Error(w, http.StatusConflict, "mixed", map[string][]string{"targets": {"linux/amd64", "linux/arm64"}})
	}))
	_, _, _, err := r.Resolve(context.Background())
	if err == nil {
		t.Fatal("expected MixedTargetError")
	}
	if mix, ok := errors.AsType[*MixedTargetError](err); !ok {
		t.Errorf("err = %v; want *MixedTargetError", err)
	} else if len(mix.Targets) != 2 || mix.Targets[0] != "linux/amd64" {
		t.Errorf("MixedTargetError.Targets = %v; want [linux/amd64 linux/arm64]", mix.Targets)
	}

	for range 10 {
		_, _, _, err2 := r.Resolve(context.Background())
		if err2 == nil || err2.Error() != err.Error() {
			t.Errorf("second Resolve err = %v; want same error as first %v", err2, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("platform hit count = %d; want exactly 1 (resolver should cache)", got)
	}
}

func TestResolver_PlatformUnreachable(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	r := NewPlatformBuildTargetResolver(newBuildTargetClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, _, _, err := r.Resolve(context.Background())
	ute, ok := errors.AsType[*UndiscoverableTargetError](err)
	if !ok {
		t.Fatalf("Resolve err = %v; want *UndiscoverableTargetError", err)
	}
	if ute.Unreachable == nil {
		t.Error("UndiscoverableTargetError.Unreachable should carry the underlying transport error")
	}
}

func TestResolver_FallbackSourceIsUndiscoverable(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "")
	r := NewPlatformBuildTargetResolver(newBuildTargetClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, apiv2.ToolsetBuildTarget{OS: "linux", Arch: "arm64", Source: "fallback"})
	}))
	_, _, _, err := r.Resolve(context.Background())
	if ute, ok := errors.AsType[*UndiscoverableTargetError](err); !ok {
		t.Fatalf("Resolve err = %v; want *UndiscoverableTargetError", err)
	} else if ute.Unreachable != nil {
		t.Errorf("fallback (reachable) should not set Unreachable; got %v", ute.Unreachable)
	}
}

func TestResolver_FallbackSourceWithSingleEnvPin(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	r := NewPlatformBuildTargetResolver(newBuildTargetClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, apiv2.ToolsetBuildTarget{OS: "linux", Arch: "arm64", Source: "fallback"})
	}))
	if _, _, _, err := r.Resolve(context.Background()); err == nil {
		t.Fatal("expected UndiscoverableTargetError; a single env pin must not rescue a fallback answer")
	} else if _, ok := errors.AsType[*UndiscoverableTargetError](err); !ok {
		t.Fatalf("Resolve err = %v; want *UndiscoverableTargetError", err)
	}
}

func TestResolver_PartialEnvOverride(t *testing.T) {
	t.Setenv("SAM_TOOL_TARGET_OS", "")
	t.Setenv("SAM_TOOL_TARGET_ARCH", "amd64")
	r := NewPlatformBuildTargetResolver(newBuildTargetClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Single(w, http.StatusOK, apiv2.ToolsetBuildTarget{OS: "linux", Arch: "arm64", Source: "str_init"})
	}))
	osS, archS, _, err := r.Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve err: %v", err)
	}
	if osS != "linux" || archS != "amd64" {
		t.Errorf("Resolve = (%q, %q); want (linux, amd64) — env arch should win, platform os should be used", osS, archS)
	}
}
