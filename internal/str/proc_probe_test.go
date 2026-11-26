// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

const kataProcStderr = "bwrap: Can't mount proc on /newroot/proc: Operation not permitted"

func TestDecideFreshProc(t *testing.T) {
	tests := []struct {
		name     string
		exitCode int
		stderr   string
		want     bool
	}{
		{"clean success keeps", 0, "", true},
		{"success with unrelated stderr keeps", 0, "some warning", true},
		{"success but proc-rejection stderr drops", 0, kataProcStderr, false},
		{"kata exit drops", 1, kataProcStderr, false},
		{"generic failure drops", 2, "boom", false},
		{"start failure drops", -1, "exec: not found", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideFreshProc(tt.exitCode, tt.stderr); got != tt.want {
				t.Errorf("decideFreshProc(%d, %q) = %v, want %v", tt.exitCode, tt.stderr, got, tt.want)
			}
		})
	}
}

func TestResolveFreshProc(t *testing.T) {
	tests := []struct {
		mode   string
		probed bool
		want   bool
	}{
		{freshProcAlways, false, true},
		{freshProcAlways, true, true},
		{freshProcNever, true, false},
		{freshProcNever, false, false},
		{freshProcAuto, true, true},
		{freshProcAuto, false, false},
		{"", true, true},
		{"", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.mode+"/"+boolStr(tt.probed), func(t *testing.T) {
			cfg := SandboxConfig{FreshProc: tt.mode}
			if got := resolveFreshProc(cfg, tt.probed); got != tt.want {
				t.Errorf("resolveFreshProc(%q, %v) = %v, want %v", tt.mode, tt.probed, got, tt.want)
			}
		})
	}
}

func boolStr(b bool) string {
	if b {
		return "probed"
	}
	return "unprobed"
}

func fakeProbe(exit int, stderr string, err error, calls *int) execProbeFunc {
	return func(_ context.Context, _ string, _ ...string) (int, string, error) {
		*calls++
		return exit, stderr, err
	}
}

func newProbeRunner(mode, freshProcMode string) *SandboxRunner {
	r := &SandboxRunner{
		cfg: SandboxConfig{
			Mode:      mode,
			BwrapBin:  "/usr/bin/bwrap",
			FreshProc: freshProcMode,
		},
		logger: slog.Default(),
	}
	r.cfg.setDefaults()
	return r
}

func TestProbeFreshProc(t *testing.T) {
	tests := []struct {
		name       string
		exit       int
		stderr     string
		err        error
		wantKeep   bool
		wantFailed bool
	}{
		{"keep", 0, "", nil, true, false},
		{"kernel rejection", 1, kataProcStderr, nil, false, false},
		{"zero exit but rejection stderr", 0, kataProcStderr, nil, false, false},
		{"malfunction nonzero exit", 2, "boom", nil, false, true},
		{"start error", -1, "", errors.New("exec: not found"), false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			keep, failed, _, _ := probeFreshProc(context.Background(), "/usr/bin/bwrap",
				fakeProbe(tt.exit, tt.stderr, tt.err, &calls))
			if keep != tt.wantKeep || failed != tt.wantFailed {
				t.Errorf("probeFreshProc keep=%v failed=%v, want keep=%v failed=%v", keep, failed, tt.wantKeep, tt.wantFailed)
			}
		})
	}
}

func TestRealProbeExec(t *testing.T) {
	ctx := context.Background()

	if code, _, err := realProbeExec(ctx, "/bin/sh", "-c", "exit 0"); err != nil || code != 0 {
		t.Errorf("exit 0: code=%d err=%v", code, err)
	}
	if code, _, err := realProbeExec(ctx, "/bin/sh", "-c", "exit 7"); err != nil || code != 7 {
		t.Errorf("exit 7: code=%d err=%v", code, err)
	}
	if _, _, err := realProbeExec(ctx, "/nonexistent/bwrap-xyz"); err == nil {
		t.Error("expected start error for missing binary")
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := realProbeExec(cctx, "/bin/sh", "-c", "exit 0"); err == nil {
		t.Error("expected context error when ctx already cancelled")
	}
}

func TestValidateFreshProc(t *testing.T) {
	for _, v := range []string{"", "auto", "always", "never"} {
		if err := validateFreshProc(v); err != nil {
			t.Errorf("validateFreshProc(%q) = %v, want nil", v, err)
		}
	}
	for _, v := range []string{"Auto", "alway", "off", "true", "disabled"} {
		if err := validateFreshProc(v); err == nil {
			t.Errorf("validateFreshProc(%q) = nil, want error", v)
		}
	}
}

func TestResolveFreshProcSupport_ConfigSkipsProbe(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{freshProcAlways, true},
		{freshProcNever, false},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			calls := 0
			r := newProbeRunner("bwrap", tt.mode)
			r.resolveFreshProcSupport(context.Background(), fakeProbe(0, "", nil, &calls))
			if calls != 0 {
				t.Errorf("probe must be skipped for %q; got %d calls", tt.mode, calls)
			}
			if r.freshProc != tt.want {
				t.Errorf("freshProc = %v, want %v", r.freshProc, tt.want)
			}
		})
	}
}

func TestResolveFreshProcSupport_Auto(t *testing.T) {
	tests := []struct {
		name   string
		exit   int
		stderr string
		err    error
		want   bool
	}{
		{"gvisor keeps", 0, "", nil, true},
		{"kata drops", 1, kataProcStderr, nil, false},
		{"generic failure drops", 2, "boom", nil, false},
		{"start error drops", -1, "", errors.New("exec: \"bwrap\": not found"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			r := newProbeRunner("bwrap", freshProcAuto)
			r.resolveFreshProcSupport(context.Background(), fakeProbe(tt.exit, tt.stderr, tt.err, &calls))
			if calls != 1 {
				t.Errorf("auto mode must run the probe exactly once; got %d", calls)
			}
			if r.freshProc != tt.want {
				t.Errorf("freshProc = %v, want %v", r.freshProc, tt.want)
			}
		})
	}
}

func TestResolveFreshProcSupport_NonBwrapModeNoOp(t *testing.T) {
	calls := 0
	r := newProbeRunner("direct", freshProcAuto)
	r.resolveFreshProcSupport(context.Background(), fakeProbe(0, "", nil, &calls))
	if calls != 0 {
		t.Errorf("probe must not run outside bwrap mode; got %d calls", calls)
	}
	if r.freshProc {
		t.Error("freshProc must stay false outside bwrap mode")
	}
}

func TestWarnIfBwrapSetuid(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "bwrap-plain")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	setuid := filepath.Join(dir, "bwrap-setuid")
	if err := os.WriteFile(setuid, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(setuid, 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(setuid); err != nil || info.Mode()&os.ModeSetuid == 0 {
		t.Skipf("filesystem did not preserve the setuid bit (mode=%v, err=%v)", info.Mode(), err)
	}

	tests := []struct {
		name     string
		bwrapBin string
		want     bool
	}{
		{"non-setuid", plain, false},
		{"setuid", setuid, true},
		{"missing", filepath.Join(dir, "nope"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &SandboxRunner{cfg: SandboxConfig{BwrapBin: tt.bwrapBin}, logger: slog.Default()}
			if got := r.warnIfBwrapSetuid(); got != tt.want {
				t.Errorf("warnIfBwrapSetuid() = %v, want %v", got, tt.want)
			}
		})
	}
}
