// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestFindFreePort_FallsBackWhenLoopbackPortHeld(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer occupied.Close()
	busyPort := occupied.Addr().(*net.TCPAddr).Port

	got := findFreePort("127.0.0.1", busyPort)
	if got == busyPort {
		t.Fatalf("findFreePort returned the busy port %d; expected a fallback port", busyPort)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", got))
	if err != nil {
		t.Fatalf("findFreePort returned %d but loopback bind failed: %v", got, err)
	}
	_ = ln.Close()
}

func TestFindFreePort_WildcardHost_FallsBackWhenPortHeld(t *testing.T) {
	occupied, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer occupied.Close()
	busyPort := occupied.Addr().(*net.TCPAddr).Port

	got := findFreePort("", busyPort)
	if got == busyPort {
		t.Fatalf("findFreePort returned the busy port %d; expected a fallback port", busyPort)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", got))
	if err != nil {
		t.Fatalf("findFreePort returned %d but wildcard bind failed: %v", got, err)
	}
	_ = ln.Close()
}

func TestFindFreePort_ReturnsPreferredWhenFree(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	free := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if got := findFreePort("127.0.0.1", free); got != free {
		t.Errorf("findFreePort(%d) = %d, want %d (port was free)", free, got, free)
	}
}

func TestFindFreePort_LoopbackProbeSucceedsAlongsideWildcardHolder(t *testing.T) {
	occupied, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer occupied.Close()
	busyPort := occupied.Addr().(*net.TCPAddr).Port

	got := findFreePort("127.0.0.1", busyPort)

	if got == busyPort {
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", got))
	if err != nil {
		t.Fatalf("findFreePort returned %d but loopback bind failed: %v", got, err)
	}
	_ = ln.Close()
}

func TestDiscoverTopLevelConfigs_LastDirWinsOnDuplicate(t *testing.T) {
	bundled := t.TempDir()
	user := t.TempDir()

	for _, dir := range []string{bundled, user} {
		if err := os.WriteFile(filepath.Join(dir, "salesforce-agent.yaml"), []byte("apps: []\n"), 0644); err != nil {
			t.Fatalf("write yaml: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(bundled, "bundled-only.yaml"), []byte("apps: []\n"), 0644); err != nil {
		t.Fatalf("write bundled-only: %v", err)
	}
	if err := os.WriteFile(filepath.Join(user, "user-only.yaml"), []byte("apps: []\n"), 0644); err != nil {
		t.Fatalf("write user-only: %v", err)
	}

	got, err := discoverTopLevelConfigs([]string{bundled, user})
	if err != nil {
		t.Fatalf("discoverTopLevelConfigs: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 unique files, got %d: %v", len(got), got)
	}

	wantSalesforce := filepath.Join(user, "salesforce-agent.yaml")
	abs, _ := filepath.Abs(wantSalesforce)
	found := false
	for _, p := range got {
		if p == abs {
			found = true
		}
		if p == filepath.Join(bundled, "salesforce-agent.yaml") {
			t.Errorf("bundled copy should have been shadowed by user copy: %s", p)
		}
	}
	if !found {
		t.Errorf("expected user-dir salesforce-agent.yaml in result, got %v", got)
	}
}

func TestDiscoverTopLevelConfigs_SkipsUnderscoreAndShared(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"_skip.yaml", "shared_config.yaml", "shared_config_inner.yaml", "real.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("apps: []\n"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	got, err := discoverTopLevelConfigs([]string{dir})
	if err != nil {
		t.Fatalf("discoverTopLevelConfigs: %v", err)
	}
	if len(got) != 1 || filepath.Base(got[0]) != "real.yaml" {
		t.Errorf("expected only real.yaml, got %v", got)
	}
}

func TestReadFileBase64_ReadsOnlyAFileChosenInTheDialog(t *testing.T) {
	dir := t.TempDir()
	chosen := filepath.Join(dir, "chosen.txt")
	other := filepath.Join(dir, "other.txt")
	for _, p := range []string{chosen, other} {
		if err := os.WriteFile(p, []byte("hi"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := NewApp()
	a.rememberPicked(chosen)

	if _, err := a.ReadFileBase64(other); !errors.Is(err, errFileNotPicked) {
		t.Fatalf("reading a file never chosen: err = %v, want errFileNotPicked", err)
	}
	got, err := a.ReadFileBase64(chosen)
	if err != nil || got != "aGk=" {
		t.Fatalf("reading the chosen file = %q, %v; want its contents", got, err)
	}
	if _, err := a.ReadFileBase64(chosen); !errors.Is(err, errFileNotPicked) {
		t.Fatalf("reading the chosen file twice: err = %v, want errFileNotPicked", err)
	}
}
