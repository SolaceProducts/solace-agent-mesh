// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/bundle"
)

func TestHash_IncludesBundleFormatVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tool"), []byte{0x7f, 'E', 'L', 'F'}, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	plan := Plan{Kind: KindNone, SrcDir: dir}
	target := Target{OS: "linux", Arch: "amd64"}

	got, err := Hash(plan, target, dir)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}

	next, err := hashWithBundleVersion(plan, target, dir, bundle.FormatVersion+1)
	if err != nil {
		t.Fatalf("control hash: %v", err)
	}
	if got == next {
		t.Errorf("bundle.FormatVersion (%d) does not reach the cache key", bundle.FormatVersion)
	}

	same, err := hashWithBundleVersion(plan, target, dir, bundle.FormatVersion)
	if err != nil {
		t.Fatalf("same-version hash: %v", err)
	}
	if got != same {
		t.Errorf("Hash does not use bundle.FormatVersion: %s != %s", got, same)
	}
}
