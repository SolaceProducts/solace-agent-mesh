// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func newInlineFilePart(name, mime string, content []byte) a2a.Part {
	return a2a.NewFilePartWithBytes(base64.StdEncoding.EncodeToString(content), name, mime)
}

func newTestScopedService(inner artifact.Service) *artifact.ScopedService {
	return &artifact.ScopedService{Inner: inner, Scope: "namespace", Namespace: "test-ns"}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestConvertInlineFileParts_SavesAndRewrites(t *testing.T) {
	svc := newTestScopedService(memory.New())
	msg := &a2a.Message{Parts: []a2a.Part{
		a2a.NewTextPart("hello"),
		newInlineFilePart("report.pdf", "application/pdf", []byte("PDFDATA")),
	}}

	dropped := ConvertInlineFilePartsToArtifacts(t.Context(), msg, svc, ArtifactScope{UserID: "user1", SessionID: "sess1"}, discardLogger())

	if len(dropped) != 0 {
		t.Fatalf("expected no dropped files, got %v", dropped)
	}
	if len(msg.Parts) != 2 {
		t.Fatalf("expected 2 parts, got %d", len(msg.Parts))
	}
	fc := msg.Parts[1].File
	if fc.Bytes != "" {
		t.Errorf("expected Bytes cleared, got %q", fc.Bytes)
	}
	if fc.URI == "" {
		t.Fatal("expected URI set")
	}
	if _, _, ok := ParseArtifactURI(fc.URI); !ok {
		t.Errorf("emitted URI does not parse: %q", fc.URI)
	}
}

func TestConvertInlineFileParts_NilServiceDropsPart(t *testing.T) {
	msg := &a2a.Message{Parts: []a2a.Part{
		a2a.NewTextPart("hi"),
		newInlineFilePart("big.bin", "application/octet-stream", []byte("data")),
	}}

	dropped := ConvertInlineFilePartsToArtifacts(t.Context(), msg, nil, ArtifactScope{UserID: "user1", SessionID: "sess1"}, discardLogger())

	if len(dropped) != 1 || dropped[0] != "big.bin" {
		t.Fatalf("expected [big.bin] dropped, got %v", dropped)
	}
	if len(msg.Parts) != 1 || msg.Parts[0].Kind != "text" {
		t.Fatalf("expected only the text part to remain, got %d parts", len(msg.Parts))
	}
}

func TestConvertInlineFileParts_UndecodableBytesDropped(t *testing.T) {
	svc := newTestScopedService(memory.New())
	msg := &a2a.Message{Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{Name: "x.bin", Bytes: "!!!not base64!!!"}},
	}}

	dropped := ConvertInlineFilePartsToArtifacts(t.Context(), msg, svc, ArtifactScope{UserID: "u", SessionID: "s"}, discardLogger())

	if len(dropped) != 1 || dropped[0] != "x.bin" {
		t.Fatalf("expected [x.bin] dropped, got %v", dropped)
	}
	if len(msg.Parts) != 0 {
		t.Fatalf("expected part dropped, got %d", len(msg.Parts))
	}
}

func TestConvertInlineFileParts_ReservedFilenameDropped(t *testing.T) {
	svc := newTestScopedService(memory.New())
	msg := &a2a.Message{Parts: []a2a.Part{
		newInlineFilePart("report.metadata.json", "application/json", []byte("{}")),
	}}

	dropped := ConvertInlineFilePartsToArtifacts(t.Context(), msg, svc, ArtifactScope{UserID: "u", SessionID: "s"}, discardLogger())

	if len(dropped) != 1 {
		t.Fatalf("expected reserved filename dropped, got %v", dropped)
	}
	if len(msg.Parts) != 0 {
		t.Fatalf("expected part dropped, got %d", len(msg.Parts))
	}
}

func TestConvertInlineFileParts_UriPartUnchanged(t *testing.T) {
	svc := newTestScopedService(memory.New())
	existing := a2a.NewFilePart("artifact://test-ns/u/s/keep.txt?version=1", "keep.txt", "text/plain")
	msg := &a2a.Message{Parts: []a2a.Part{existing}}

	dropped := ConvertInlineFilePartsToArtifacts(t.Context(), msg, svc, ArtifactScope{UserID: "u", SessionID: "s"}, discardLogger())

	if len(dropped) != 0 {
		t.Fatalf("expected nothing dropped, got %v", dropped)
	}
	if len(msg.Parts) != 1 || msg.Parts[0].File.URI != existing.File.URI {
		t.Fatalf("expected URI part untouched")
	}
}

func TestConvertInlineFileParts_MimeReDerivedFromFilename(t *testing.T) {
	inner := memory.New()
	svc := newTestScopedService(inner)
	msg := &a2a.Message{Parts: []a2a.Part{
		newInlineFilePart("config.yaml", "text/plain", []byte("a: 1\nb: 2\n")),
	}}

	ConvertInlineFilePartsToArtifacts(t.Context(), msg, svc, ArtifactScope{UserID: "u", SessionID: "s"}, discardLogger())

	loc := svc.Resolve("", "u", "s", "config.yaml")
	_, mime, err := inner.Load(t.Context(), loc, nil)
	if err != nil {
		t.Fatalf("load saved artifact: %v", err)
	}
	if mime == "text/plain" {
		t.Errorf("expected MIME re-derived from .yaml, still got text/plain")
	}
}

type failingStore struct {
	artifact.Service
	failName string
}

func (f *failingStore) Save(ctx context.Context, loc artifact.Locator, content []byte, mime string) (int, error) {
	if loc.Filename == f.failName {
		return 0, errors.New("disk full")
	}
	return f.Service.Save(ctx, loc, content, mime)
}

func TestSaveBytesAsArtifact_DeriveMIME(t *testing.T) {
	inner := memory.New()
	svc := newTestScopedService(inner)

	uri, ok := SaveBytesAsArtifact(t.Context(), svc, ArtifactScope{UserID: "u", SessionID: "s"},
		ArtifactUpload{Filename: "config.yaml", MIMEType: "text/plain", Content: []byte("a: 1\n"), DeriveMIME: true}, discardLogger())

	if !ok {
		t.Fatal("expected save to succeed")
	}
	if _, _, parsed := ParseArtifactURI(uri); !parsed {
		t.Errorf("URI does not parse: %q", uri)
	}
	_, mime, err := inner.Load(t.Context(), svc.Resolve("", "u", "s", "config.yaml"), nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if mime == "text/plain" {
		t.Errorf("expected MIME re-derived from .yaml, got text/plain")
	}
}

func TestSaveBytesAsArtifact_KeepsMIMEWhenNotDeriving(t *testing.T) {
	inner := memory.New()
	svc := newTestScopedService(inner)

	_, ok := SaveBytesAsArtifact(t.Context(), svc, ArtifactScope{UserID: "u", SessionID: "s"},
		ArtifactUpload{Filename: "report", MIMEType: "application/pdf", Content: []byte("data")}, discardLogger())

	if !ok {
		t.Fatal("expected save to succeed")
	}
	_, mime, _ := inner.Load(t.Context(), svc.Resolve("", "u", "s", "report"), nil)
	if mime != "application/pdf" {
		t.Errorf("MIME = %q, want application/pdf (unchanged)", mime)
	}
}

func TestSaveBytesAsArtifact_NilServiceAndReserved(t *testing.T) {
	scope := ArtifactScope{UserID: "u", SessionID: "s"}
	if _, ok := SaveBytesAsArtifact(t.Context(), nil, scope, ArtifactUpload{Filename: "f.bin", MIMEType: "application/octet-stream", Content: []byte("x")}, discardLogger()); ok {
		t.Error("expected ok=false with nil artifact service")
	}
	svc := newTestScopedService(memory.New())
	if _, ok := SaveBytesAsArtifact(t.Context(), svc, scope, ArtifactUpload{Filename: "x.metadata.json", MIMEType: "application/json", Content: []byte("{}")}, discardLogger()); ok {
		t.Error("expected ok=false for reserved internal-artifact filename")
	}
}

func TestAppendDroppedFilesNote_AddsNote(t *testing.T) {
	msg := &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("hi")}}

	AppendDroppedFilesNote(msg, []string{"report.pdf", "data.csv"})

	if len(msg.Parts) != 2 {
		t.Fatalf("expected note part appended, got %d parts", len(msg.Parts))
	}
	note := msg.Parts[1].Text
	if !strings.Contains(note, "report.pdf") || !strings.Contains(note, "data.csv") || !strings.Contains(note, "2 uploaded file") {
		t.Errorf("note missing expected content: %q", note)
	}
}

func TestAppendDroppedFilesNote_NoopWhenEmpty(t *testing.T) {
	msg := &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("hi")}}

	AppendDroppedFilesNote(msg, nil)

	if len(msg.Parts) != 1 {
		t.Fatalf("expected no note appended, got %d parts", len(msg.Parts))
	}
}

func TestConvertInlineFileParts_SaveFailureDropsOnlyThatPart(t *testing.T) {
	svc := newTestScopedService(&failingStore{Service: memory.New(), failName: "bad.bin"})
	msg := &a2a.Message{Parts: []a2a.Part{
		newInlineFilePart("good.bin", "application/octet-stream", []byte("ok")),
		newInlineFilePart("bad.bin", "application/octet-stream", []byte("nope")),
	}}

	dropped := ConvertInlineFilePartsToArtifacts(t.Context(), msg, svc, ArtifactScope{UserID: "u", SessionID: "s"}, discardLogger())

	if len(dropped) != 1 || dropped[0] != "bad.bin" {
		t.Fatalf("expected [bad.bin] dropped, got %v", dropped)
	}
	if len(msg.Parts) != 1 || msg.Parts[0].File.Name != "good.bin" || msg.Parts[0].File.URI == "" {
		t.Fatalf("expected good.bin saved and kept, got %+v", msg.Parts)
	}
}

func TestSaveBytesAsArtifact_ClientFilenameCannotEscapeSessionScope(t *testing.T) {
	const sharedPrincipal = "system:ops"
	dir := t.TempDir()
	svc := newTestScopedService(filesystem.New(dir))

	uri, ok := SaveBytesAsArtifact(t.Context(), svc,
		ArtifactScope{UserID: sharedPrincipal, SessionID: "sessionA"},
		ArtifactUpload{Filename: "user:notes.txt", Content: []byte("SENDER-A-CONFIDENTIAL")},
		discardLogger())
	if !ok {
		t.Fatal("expected the upload to be stored under a neutralized name, not dropped")
	}
	if strings.Contains(uri, "user:") {
		t.Errorf("artifact URI %q still carries the scope selector", uri)
	}

	keys, err := svc.Inner.ListKeys(t.Context(), artifact.KeyScope{
		AppName: "test-ns", UserID: sharedPrincipal, SessionID: "sessionB",
	})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("a second session under the same principal sees %d artifact(s): %v — the upload escaped session scope", len(keys), keys)
	}

	own, err := svc.Inner.ListKeys(t.Context(), artifact.KeyScope{
		AppName: "test-ns", UserID: sharedPrincipal, SessionID: "sessionA",
	})
	if err != nil {
		t.Fatalf("ListKeys own session: %v", err)
	}
	if !slices.Contains(own, "notes.txt") {
		t.Errorf("owning session artifacts = %v, want it to contain notes.txt", own)
	}
}

func TestStripScopeSelector(t *testing.T) {
	for _, tt := range []struct {
		in      string
		want    string
		wantHad bool
	}{
		{in: "notes.txt", want: "notes.txt"},
		{in: "user:notes.txt", want: "notes.txt", wantHad: true},
		{in: "user:user:notes.txt", want: "notes.txt", wantHad: true},
		{in: "users:notes.txt", want: "users:notes.txt"},
		{in: "my:notes.txt", want: "my:notes.txt"},
		{in: "USER:notes.txt", want: "USER:notes.txt"},
	} {
		got, had := stripScopeSelector(tt.in)
		if got != tt.want || had != tt.wantHad {
			t.Errorf("stripScopeSelector(%q) = (%q, %v), want (%q, %v)", tt.in, got, had, tt.want, tt.wantHad)
		}
	}
}

func TestSaveBytesAsArtifact_InfersASchemaOnlyWhereItIsCheap(t *testing.T) {
	large := maxInferredUploadBytes + 1
	jsonArray := "[" + strings.Repeat("1,", large/2) + "1]"
	csv := "id,name\n" + strings.Repeat("1,a\n", large/4)
	for name, tc := range map[string]struct {
		upload     ArtifactUpload
		wantSchema bool
	}{
		"small json":          {ArtifactUpload{Filename: "small.json", MIMEType: "application/json", Content: []byte(`{"a": 1}`)}, true},
		"large json":          {ArtifactUpload{Filename: "large.json", MIMEType: "application/json", Content: []byte(jsonArray)}, false},
		"large json, derived": {ArtifactUpload{Filename: "large.json", MIMEType: "text/plain", Content: []byte(jsonArray), DeriveMIME: true}, false},
		"large yaml, derived": {ArtifactUpload{Filename: "large.yaml", MIMEType: "application/octet-stream", Content: []byte(strings.Repeat("- 1\n", large/4+1)), DeriveMIME: true}, false},
		"large csv":           {ArtifactUpload{Filename: "large.csv", MIMEType: "text/csv", Content: []byte(csv)}, true},
	} {
		t.Run(name, func(t *testing.T) {
			inner := memory.New()
			svc := newTestScopedService(inner)
			if _, ok := SaveBytesAsArtifact(t.Context(), svc, ArtifactScope{UserID: "u", SessionID: "s"}, tc.upload, discardLogger()); !ok {
				t.Fatal("save failed")
			}
			meta, _, err := inner.Load(t.Context(), svc.Resolve("", "u", "s", tc.upload.Filename+artifact.MetadataSuffix), nil)
			if err != nil {
				t.Fatalf("load metadata: %v", err)
			}
			if got := strings.Contains(string(meta), `"schema"`); got != tc.wantSchema {
				t.Errorf("metadata has a schema = %v, want %v", got, tc.wantSchema)
			}
		})
	}
}
