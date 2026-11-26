// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type testSkillEnv struct {
	svc  *SkillService
	repo *store.SkillRepo
}

func setupSkillEnv(t *testing.T) *testSkillEnv {
	t.Helper()

	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))
	svc.WithStorage(objectstore.NewStore(objectstore.NewMemoryClient()), "test-ns")
	return &testSkillEnv{svc: svc, repo: repo}
}

func buildSkillZIPWithContent(t *testing.T, skillMDContent string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("SKILL.md")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := f.Write([]byte(skillMDContent)); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestValidateSkillZIP_NameMatch(t *testing.T) {
	t.Parallel()

	skillMD := "---\nname: my-skill\ndescription: test\n---\n\nContent.\n"
	zipData := buildSkillZIPWithContent(t, skillMD)

	name, err := validateSkillZIP(zipData, "my-skill")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if name != "my-skill" {
		t.Errorf("extracted name = %q, want my-skill", name)
	}
}

func TestValidateSkillZIP_ExtractsName(t *testing.T) {
	t.Parallel()

	skillMD := "---\nname: extracted-skill\ndescription: test\n---\n\nContent.\n"
	zipData := buildSkillZIPWithContent(t, skillMD)

	name, err := validateSkillZIP(zipData, "")
	if err != nil {
		t.Fatalf("expected no error with empty expectedName, got: %v", err)
	}
	if name != "extracted-skill" {
		t.Errorf("extracted name = %q, want extracted-skill", name)
	}
}

func TestValidateSkillZIP_NameMismatch(t *testing.T) {
	t.Parallel()

	skillMD := "---\nname: actual-name\ndescription: test\n---\n\nContent.\n"
	zipData := buildSkillZIPWithContent(t, skillMD)

	_, err := validateSkillZIP(zipData, "expected-name")
	if err == nil {
		t.Fatal("expected error for name mismatch, got nil")
	}
	if !strings.Contains(err.Error(), "expected-name") {
		t.Errorf("error %q missing expected name", err.Error())
	}
}

func TestValidateSkillZIP_MissingSkillMD(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("README.md")
	_, _ = f.Write([]byte("just a readme"))
	_ = zw.Close()

	_, err := validateSkillZIP(buf.Bytes(), "any-skill")
	if err == nil {
		t.Fatal("expected error for missing SKILL.md, got nil")
	}
}

func TestValidateSkillZIP_MalformedFrontmatter(t *testing.T) {
	t.Parallel()

	skillMD := "---\n: invalid: yaml:\n---\n\nBody.\n"
	zipData := buildSkillZIPWithContent(t, skillMD)

	_, err := validateSkillZIP(zipData, "any-skill")
	if err == nil {
		t.Fatal("expected error for malformed frontmatter, got nil")
	}
}

func TestValidateSkillZIP_NoFrontmatter(t *testing.T) {
	t.Parallel()

	skillMD := "Just markdown content without frontmatter.\n"
	zipData := buildSkillZIPWithContent(t, skillMD)

	_, err := validateSkillZIP(zipData, "any-skill")
	if err == nil {
		t.Fatal("expected error when frontmatter is missing, got nil")
	}
}

func TestValidateSkillZIP_PathTraversal(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("../etc/passwd")
	_, _ = f.Write([]byte("evil"))
	_ = zw.Close()

	_, err := validateSkillZIP(buf.Bytes(), "any-skill")
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
}
