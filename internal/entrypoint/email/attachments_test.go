// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func newTestArtSvc() *artifact.ScopedService {
	return &artifact.ScopedService{
		Inner:     memory.New(),
		Scope:     "namespace",
		Namespace: "test-ns",
	}
}

func TestAttachmentsToFileParts_InlineBytesUploadedAndReferenced(t *testing.T) {
	t.Parallel()
	artSvc := newTestArtSvc()
	body := []byte("# notes\n\nhello world\n")

	atts := []Attachment{{
		Filename:         "notes.md",
		DeclaredMIMEType: "text/plain",
		DetectedMIMEType: "text/markdown; charset=utf-8",
		SizeBytes:        int64(len(body)),
		Content:          AttachmentContent{InlineBytes: body},
	}}

	parts, ok := attachmentsToFileParts(t.Context(), atts, artSvc, "judy", "email-thread-1", slog.Default())
	if !ok {
		t.Fatalf("attachmentsToFileParts returned ok=false")
	}

	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	p := parts[0]
	if p.Kind != "file" || p.File == nil {
		t.Fatalf("part kind = %q, file = %v; want file part", p.Kind, p.File)
	}
	if p.File.Name != "notes.md" {
		t.Errorf("filename = %q, want notes.md", p.File.Name)
	}
	if p.File.MIMEType != "text/markdown; charset=utf-8" {
		t.Errorf("mime type = %q, want detected charset variant", p.File.MIMEType)
	}
	if !strings.HasPrefix(p.File.URI, "artifact://") {
		t.Errorf("uri = %q, want artifact:// prefix", p.File.URI)
	}
	if !strings.Contains(p.File.URI, "notes.md") {
		t.Errorf("uri = %q, want filename embedded", p.File.URI)
	}
	if p.File.Bytes != "" {
		t.Errorf("bytes should be empty (URI-based reference), got %q", p.File.Bytes)
	}

	loc := artSvc.Resolve("", "judy", "email-thread-1", "notes.md")
	got, _, err := artSvc.Inner.Load(t.Context(), loc, nil)
	if err != nil {
		t.Fatalf("load saved artifact: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("artifact bytes mismatch:\n got = %q\nwant = %q", got, body)
	}
}

func TestAttachmentsToFileParts_PreservesExistingArtifactURI(t *testing.T) {
	t.Parallel()
	artSvc := newTestArtSvc()
	atts := []Attachment{{
		Filename:         "report.pdf",
		DeclaredMIMEType: "application/pdf",
		Content:          AttachmentContent{ArtifactURI: "artifact://app/u/sess/report.pdf?version=3"},
	}}

	parts, ok := attachmentsToFileParts(t.Context(), atts, artSvc, "u", "sess", slog.Default())
	if !ok {
		t.Fatalf("attachmentsToFileParts returned ok=false")
	}

	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	if got := parts[0].File.URI; got != "artifact://app/u/sess/report.pdf?version=3" {
		t.Errorf("uri = %q, want pre-existing URI forwarded unchanged", got)
	}
	if parts[0].File.Bytes != "" {
		t.Errorf("bytes should be empty, got %q", parts[0].File.Bytes)
	}
}

func TestAttachmentsToFileParts_NilArtifactServiceDropsInlineButKeepsURI(t *testing.T) {
	t.Parallel()
	atts := []Attachment{
		{Filename: "lost.txt", Content: AttachmentContent{InlineBytes: []byte("x")}},
		{Filename: "kept.pdf", Content: AttachmentContent{ArtifactURI: "artifact://a/b/c/kept.pdf?version=1"}},
	}

	var logBuf bytes.Buffer
	parts, ok := attachmentsToFileParts(t.Context(), atts, nil, "u", "s", slog.New(slog.NewTextHandler(&logBuf, nil)))
	if !ok {
		t.Fatalf("attachmentsToFileParts returned ok=false")
	}
	if strings.Contains(logBuf.String(), "lost.txt") || !strings.Contains(logBuf.String(), "extension=.txt") {
		t.Errorf("drop log should carry the extension, not the file name: %s", logBuf.String())
	}

	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1 (inline dropped, URI kept)", len(parts))
	}
	if parts[0].File.Name != "kept.pdf" {
		t.Errorf("kept the wrong attachment: %q", parts[0].File.Name)
	}
}

func TestAttachmentsToFileParts_FallsBackToDeclaredMIMEWhenDetectedEmpty(t *testing.T) {
	t.Parallel()
	artSvc := newTestArtSvc()
	atts := []Attachment{{
		Filename:         "data.bin",
		DeclaredMIMEType: "application/octet-stream",
		Content:          AttachmentContent{InlineBytes: []byte{0x00, 0x01, 0x02}},
	}}

	parts, ok := attachmentsToFileParts(t.Context(), atts, artSvc, "u", "s", slog.Default())
	if !ok {
		t.Fatalf("attachmentsToFileParts returned ok=false")
	}

	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1", len(parts))
	}
	if parts[0].File.MIMEType != "application/octet-stream" {
		t.Errorf("mime type = %q, want declared fallback", parts[0].File.MIMEType)
	}
}
