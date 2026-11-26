// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/str"
)

const (
	extractedTestNamespace = "ns"
	extractedTestSkill     = "extracted_skill"
	extractedTestSuffix    = ".extracted"
	extractedTestSkillMD   = "---\nname: extracted_skill\ndescription: test\n---\n\nBody.\n"
)

func strRuntimeDir(base, kind string) string {
	return filepath.Join(base, extractedTestNamespace, "str-runtime", kind)
}

func TestToolsetService_Download_AfterSTRExtractsTheZIP(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	base := t.TempDir()
	logger := slog.New(slog.DiscardHandler)

	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	storage := newToolsetStorage(objectstore.NewStore(objectstore.NewFilesystemClient(base)), logger)
	svc := NewToolsetService(db, repo, storage, nil, extractedTestNamespace, logger)

	pkg, err := svc.Create(ctx, CreateToolsetRequest{Name: "extracted_pkg", Description: "desc"}, "tester")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	zipBytes := buildValidZIP(t, map[string][]byte{"a.py": []byte("a = 1")})
	if err = svc.Upload(ctx, pkg.ID, "pkg.zip", zipBytes, "tester"); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	toolsDir := strRuntimeDir(base, "toolsets")
	if _, _, err = str.NewPackageProcessor(toolsDir, logger).ProcessAll(ctx); err != nil {
		t.Fatalf("STR ProcessAll: %v", err)
	}
	if keyExists(ctx, t, objectstore.NewStore(objectstore.NewFilesystemClient(base)), packageKey(extractedTestNamespace, "extracted_pkg")) {
		t.Fatal("STR left package.zip in place; the test no longer reproduces the rename")
	}

	rc, filename, size, err := svc.Download(ctx, pkg.ID)
	if err != nil {
		t.Fatalf("Download after STR extraction: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(body, zipBytes) {
		t.Errorf("downloaded %d bytes, want the %d uploaded", len(body), len(zipBytes))
	}
	if filename != "pkg.zip" || size != int64(len(zipBytes)) {
		t.Errorf("filename, size = %q, %d; want pkg.zip, %d", filename, size, len(zipBytes))
	}
}

func newExtractedSkillEnv(t *testing.T) (*skillOutboxFixture, string, string) {
	t.Helper()
	base := t.TempDir()
	f := newSkillOutboxFixture(t)
	f.svc.WithStorage(objectstore.NewStore(objectstore.NewFilesystemClient(base)), extractedTestNamespace)
	sk := f.seedReady(t.Context(), t, extractedTestSkill, skillSchemaSecretRequired)
	return f, sk.ID, strRuntimeDir(base, "skills")
}

func uploadAndExtractSkill(t *testing.T, f *skillOutboxFixture, id, skillsDir string, files map[string]string) []byte {
	t.Helper()
	data := buildSkillZIP(t, "", files)
	if err := f.svc.UploadZIP(t.Context(), id, extractedTestSkill, data, "tester"); err != nil {
		t.Fatalf("UploadZIP: %v", err)
	}
	if _, err := str.NewSkillProcessor(skillsDir, slog.New(slog.DiscardHandler)).ProcessAll(); err != nil {
		t.Fatalf("STR ProcessAll: %v", err)
	}
	return data
}

func TestSkillService_Download_AfterSTRExtractsTheZIP(t *testing.T) {
	t.Parallel()
	f, id, skillsDir := newExtractedSkillEnv(t)
	data := uploadAndExtractSkill(t, f, id, skillsDir, map[string]string{
		"SKILL.md":            extractedTestSkillMD,
		"references/guide.md": "reference guide",
	})

	rc, filename, err := f.svc.Download(t.Context(), id)
	if err != nil {
		t.Fatalf("Download after STR extraction: %v", err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(body, data) {
		t.Errorf("downloaded %d bytes, want the %d uploaded", len(body), len(data))
	}
	if filename != extractedTestSkill+".zip" {
		t.Errorf("filename = %q", filename)
	}
}

func TestSkillService_Files_AfterSTRExtractsTheZIP(t *testing.T) {
	t.Parallel()
	f, id, skillsDir := newExtractedSkillEnv(t)
	uploadAndExtractSkill(t, f, id, skillsDir, map[string]string{
		"SKILL.md":            extractedTestSkillMD,
		"references/guide.md": "reference guide",
	})

	files, err := f.svc.SkillFiles(t.Context(), id)
	if err != nil {
		t.Fatalf("SkillFiles: %v", err)
	}
	if len(files.References) != 1 || files.References[0].Path != "guide.md" {
		t.Errorf("References = %+v, want guide.md", files.References)
	}
	content, _, err := f.svc.SkillFileContent(t.Context(), id, "references/guide.md")
	if err != nil {
		t.Fatalf("SkillFileContent: %v", err)
	}
	if string(content) != "reference guide" {
		t.Errorf("content = %q", content)
	}
}

func TestSkillService_UploadZIP_AfterSTRExtractsKeepsStoredSecret(t *testing.T) {
	t.Parallel()
	f, id, skillsDir := newExtractedSkillEnv(t)
	secretMD := "---\nname: extracted_skill\ndescription: test\ntools:\n  - tool_type: builtin\n    tool_name: web_search_google\n    tool_config:\n      google_search_api_key: " + fakeSearchKey + "\n---\nBody.\n"
	uploadAndExtractSkill(t, f, id, skillsDir, map[string]string{"SKILL.md": secretMD})

	rc, _, err := f.svc.Download(t.Context(), id)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	redacted, _ := io.ReadAll(rc)
	_ = rc.Close()

	if err = f.svc.UploadZIP(t.Context(), id, extractedTestSkill, redacted, "tester"); err != nil {
		t.Fatalf("re-upload of the downloaded bundle: %v", err)
	}
	rs := f.svc.resourceStore
	stored, err := rs.Get(t.Context(), skillRuntimeKey(extractedTestNamespace, extractedTestSkill))
	if err != nil {
		t.Fatalf("Get stored: %v", err)
	}
	defer func() { _ = stored.Close() }()
	raw, _ := io.ReadAll(stored)
	if !bytes.Contains(readEntries(t, raw)["SKILL.md"], []byte(fakeSearchKey)) {
		t.Error("stored SKILL.md lost the original secret")
	}
}

func TestSkillService_DeleteWithCleanup_RemovesTheExtractedZIP(t *testing.T) {
	t.Parallel()
	f, id, skillsDir := newExtractedSkillEnv(t)
	uploadAndExtractSkill(t, f, id, skillsDir, map[string]string{"SKILL.md": extractedTestSkillMD})

	if err := f.svc.DeleteWithCleanup(t.Context(), id); err != nil {
		t.Fatalf("DeleteWithCleanup: %v", err)
	}
	key := skillRuntimeKey(extractedTestNamespace, extractedTestSkill)
	for _, k := range []string{key, key + extractedTestSuffix} {
		if rc, err := f.svc.resourceStore.Get(t.Context(), k); err == nil {
			_ = rc.Close()
			t.Errorf("%s still stored after the skill was deleted", k)
		}
	}
}

func TestSkillService_Download_MissingBundleStaysNotFound(t *testing.T) {
	t.Parallel()
	f, id, _ := newExtractedSkillEnv(t)
	_, _, err := f.svc.Download(t.Context(), id)
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("error = %T %v, want *NotFoundError", err, err)
	}
}

func downloadSkillFrom(t *testing.T, rs ResourceStore) error {
	t.Helper()
	f := newSkillOutboxFixture(t)
	f.svc.WithStorage(rs, extractedTestNamespace)
	sk := f.seedReady(t.Context(), t, extractedTestSkill, skillSchemaSecretRequired)
	rc, _, err := f.svc.Download(t.Context(), sk.ID)
	if err == nil {
		_ = rc.Close()
	}
	return err
}

type failingGetStore struct{ ResourceStore }

func (failingGetStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("object store unavailable")
}

func TestSkillService_Download_StoreFailureIsNotAMissingBundle(t *testing.T) {
	t.Parallel()
	err := downloadSkillFrom(t, failingGetStore{newMemResourceStore()})
	if err == nil || errors.As(err, new(*NotFoundError)) {
		t.Fatalf("error = %v, want the store failure", err)
	}
}

type extractedFailingStore struct{ *memResourceStore }

func (s extractedFailingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if strings.HasSuffix(key, extractedTestSuffix) {
		return nil, errors.New("object store unavailable")
	}
	return s.memResourceStore.Get(ctx, key)
}

func TestSkillService_Download_ExtractedLookupFailureIsReported(t *testing.T) {
	t.Parallel()
	err := downloadSkillFrom(t, extractedFailingStore{newMemResourceStore()})
	if err == nil || errors.As(err, new(*NotFoundError)) || objectstore.IsNotFound(err) {
		t.Fatalf("error = %v, want the store failure", err)
	}
}

func TestSkillService_Download_PrefersAFreshZIPOverTheExtractedOne(t *testing.T) {
	t.Parallel()
	f, id, _ := newExtractedSkillEnv(t)
	rs := f.svc.resourceStore
	key := skillRuntimeKey(extractedTestNamespace, extractedTestSkill)
	old := buildSkillZIP(t, "", map[string]string{"SKILL.md": extractedTestSkillMD, "references/a.md": "old"})
	fresh := buildSkillZIP(t, "", map[string]string{"SKILL.md": extractedTestSkillMD, "references/a.md": "fresh"})
	if err := rs.Put(t.Context(), key+".extracted", bytes.NewReader(old)); err != nil {
		t.Fatalf("put extracted: %v", err)
	}
	if err := rs.Put(t.Context(), key, bytes.NewReader(fresh)); err != nil {
		t.Fatalf("put fresh: %v", err)
	}

	content, _, err := f.svc.SkillFileContent(t.Context(), id, "references/a.md")
	if err != nil {
		t.Fatalf("SkillFileContent: %v", err)
	}
	if string(content) != "fresh" {
		t.Errorf("content = %q, want the fresh upload", content)
	}
}
