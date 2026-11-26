// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package skillruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleHashIsTheRecordedDigestOfTheArchiveTheSkillCameFrom(t *testing.T) {
	dir := t.TempDir()
	if got := BundleHash(dir); got != "" {
		t.Errorf("a skill with no recorded archive -> %q, want none", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.zip.extracted"), []byte("the archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := BundleHash(dir); got != "" {
		t.Errorf("an archive with no recorded digest -> %q, want none: the runtime reads only what it recorded", got)
	}
	digest := strings.Repeat("0f", 32)
	if err := RecordBundleHash(dir, digest); err != nil {
		t.Fatal(err)
	}
	if got := BundleHash(dir); got != digest {
		t.Errorf("recorded digest -> %q, want %q", got, digest)
	}
	for _, bad := range []string{strings.ToUpper(digest), digest[:63], "sha256:" + digest[:57]} {
		if err := os.WriteFile(filepath.Join(dir, BundleHashFile), []byte(bad), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := BundleHash(dir); got != "" {
			t.Errorf("a record holding %q -> %q, want none", bad, got)
		}
	}
}

func TestALoadedSkillReportsItsBundleHashOnlyWhenItHasOne(t *testing.T) {
	tmp := t.TempDir()
	buildFixtureSkill(t, tmp, "fixture")
	ctx, err := LoadFromDisk(tmp, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, present := LoadSkill(ctx).AsMap()["content_hash"]; present {
		t.Error("a skill with no recorded archive reported a content hash")
	}
	want := strings.Repeat("ab", 32)
	if err = RecordBundleHash(filepath.Join(tmp, "fixture"), want); err != nil {
		t.Fatal(err)
	}
	if ctx, err = LoadFromDisk(tmp, "fixture"); err != nil {
		t.Fatal(err)
	}
	if got := LoadSkill(ctx).AsMap()["content_hash"]; got != want {
		t.Errorf("content_hash = %v, want the recorded digest %s", got, want)
	}
}
