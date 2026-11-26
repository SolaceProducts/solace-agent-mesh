// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sdkvendor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVendoredGoSDKKeepsItsDeclaredGoVersion(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	if _, err := EnsureGoSDK(dir); err != nil {
		t.Fatalf("EnsureGoSDK: %v", err)
	}

	cmd := exec.CommandContext(t.Context(), goBin, "vet", "./...")
	cmd.Dir = filepath.Join(dir, VendorDirName(), GoSDKDirName())
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go vet on the vendored SDK failed; it must build on the Go version its go.mod declares:\n%s", out)
	}
}

func TestEnsureGoSDKWritesWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	injected, err := EnsureGoSDK(dir)
	if err != nil {
		t.Fatalf("EnsureGoSDK: %v", err)
	}
	if !injected {
		t.Errorf("injected=false on empty dir, want true")
	}
	got := filepath.Join(dir, VendorDirName(), GoSDKDirName(), "run.go")
	if _, err := os.Stat(got); err != nil {
		t.Errorf("expected %s to exist: %v", got, err)
	}
}

func TestEnsureGoSDKIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureGoSDK(dir); err != nil {
		t.Fatalf("first EnsureGoSDK: %v", err)
	}
	injected, err := EnsureGoSDK(dir)
	if err != nil {
		t.Fatalf("second EnsureGoSDK: %v", err)
	}
	if injected {
		t.Errorf("injected=true on second call, want false")
	}
}

func TestForceGoSDKOverwrites(t *testing.T) {
	dir := t.TempDir()
	vendorDir := filepath.Join(dir, "_sdk", "samtoolsdk")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	stale := filepath.Join(vendorDir, "stale.txt")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatalf("write stale: %v", err)
	}
	if err := ForceGoSDK(dir); err != nil {
		t.Fatalf("ForceGoSDK: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale file still exists after ForceGoSDK")
	}
	if _, err := os.Stat(filepath.Join(vendorDir, "run.go")); err != nil {
		t.Errorf("expected run.go after force: %v", err)
	}
}

func TestEnsureGoSDKRejectsMissingDir(t *testing.T) {
	if _, err := EnsureGoSDK(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected error for missing dir")
	}
}

func TestForceGoSDKRejectsMissingDir(t *testing.T) {
	if err := ForceGoSDK(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected error for missing dir")
	}
}
