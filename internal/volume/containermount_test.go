// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package volume

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMapHostToContainer_UnderScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-style fixtures")
	}
	cases := []struct {
		name     string
		scope    string
		hostPath string
		want     string
	}{
		{"direct child", "/home/u", "/home/u/projects/foo", "/host/home/projects/foo"},
		{"nested", "/home/u", "/home/u/a/b/c", "/host/home/a/b/c"},
		{"scope itself", "/home/u", "/home/u", "/host/home"},
		{"trailing slash on scope", "/home/u/", "/home/u/x", "/host/home/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MapHostToContainer(tc.scope, tc.hostPath)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMapHostToContainer_OutsideScope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-style fixtures")
	}
	_, err := MapHostToContainer("/home/u", "/etc/passwd")
	if !errors.Is(err, ErrPathOutsideScope) {
		t.Fatalf("expected ErrPathOutsideScope, got %v", err)
	}
}

func TestMapHostToContainer_RejectsRelativeHostPath(t *testing.T) {
	_, err := MapHostToContainer("/anything", "relative/path")
	if err == nil {
		t.Fatalf("expected error for relative host path")
	}
}

func TestMapHostToContainer_EmptyScope(t *testing.T) {
	_, err := MapHostToContainer("", "/anywhere")
	if err == nil {
		t.Fatalf("expected error for empty scope")
	}
}

func TestDefaultHostMountScope_OverrideNone(t *testing.T) {
	t.Setenv(scopeEnvOverride, "none")
	if got := DefaultHostMountScope(); got != "" {
		t.Fatalf(`got %q, want "" for "none" override`, got)
	}
}

func TestDefaultHostMountScope_OverridePath(t *testing.T) {
	t.Setenv(scopeEnvOverride, "/explicit/path")
	if got := DefaultHostMountScope(); got != "/explicit/path" {
		t.Fatalf("got %q, want /explicit/path", got)
	}
}

func TestDefaultHostMountScope_DefaultsToHome(t *testing.T) {
	t.Setenv(scopeEnvOverride, "")
	got := DefaultHostMountScope()
	if got == "" {
		t.Skip("UserHomeDir failed; nothing to assert")
	}
	if !filepath.IsAbs(got) {
		t.Errorf("default scope should be absolute, got %q", got)
	}
}
