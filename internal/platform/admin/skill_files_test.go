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
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/objectstore"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type memResourceStore struct{ m map[string][]byte }

func newMemResourceStore() *memResourceStore { return &memResourceStore{m: map[string][]byte{}} }

func (s *memResourceStore) Put(_ context.Context, key string, r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.m[key] = b
	return nil
}

func (s *memResourceStore) PutWithMetadata(ctx context.Context, key string, r io.Reader, _ map[string]string) error {
	return s.Put(ctx, key, r)
}

func (s *memResourceStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	b, ok := s.m[key]
	if !ok {
		return nil, &objectstore.NotFoundError{Key: key}
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (s *memResourceStore) Head(_ context.Context, key string) (map[string]string, error) {
	if _, ok := s.m[key]; !ok {
		return nil, errors.New("not found")
	}
	return map[string]string{}, nil
}

func (s *memResourceStore) Delete(_ context.Context, key string) error {
	delete(s.m, key)
	return nil
}

func buildSkillZIP(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(prefix + name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func newSkillFilesFixture(t *testing.T, prefix string, files map[string]string) (*SkillService, string) {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))
	rs := newMemResourceStore()
	svc.WithStorage(rs, "default")

	skill := &store.Skill{Name: "my-skill", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if err := rs.Put(t.Context(), skillRuntimeKey("default", "my-skill"), bytes.NewReader(buildSkillZIP(t, prefix, files))); err != nil {
		t.Fatalf("put zip: %v", err)
	}
	return svc, skill.ID
}

func standardSkillFiles() map[string]string {
	return map[string]string{
		"SKILL.md":            "# skill\n",
		"references/guide.md": "reference guide",
		"references/api.md":   "api reference",
		"assets/logo.txt":     "asset content",
		"tools/manifest.yaml": "tools: []",
	}
}

func TestSkillFiles_ListsReferencesAndAssets(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{"", "my-skill/"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			t.Parallel()
			svc, id := newSkillFilesFixture(t, prefix, standardSkillFiles())
			resp, err := svc.SkillFiles(t.Context(), id)
			if err != nil {
				t.Fatalf("SkillFiles: %v", err)
			}
			gotRefs := []string{resp.References[0].Path, resp.References[1].Path}
			if len(resp.References) != 2 || gotRefs[0] != "api.md" || gotRefs[1] != "guide.md" {
				t.Errorf("references = %+v, want sorted [api.md guide.md]", resp.References)
			}
			if len(resp.Assets) != 1 || resp.Assets[0].Path != "logo.txt" {
				t.Errorf("assets = %+v, want [logo.txt]", resp.Assets)
			}
			if resp.Assets[0].SizeBytes != int64(len("asset content")) {
				t.Errorf("logo.txt size = %d, want %d", resp.Assets[0].SizeBytes, len("asset content"))
			}
		})
	}
}

func TestSkillFileContent_ServesFiles(t *testing.T) {
	t.Parallel()
	svc, id := newSkillFilesFixture(t, "", standardSkillFiles())
	for _, tc := range []struct{ path, want string }{
		{"references/guide.md", "reference guide"},
		{"SKILL.md", "# skill\n"},
		{"assets/logo.txt", "asset content"},
	} {
		content, ct, err := svc.SkillFileContent(t.Context(), id, tc.path)
		if err != nil {
			t.Fatalf("SkillFileContent(%q): %v", tc.path, err)
		}
		if string(content) != tc.want {
			t.Errorf("content(%q) = %q, want %q", tc.path, content, tc.want)
		}
		if ct == "" {
			t.Errorf("content-type(%q) empty", tc.path)
		}
	}
}

func TestSkillFileContent_RejectsUnsafeOrUnknownPaths(t *testing.T) {
	t.Parallel()
	svc, id := newSkillFilesFixture(t, "", standardSkillFiles())

	for _, bad := range []string{"../secret", "references/../../etc/passwd", "/etc/passwd", "tools/manifest.yaml", "config_yaml", ""} {
		_, _, err := svc.SkillFileContent(t.Context(), id, bad)
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("SkillFileContent(%q) error = %T %v, want *ValidationError", bad, err, err)
		}
	}

	_, _, err := svc.SkillFileContent(t.Context(), id, "references/missing.md")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("missing file error = %T %v, want *NotFoundError", err, err)
	}
}

func TestSkillFiles_NoBundleYieldsEmptyLists(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))
	svc.WithStorage(newMemResourceStore(), "default")

	skill := &store.Skill{Name: "no-bundle", Origin: "platform", DiscoveryStatus: "created", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	resp, err := svc.SkillFiles(t.Context(), skill.ID)
	if err != nil {
		t.Fatalf("SkillFiles (no bundle) should not error, got: %v", err)
	}
	if resp.References == nil || resp.Assets == nil || len(resp.References) != 0 || len(resp.Assets) != 0 {
		t.Errorf("SkillFiles (no bundle) = %+v, want empty non-nil lists", resp)
	}

	_, _, err = svc.SkillFileContent(t.Context(), skill.ID, "references/x.md")
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Errorf("SkillFileContent (no bundle) error = %T %v, want *NotFoundError", err, err)
	}
}

func TestSkillFiles_NilStoreIsValidationError(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))

	skill := &store.Skill{Name: "no-store", Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	_, err := svc.SkillFiles(t.Context(), skill.ID)
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("SkillFiles (nil store) error = %T %v, want *ValidationError", err, err)
	}
}

func TestSkillFiles_ExcludesEntriesOutsideBundleRoot(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"my-skill/SKILL.md":             "# skill\n",
		"my-skill/references/inside.md": "in",
		"references/stray.md":           "out",
	}
	svc, id := newSkillFilesFixture(t, "", files)
	resp, err := svc.SkillFiles(t.Context(), id)
	if err != nil {
		t.Fatalf("SkillFiles: %v", err)
	}
	if len(resp.References) != 1 || resp.References[0].Path != "inside.md" {
		t.Errorf("references = %+v, want only [inside.md] (stray root entry excluded)", resp.References)
	}
}

func TestSkillFileContent_NeutralizesExecutableTypes(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"SKILL.md":             "# skill\n",
		"references/page.html": "<script>alert(1)</script>",
		"assets/logo.svg":      "<svg onload=\"alert(1)\"></svg>",
		"references/notes.md":  "# notes",
	}
	svc, id := newSkillFilesFixture(t, "", files)

	for _, path := range []string{"references/page.html", "assets/logo.svg"} {
		_, ct, err := svc.SkillFileContent(t.Context(), id, path)
		if err != nil {
			t.Fatalf("SkillFileContent(%q): %v", path, err)
		}
		if !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("content-type(%q) = %q, want text/plain (executable types must be neutralized)", path, ct)
		}
	}

	_, ct, err := svc.SkillFileContent(t.Context(), id, "references/notes.md")
	if err != nil {
		t.Fatalf("SkillFileContent(notes.md): %v", err)
	}
	if !strings.Contains(strings.ToLower(ct), "markdown") && !strings.HasPrefix(ct, "text/") {
		t.Errorf("markdown content-type = %q, want a text/markdown or text/* type", ct)
	}
}

func TestSkillFileContent_RejectsOversizeFile(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"SKILL.md":        "# skill\n",
		"assets/big.bin":  strings.Repeat("x", maxSkillPreviewBytes+10),
		"assets/small.md": "ok",
	}
	svc, id := newSkillFilesFixture(t, "", files)

	if _, _, err := svc.SkillFileContent(t.Context(), id, "assets/small.md"); err != nil {
		t.Fatalf("small file should serve: %v", err)
	}
	_, _, err := svc.SkillFileContent(t.Context(), id, "assets/big.bin")
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Error(), "too large") {
		t.Errorf("oversize file error = %T %v, want *ValidationError mentioning 'too large'", err, err)
	}
}
