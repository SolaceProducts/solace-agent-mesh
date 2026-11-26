// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/skills/design"
)

func requireGeneratedSchemas(t *testing.T) {
	t.Helper()
	if _, err := loadSchemaSubstitutions(); err != nil {
		var pathErr interface{ Error() string }
		if errors.As(err, &pathErr) && strings.Contains(err.Error(), "connector.schema.yaml: file does not exist") {
			t.Skipf("skipping: connector schema not generated (run `make generate` first); raw: %v", err)
		}
	}
}

func expectedBundledAgentSkills() []string {
	var out []string
	for _, sk := range design.Skills() {
		out = append(out, sk.Name)
	}
	out = append(out, schemaSkillNames()...)
	slices.Sort(out)
	return out
}

func TestReconcileBundledAgentSkills_UploadsAllOnEmptyStore(t *testing.T) {
	t.Parallel()
	requireGeneratedSchemas(t)

	ctx := context.Background()
	store := newReconcileTestStore()
	logger := slog.New(slog.DiscardHandler)

	if err := reconcileBundledAgentSkills(ctx, store, "test-ns", logger); err != nil {
		t.Fatalf("reconcileBundledAgentSkills: %v", err)
	}

	for _, name := range expectedBundledAgentSkills() {
		key := skillRuntimeKey("test-ns", name)
		md, headErr := store.Head(ctx, key)
		if headErr != nil {
			t.Errorf("expected %q to exist after reconcile: %v", key, headErr)
			continue
		}
		if md[builtinSkillHashMetadata] == "" {
			t.Errorf("missing hash metadata for %q (have %v)", key, md)
		}
		rc, err := store.Get(ctx, key)
		if err != nil {
			t.Errorf("Get %q: %v", key, err)
			continue
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if len(data) == 0 {
			t.Errorf("reconciled %q is empty", key)
		}
	}
}

func TestReconcileBundledAgentSkills_SkipsWhenHashMatches(t *testing.T) {
	t.Parallel()
	requireGeneratedSchemas(t)

	ctx := context.Background()
	rs := newReconcileTestStore()
	logger := slog.New(slog.DiscardHandler)

	if err := reconcileBundledAgentSkills(ctx, rs, "ns", logger); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	tracker := &putCountingStore{Store: rs}
	if err := reconcileBundledAgentSkills(ctx, tracker, "ns", logger); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if tracker.puts != 0 {
		t.Errorf("second reconcile should have short-circuited via hash; got %d uploads", tracker.puts)
	}
}

func TestSchemaSkillZip_PlaceholdersResolved(t *testing.T) {
	t.Parallel()
	requireGeneratedSchemas(t)

	subst, err := loadSchemaSubstitutions()
	if err != nil {
		t.Fatalf("loadSchemaSubstitutions: %v", err)
	}
	zipBytes, _, err := buildSchemaSkillZip("sam-agent-schema", subst)
	if err != nil {
		t.Fatalf("buildSchemaSkillZip: %v", err)
	}

	skillMD := readZipEntry(t, zipBytes, "SKILL.md")
	if strings.Contains(skillMD, "{{SCHEMA:") {
		t.Error("unresolved {{SCHEMA:…}} placeholder leaked into the zip's SKILL.md")
	}
	if strings.Contains(skillMD, "{{TOOL_GROUPS_TABLE}}") {
		t.Error("unresolved {{TOOL_GROUPS_TABLE}} placeholder leaked into the zip's SKILL.md")
	}
}

func TestDesignSkillZip_LayoutAndBytesMatchSharedPackage(t *testing.T) {
	t.Parallel()

	for _, sk := range design.Skills() {
		t.Run(sk.Name, func(t *testing.T) {
			t.Parallel()
			zipBytes, _, err := buildDesignSkillZip(sk)
			if err != nil {
				t.Fatalf("buildDesignSkillZip: %v", err)
			}

			names := zipEntryNames(t, zipBytes)
			bodyEntry := "references/" + sk.BodyFile
			if !slices.Contains(names, "SKILL.md") {
				t.Fatalf("missing SKILL.md (entries: %v)", names)
			}
			if !slices.Contains(names, bodyEntry) {
				t.Fatalf("missing reference entry %q (entries: %v)", bodyEntry, names)
			}

			router, err := sk.Router()
			if err != nil {
				t.Fatalf("%s.Router(): %v", sk.Name, err)
			}
			if got := readZipEntry(t, zipBytes, "SKILL.md"); got != string(router) {
				t.Errorf("SKILL.md differs from %s.Router()", sk.Name)
			}
			body, err := sk.Body(design.Builder)
			if err != nil {
				t.Fatalf("%s.Body(Builder): %v", sk.Name, err)
			}
			if got := readZipEntry(t, zipBytes, bodyEntry); got != string(body) {
				t.Errorf("%s differs from %s.Body(Builder)", bodyEntry, sk.Name)
			}
		})
	}
}

func TestBuildDesignSkillZip_MissingGuideErrors(t *testing.T) {
	t.Parallel()
	_, _, err := buildDesignSkillZip(design.Skill{Name: "sam-missing", Guide: "missing", BodyFile: "missing.md"})
	if err == nil {
		t.Fatal("buildDesignSkillZip() succeeded for a guide the design package does not embed")
	}
}

func readZipEntry(t *testing.T, zipBytes []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %q: %v", name, err)
			}
			data, readErr := io.ReadAll(rc)
			closeErr := rc.Close()
			if readErr != nil {
				t.Fatalf("read %q: %v", name, readErr)
			}
			if closeErr != nil {
				t.Fatalf("close %q: %v", name, closeErr)
			}
			return string(data)
		}
	}
	t.Fatalf("entry %q not found (have: %v)", name, zipEntryNames(t, zipBytes))
	return ""
}

func zipEntryNames(t *testing.T, zipBytes []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	return names
}
