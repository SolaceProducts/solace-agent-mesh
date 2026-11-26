// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

func TestSafeJoin_RelativePath(t *testing.T) {
	root := t.TempDir()
	got, err := safeJoin(root, filepath.Join("src", "main.go"))
	if err != nil {
		t.Fatalf("safeJoin: %v", err)
	}
	want := filepath.Join(root, "src", "main.go")
	if got != want {
		t.Errorf("safeJoin = %q, want %q", got, want)
	}
}

func TestSafeJoin_AbsolutePathUnderRoot(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "sub", "file.txt")
	got, err := safeJoin(root, abs)
	if err != nil {
		t.Fatalf("safeJoin: %v", err)
	}
	if got != abs {
		t.Errorf("safeJoin = %q, want %q", got, abs)
	}
}

func TestSafeJoin_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	bad := []string{
		"../etc/passwd",
		"../../etc/passwd",
		"sub/../../etc/passwd",
		"/etc/passwd",
		filepath.Join(root, "..", "outside"),
	}
	for _, p := range bad {
		t.Run(p, func(t *testing.T) {
			_, err := safeJoin(root, p)
			if err == nil {
				t.Errorf("safeJoin(%q, %q) succeeded; expected escape rejection", root, p)
			}
		})
	}
}

func TestSafeJoin_PrefixCollision(t *testing.T) {
	root := "/workspace"
	got, err := safeJoin(root, "/workspace-other/file.txt")
	if err == nil {
		t.Errorf("safeJoin admitted prefix-colliding sibling: got %q", got)
	}
}

func TestSafeJoin_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink create not supported on this platform: %v", err)
	}
	_, err := safeJoin(root, "escape/secret.txt")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink-escape rejection, got %v", err)
	}
}

func TestSafeJoin_AllowsSymlinkInsideRoot(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "alias")); err != nil {
		t.Skipf("symlink create not supported: %v", err)
	}
	if _, err := safeJoin(root, "alias"); err != nil {
		t.Errorf("intra-volume symlink rejected: %v", err)
	}
}

func TestSafeJoin_RootItself(t *testing.T) {
	root := t.TempDir()
	got, err := safeJoin(root, "")
	if err != nil {
		t.Fatalf("safeJoin(root, \"\"): %v", err)
	}
	if got != root {
		t.Errorf("safeJoin(root, \"\") = %q, want %q", got, root)
	}
}

func TestTruncateForOutput(t *testing.T) {
	short := make([]byte, 1024)
	if out := truncateForOutput(short); len(out) != 1024 {
		t.Errorf("short input changed: %d != 1024", len(out))
	}

	big := make([]byte, maxOutputBytes+5000)
	out := truncateForOutput(big)
	if len(out) <= maxOutputBytes {
		t.Errorf("truncate dropped too much: %d <= %d", len(out), maxOutputBytes)
	}
	if !strings.Contains(string(out), "volume_fs: output truncated") {
		t.Error("truncation marker missing from output")
	}
	if !strings.Contains(string(out), "5000 more bytes") {
		t.Errorf("expected marker to name dropped bytes: %s", string(out)[maxOutputBytes:])
	}
}

func TestVolumeRoot_ExplicitWorkspaceLookup(t *testing.T) {
	tc := &sdk.ToolContext{
		VolumeMounts: []sdk.VolumeMount{
			{VolumeID: "other", MountPath: "/somewhere-else"},
			{VolumeID: "ws", MountPath: "/workspace"},
		},
	}
	got, err := volumeRoot(tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/workspace" {
		t.Errorf("explicit lookup should win and return /workspace, got %q", got)
	}
}

func TestVolumeRoot_AmbiguousMultiMount(t *testing.T) {
	tc := &sdk.ToolContext{
		VolumeMounts: []sdk.VolumeMount{
			{VolumeID: "a", MountPath: "/host/a"},
			{VolumeID: "b", MountPath: "/host/b"},
		},
	}
	if _, err := volumeRoot(tc); err == nil {
		t.Error("ambiguous multi-mount with no /workspace should error, got nil")
	}
}

func TestVolumeRoot_SingleMountFallback(t *testing.T) {
	tc := &sdk.ToolContext{
		VolumeMounts: []sdk.VolumeMount{{VolumeID: "only", MountPath: "/host/only"}},
	}
	got, err := volumeRoot(tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/host/only" {
		t.Errorf("single-mount fallback should return /host/only, got %q", got)
	}
}

func TestVolumeRoot_NoMountErrors(t *testing.T) {
	if _, err := volumeRoot(&sdk.ToolContext{}); err == nil {
		t.Error("no volume mount should error, got nil")
	}
}
