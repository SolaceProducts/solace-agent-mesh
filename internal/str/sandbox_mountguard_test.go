// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

func TestUnderAnyPrefix(t *testing.T) {
	prefixes := []string{"/tmp", "/var", "/tmp/work"}
	cases := []struct {
		path string
		want bool
	}{
		{"/tmp", true},
		{"/tmp/skill", true},
		{"/var/cache/x", true},
		{"/tmp/work/out", true},
		{"/skill", false},
		{"/volumes/data", false},
		{"/tmpfoo", false},
		{"/var-data", false},
	}
	for _, c := range cases {
		if got := underAnyPrefix(c.path, prefixes); got != c.want {
			t.Errorf("underAnyPrefix(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestWritableBwrapPrefixes_VarGatedOnProfile(t *testing.T) {
	if got := writableBwrapPrefixes(SandboxProfile{WritableVar: false}, "/tmp/work"); underAnyPrefix("/var/x", got) {
		t.Error("/var must not be writable when profile.WritableVar is false")
	}
	if got := writableBwrapPrefixes(SandboxProfile{WritableVar: true}, "/tmp/work"); !underAnyPrefix("/var/x", got) {
		t.Error("/var must be writable when profile.WritableVar is true")
	}
}

func TestCheckBindMountPoints(t *testing.T) {
	writable := []string{"/tmp"}

	err := checkBindMountPoints([]string{"/nonexistent-mount-xyz-123"}, writable)
	if !errors.Is(err, errBwrapMountPointMissing) {
		t.Errorf("missing non-writable dest: got %v, want errBwrapMountPointMissing", err)
	}

	for _, dest := range []string{"", "/tmp/skill", "/"} {
		if err := checkBindMountPoints([]string{dest}, writable); err != nil {
			t.Errorf("checkBindMountPoints(%q) = %v, want nil", dest, err)
		}
	}
}

func TestBuildBwrapCommand_MissingMountPointFails(t *testing.T) {
	runner := hardeningTestRunner(t)
	entry := &ManifestEntry{
		ToolName:       "dates__date_math",
		SandboxProfile: "standard",
		ExtraMounts: []ExtraMount{
			{HostPath: t.TempDir(), MountPath: "/skill", ReadOnly: true},
		},
	}

	cmd, err := runner.buildBwrapCommand(context.Background(), entry, []string{"/tmp/args.json"}, "/tmp/work", nil, "", slog.Default())
	if !errors.Is(err, errBwrapMountPointMissing) {
		t.Fatalf("expected errBwrapMountPointMissing for unbaked /skill mount, got err=%v", err)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd when the guard fails, got %v", cmd)
	}
}
