// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/skillsecret"
)

const (
	redactionSkillName = "my-skill"
	fakeSearchKey      = "AIza" + "FAKEKEY0000000000000000000000000000"
	fakeNewSearchKey   = "AIza" + "FAKENEWKEY000000000000000000000000000"
)

const skillMDWithSecret = `---
name: my-skill
description: Search the web
tools:
  - tool_type: builtin
    tool_name: web_search_google
    tool_config:
      google_search_api_key: ` + fakeSearchKey + `
      google_cse_id: e11bfake0000
---
# My skill

Body stays as written.
`

const cleanSkillMD = "---\nname:   my-skill\ndescription: clean\n---\nbody\n"

type redactionFixture struct {
	svc  *SkillService
	repo *store.SkillRepo
	rs   *memResourceStore
	id   string
}

func newRedactionFixture(t *testing.T, storedZIP []byte) *redactionFixture {
	t.Helper()
	db := testOpenDB(t)
	repo := store.NewSkillRepo(db)
	svc := NewSkillService(db, repo, slog.New(slog.DiscardHandler))
	rs := newMemResourceStore()
	svc.WithStorage(rs, "default")

	skill := &store.Skill{Name: redactionSkillName, Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	if storedZIP != nil {
		rs.m[skillRuntimeKey("default", redactionSkillName)] = storedZIP
	}
	return &redactionFixture{svc: svc, repo: repo, rs: rs, id: skill.ID}
}

func (f *redactionFixture) storedZIP() []byte {
	return f.rs.m[skillRuntimeKey("default", redactionSkillName)]
}

func bundle(t *testing.T, skillMD string) []byte {
	t.Helper()
	return buildSkillZIP(t, "", map[string]string{
		"SKILL.md":            skillMD,
		"references/guide.md": "reference guide mentions api_key: not-a-secret",
		"assets/logo.txt":     "asset content",
	})
}

func readEntries(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	out := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

func TestSkillFileContent_RedactsSkillMDOnly(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, bundle(t, skillMDWithSecret))

	manifest, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "SKILL.md")
	if err != nil {
		t.Fatalf("SkillFileContent(SKILL.md): %v", err)
	}
	if strings.Contains(string(manifest), fakeSearchKey) {
		t.Fatalf("SKILL.md leaked the secret:\n%s", manifest)
	}
	if !strings.Contains(string(manifest), skillsecret.RedactedSentinel) || !strings.Contains(string(manifest), "e11bfake0000") {
		t.Fatalf("SKILL.md should carry the sentinel and keep non-secrets:\n%s", manifest)
	}
	if !strings.HasSuffix(string(manifest), "# My skill\n\nBody stays as written.\n") {
		t.Fatalf("body changed:\n%s", manifest)
	}

	ref, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "references/guide.md")
	if err != nil || string(ref) != "reference guide mentions api_key: not-a-secret" {
		t.Fatalf("reference served modified: %q err=%v", ref, err)
	}
	asset, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "assets/logo.txt")
	if err != nil || string(asset) != "asset content" {
		t.Fatalf("asset served modified: %q err=%v", asset, err)
	}
}

func TestSkillFileContent_MalformedFrontMatterFailsClosed(t *testing.T) {
	t.Parallel()
	raw := "---\nname: [unclosed\ntools: {\n  api_key: " + fakeSearchKey + "\n---\nbody\n"
	fx := newRedactionFixture(t, bundle(t, raw))

	content, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "SKILL.md")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	if len(content) != 0 {
		t.Fatalf("raw bytes returned alongside the error: %q", content)
	}
}

func TestSkillDownload_RedactsSkillMDKeepsOtherEntries(t *testing.T) {
	t.Parallel()
	stored := bundle(t, skillMDWithSecret)
	fx := newRedactionFixture(t, stored)

	rc, filename, err := fx.svc.Download(t.Context(), fx.id)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	downloaded, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatalf("read download: %v", err)
	}
	if filename != "my-skill.zip" {
		t.Errorf("filename = %q", filename)
	}

	got, orig := readEntries(t, downloaded), readEntries(t, stored)
	if len(got) != len(orig) {
		t.Fatalf("entry count %d, want %d", len(got), len(orig))
	}
	for name, want := range orig {
		if name == "SKILL.md" {
			continue
		}
		if !bytes.Equal(got[name], want) {
			t.Errorf("entry %s changed", name)
		}
	}
	if bytes.Contains(got["SKILL.md"], []byte(fakeSearchKey)) || !bytes.Contains(got["SKILL.md"], []byte(skillsecret.RedactedSentinel)) {
		t.Fatalf("SKILL.md not redacted:\n%s", got["SKILL.md"])
	}
	if !bytes.Equal(fx.storedZIP(), stored) {
		t.Fatal("download must not modify the stored object")
	}
}

func TestSkillDownload_CleanSkillByteIdentical(t *testing.T) {
	t.Parallel()
	stored := bundle(t, cleanSkillMD)
	fx := newRedactionFixture(t, stored)

	rc, _, err := fx.svc.Download(t.Context(), fx.id)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	downloaded, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(downloaded, stored) {
		t.Fatal("clean skill download differs from the stored ZIP")
	}
}

func TestSkillDownload_MalformedFrontMatterFailsClosed(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, bundle(t, "---\nname: [unclosed\n---\nbody\n"))
	rc, _, err := fx.svc.Download(t.Context(), fx.id)
	if err == nil {
		_ = rc.Close()
		t.Fatal("expected an error, got a stream")
	}
}

func TestSkillDownload_MissingBundleIsNotFound(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, nil)
	_, _, err := fx.svc.Download(t.Context(), fx.id)
	var nfe *NotFoundError
	if !errors.As(err, &nfe) {
		t.Fatalf("error = %T %v, want *NotFoundError", err, err)
	}
}

func TestSkillUploadZIP_RedactedRoundTripKeepsStoredSecret(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, bundle(t, skillMDWithSecret))

	rc, _, err := fx.svc.Download(t.Context(), fx.id)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	redactedZIP, _ := io.ReadAll(rc)
	_ = rc.Close()

	if err = fx.svc.UploadZIP(t.Context(), fx.id, redactionSkillName, redactedZIP, "u"); err != nil {
		t.Fatalf("UploadZIP: %v", err)
	}

	stored := fx.storedZIP()
	entries := readEntries(t, stored)
	if !bytes.Contains(entries["SKILL.md"], []byte(fakeSearchKey)) {
		t.Fatalf("stored SKILL.md lost the original secret:\n%s", entries["SKILL.md"])
	}
	if bytes.Contains(entries["SKILL.md"], []byte(skillsecret.RedactedSentinel)) {
		t.Fatalf("sentinel persisted:\n%s", entries["SKILL.md"])
	}
	if string(entries["assets/logo.txt"]) != "asset content" {
		t.Fatal("other entries must survive the rebuild")
	}

	sum := sha256.Sum256(stored)
	hashes, err := fx.repo.ContentHashesByName(t.Context(), []string{redactionSkillName})
	if err != nil {
		t.Fatalf("ContentHashesByName: %v", err)
	}
	if hashes[redactionSkillName] != hex.EncodeToString(sum[:]) {
		t.Fatalf("content hash %q does not match the stored bytes", hashes[redactionSkillName])
	}
}

func TestSkillUploadZIP_SentinelWithoutStoredValueIsRejected(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, nil)
	incoming := strings.Replace(skillMDWithSecret, fakeSearchKey, skillsecret.RedactedSentinel, 1)

	err := fx.svc.UploadZIP(t.Context(), fx.id, redactionSkillName, bundle(t, incoming), "u")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	const wantPath = "tools[web_search_google].tool_config.google_search_api_key"
	if _, ok := ve.Details[wantPath]; !ok {
		t.Fatalf("details = %v, want key %q", ve.Details, wantPath)
	}
	if fx.storedZIP() != nil {
		t.Fatal("nothing should be stored on rejection")
	}
}

func TestSkillUploadZIP_ChangedLiteralOverridesStored(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, bundle(t, skillMDWithSecret))
	updated := strings.Replace(skillMDWithSecret, fakeSearchKey, fakeNewSearchKey, 1)

	if err := fx.svc.UploadZIP(t.Context(), fx.id, redactionSkillName, bundle(t, updated), "u"); err != nil {
		t.Fatalf("UploadZIP: %v", err)
	}
	if !bytes.Contains(readEntries(t, fx.storedZIP())["SKILL.md"], []byte("AIzaFAKENEWKEY")) {
		t.Fatal("a literal supplied on upload must win over the stored value")
	}
}

func TestRewriteSkillManifest_PreservesEntryMetadata(t *testing.T) {
	t.Parallel()
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	hdr := &zip.FileHeader{Name: "my-skill/SKILL.md", Method: zip.Store, Modified: modified}
	hdr.SetMode(0o640)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(skillMDWithSecret))
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := rewriteSkillManifests(buf.Bytes(), func(_ string, c []byte, top bool) ([]byte, error) { return redactSkillMD(c, top) })
	if err != nil {
		t.Fatalf("rewriteSkillManifest: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil || len(zr.File) != 1 {
		t.Fatalf("rebuilt zip unreadable: %v", err)
	}
	f := zr.File[0]
	if f.Name != "my-skill/SKILL.md" || f.Mode().Perm() != 0o640 || !f.Modified.Equal(modified) {
		t.Fatalf("metadata not preserved: name=%q mode=%v modified=%v", f.Name, f.Mode(), f.Modified)
	}
}

const nestedSecretKey = "AIza" + "FAKENESTED00000000000000000000000000"

func nestedBundle(t *testing.T) []byte {
	t.Helper()
	nested := strings.Replace(skillMDWithSecret, fakeSearchKey, nestedSecretKey, 1)
	return buildSkillZIP(t, "", map[string]string{
		"SKILL.md":            skillMDWithSecret,
		"references/SKILL.md": nested,
		"assets/skill.md":     nested,
		"assets/logo.txt":     "asset content",
	})
}

func TestSkillFileContent_RedactsNestedSkillMD(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, nestedBundle(t))
	for _, p := range []string{"SKILL.md", "references/SKILL.md"} {
		content, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, p)
		if err != nil {
			t.Fatalf("SkillFileContent(%s): %v", p, err)
		}
		if strings.Contains(string(content), fakeSearchKey) || strings.Contains(string(content), nestedSecretKey) {
			t.Fatalf("%s leaked a secret:\n%s", p, content)
		}
	}
}

func TestSkillDownload_RedactsEverySkillMDAtAnyDepth(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, nestedBundle(t))
	rc, _, err := fx.svc.Download(t.Context(), fx.id)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	downloaded, _ := io.ReadAll(rc)
	_ = rc.Close()
	for name, content := range readEntries(t, downloaded) {
		if bytes.Contains(content, []byte(fakeSearchKey)) || bytes.Contains(content, []byte(nestedSecretKey)) {
			t.Errorf("%s leaked a secret", name)
		}
	}
	if string(readEntries(t, downloaded)["assets/logo.txt"]) != "asset content" {
		t.Error("non-manifest entry changed")
	}
}

func TestSkillUploadZIP_RestoresNestedSkillMDFromSamePath(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, nestedBundle(t))
	rc, _, err := fx.svc.Download(t.Context(), fx.id)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	redactedZIP, _ := io.ReadAll(rc)
	_ = rc.Close()

	if err := fx.svc.UploadZIP(t.Context(), fx.id, redactionSkillName, redactedZIP, "u"); err != nil {
		t.Fatalf("UploadZIP: %v", err)
	}
	entries := readEntries(t, fx.storedZIP())
	if !bytes.Contains(entries["SKILL.md"], []byte(fakeSearchKey)) || bytes.Contains(entries["SKILL.md"], []byte(nestedSecretKey)) {
		t.Errorf("root SKILL.md restored from the wrong entry:\n%s", entries["SKILL.md"])
	}
	if !bytes.Contains(entries["references/SKILL.md"], []byte(nestedSecretKey)) || bytes.Contains(entries["references/SKILL.md"], []byte(fakeSearchKey)) {
		t.Errorf("nested SKILL.md restored from the wrong entry:\n%s", entries["references/SKILL.md"])
	}
}

func TestSkillUploadZIP_RepointedToolDoesNotInheritStoredSecret(t *testing.T) {
	t.Parallel()
	const stored = "---\nname: my-skill\ntools:\n  - tool_type: openapi\n    tool_name: api\n    base_url: https://good.example.com\n    auth:\n      type: apikey\n      value: FAKESTOREDVALUE\n---\nbody\n"
	fx := newRedactionFixture(t, bundle(t, stored))
	incoming := strings.NewReplacer("good.example.com", "attacker.example.com", "FAKESTOREDVALUE", skillsecret.RedactedSentinel).Replace(stored)

	err := fx.svc.UploadZIP(t.Context(), fx.id, redactionSkillName, bundle(t, incoming), "u")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	if bytes.Contains(fx.storedZIP(), []byte("attacker.example.com")) {
		t.Fatal("rejected upload must not replace the stored bundle")
	}
}

func TestRewriteSkillManifests_PreservesArchiveComment(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := zw.SetComment("bundle comment"); err != nil {
		t.Fatal(err)
	}
	w, _ := zw.Create("SKILL.md")
	_, _ = w.Write([]byte(skillMDWithSecret))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := rewriteSkillManifests(buf.Bytes(), func(_ string, c []byte, top bool) ([]byte, error) { return redactSkillMD(c, top) })
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil || zr.Comment != "bundle comment" {
		t.Fatalf("comment = %q err=%v", zr.Comment, err)
	}
}

func TestSkillFileContent_AliasedFrontMatterFailsClosed(t *testing.T) {
	t.Parallel()
	raw := "---\nname: my-skill\ntools:\n  - tool_name: t\n    tool_config:\n      a: &k " + fakeSearchKey + "\n      api_key: *k\n---\nbody\n"
	fx := newRedactionFixture(t, bundle(t, raw))
	content, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "SKILL.md")
	var ve *ValidationError
	if !errors.As(err, &ve) || len(content) != 0 {
		t.Fatalf("content=%q err=%v, want a *ValidationError and no bytes", content, err)
	}
}

func TestSkillFileContent_NestedUnparseableCopyServedAsIs(t *testing.T) {
	t.Parallel()
	nested := "---\nname: [unclosed\n---\nnested body\n"
	fx := newRedactionFixture(t, buildSkillZIP(t, "", map[string]string{
		"SKILL.md":            skillMDWithSecret,
		"references/SKILL.md": nested,
	}))

	content, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "references/SKILL.md")
	if err != nil || string(content) != nested {
		t.Fatalf("nested copy: content=%q err=%v", content, err)
	}
	if _, _, err = fx.svc.SkillFileContent(t.Context(), fx.id, "SKILL.md"); err != nil {
		t.Fatalf("top-level manifest should still redact cleanly: %v", err)
	}

	rc, _, err := fx.svc.Download(t.Context(), fx.id)
	if err != nil {
		t.Fatalf("Download must not fail on an unparseable nested copy: %v", err)
	}
	downloaded, _ := io.ReadAll(rc)
	_ = rc.Close()
	entries := readEntries(t, downloaded)
	if string(entries["references/SKILL.md"]) != nested || bytes.Contains(entries["SKILL.md"], []byte(fakeSearchKey)) {
		t.Fatal("download should serve the nested copy as is and redact the top-level manifest")
	}
}

func TestSkillFileContent_TopLevelWithAnchorsFailsClosedEvenWithoutTools(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, bundle(t, "---\nname: &n my-skill\ndescription: *n\n---\nbody\n"))
	content, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "SKILL.md")
	var ve *ValidationError
	if !errors.As(err, &ve) || len(content) != 0 {
		t.Fatalf("content=%q err=%v, want a *ValidationError and no bytes", content, err)
	}
}

func TestSkillFileContent_TopLevelInSingleDirectoryFailsClosed(t *testing.T) {
	t.Parallel()
	raw := "---\nname: [unclosed\n---\nbody\n"
	fx := newRedactionFixture(t, buildSkillZIP(t, "my-skill/", map[string]string{"SKILL.md": raw}))
	if _, _, err := fx.svc.SkillFileContent(t.Context(), fx.id, "SKILL.md"); err == nil {
		t.Fatal("a top-level manifest inside the single top-level directory must fail closed")
	}
}

const sentinelSkillName = "sentinel-skill"

const sentinelSkillMD = "---\nname: " + sentinelSkillName + "\ndescription: d\ntools:\n  - tool_type: builtin\n    tool_name: web_search_google\n    tool_config:\n      google_search_api_key: \"<REDACTED>\"\n---\nbody\n"

const cleanSentinelSkillMD = "---\nname: " + sentinelSkillName + "\ndescription: d\n---\nbody\n"

func skillRowExists(t *testing.T, env *testSkillEnv, name string) bool {
	t.Helper()
	skill, err := env.repo.GetByName(t.Context(), name)
	return err == nil && skill != nil
}

func TestSkillCreateWithBundle_SentinelIsRejectedAndRolledBack(t *testing.T) {
	t.Parallel()
	env := setupSkillEnv(t)

	_, err := env.svc.CreateWithBundle(t.Context(), "d", buildSkillZIPWithContent(t, sentinelSkillMD), "u")
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %T %v, want *ValidationError", err, err)
	}
	const wantPath = "tools[web_search_google].tool_config.google_search_api_key"
	if _, ok := ve.Details[wantPath]; !ok {
		t.Fatalf("details = %v, want key %q", ve.Details, wantPath)
	}
	if skillRowExists(t, env, sentinelSkillName) {
		t.Fatal("the skill row must be rolled back")
	}
	if rc, getErr := env.svc.resourceStore.Get(t.Context(), skillRuntimeKey(env.svc.namespace, sentinelSkillName)); getErr == nil {
		_ = rc.Close()
		t.Fatal("nothing may be stored for a rejected bundle")
	}

	created, err := env.svc.CreateWithBundle(t.Context(), "d", buildSkillZIPWithContent(t, cleanSentinelSkillMD), "u")
	if err != nil || created.Name != sentinelSkillName {
		t.Fatalf("a retry with a clean bundle must succeed: %v", err)
	}
}

func TestSkillUploadZIP_StructuralChecksRunBeforeRestore(t *testing.T) {
	t.Parallel()
	fx := newRedactionFixture(t, nil)
	files := map[string]string{"SKILL.md": strings.Replace(skillMDWithSecret, fakeSearchKey, skillsecret.RedactedSentinel, 1)}
	for i := 0; i <= maxZipEntries; i++ {
		files[fmt.Sprintf("d%d/SKILL.md", i)] = "x"
	}

	err := fx.svc.UploadZIP(t.Context(), fx.id, redactionSkillName, buildSkillZIP(t, "", files), "u")
	var ve *ValidationError
	if !errors.As(err, &ve) || !strings.Contains(ve.Message, "too many entries") {
		t.Fatalf("error = %v, want the structural 'too many entries' rejection before any restore", err)
	}
	if fx.storedZIP() != nil {
		t.Fatal("nothing may be stored")
	}
}

func storedMethodZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSkillUploadZIP_CorruptIncomingEntryIsAValidationError(t *testing.T) {
	t.Parallel()
	for name, corrupt := range map[string]string{"root": "ROOTBODYMARKER", "nested": "NESTEDBODYMARKER"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := newRedactionFixture(t, nil)
			raw := storedMethodZIP(t, map[string]string{
				"SKILL.md":            cleanSentinelSkillMD[:len(cleanSentinelSkillMD)-len("body\n")] + "ROOTBODYMARKER\n",
				"references/SKILL.md": "nested\nNESTEDBODYMARKER\n",
			})
			idx := bytes.Index(raw, []byte(corrupt))
			if idx < 0 {
				t.Fatal("marker not found")
			}
			raw[idx] ^= 0x01

			err := fx.svc.UploadZIP(t.Context(), fx.id, redactionSkillName, raw, "u")
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error = %T %v, want *ValidationError (a corrupt upload is the caller's fault)", err, err)
			}
		})
	}
}
