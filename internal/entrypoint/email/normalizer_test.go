// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalize_BasicText(t *testing.T) {
	t.Parallel()
	raw := mustReadFixture(t, "synthetic/basic_text.eml")
	got, err := Normalize(raw, []string{"protection.outlook.com"})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if got.From.Address != "alice@vendor.com" {
		t.Errorf("From = %q", got.From.Address)
	}
	if got.From.DisplayName != "Alice Smith" {
		t.Errorf("DisplayName = %q", got.From.DisplayName)
	}
	if got.Subject != "Order request" {
		t.Errorf("Subject = %q", got.Subject)
	}
	if !strings.Contains(got.BodyText, "50 widgets") {
		t.Errorf("BodyText missing expected content: %q", got.BodyText)
	}
	if got.MessageID != "<basic-text-001@vendor.com>" {
		t.Errorf("MessageID = %q", got.MessageID)
	}
	if got.AuthResults.DMARC != AuthPass {
		t.Errorf("AuthResults.DMARC = %q, want pass", got.AuthResults.DMARC)
	}
}

func TestNormalize_FromAddressLowercased(t *testing.T) {
	t.Parallel()
	raw := []byte("From: Alice@VENDOR.COM\r\nSubject: x\r\nMessage-ID: <a@b>\r\n\r\nbody\r\n")
	got, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.From.Address != "alice@vendor.com" {
		t.Errorf("From = %q, want canonical lowercase", got.From.Address)
	}
}

func TestNormalize_MessageIDSynthesizedWhenMissing(t *testing.T) {
	t.Parallel()
	raw := mustReadFixture(t, "synthetic/no_message_id.eml")
	got, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.MessageID == "" {
		t.Error("MessageID should be synthesized, not empty")
	}
	if !strings.HasPrefix(got.MessageID, "<") || !strings.HasSuffix(got.MessageID, ">") {
		t.Errorf("synthesized MessageID lacks angle brackets: %q", got.MessageID)
	}
	if !strings.Contains(got.MessageID, "@") {
		t.Errorf("synthesized MessageID lacks '@': %q", got.MessageID)
	}
}

func TestNormalize_ThreadIDDerivesFromReferences(t *testing.T) {
	t.Parallel()
	raw := []byte("From: a@b\r\nSubject: re\r\nMessage-ID: <reply@b>\r\nReferences: <orig@b>\r\nIn-Reply-To: <orig@b>\r\n\r\nbody\r\n")
	got, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "<orig@b>" {
		t.Errorf("ThreadID = %q, want References[0]", got.ThreadID)
	}
}

func TestNormalize_ThreadIDFallsBackToInReplyTo(t *testing.T) {
	t.Parallel()
	raw := []byte("From: a@b\r\nSubject: re\r\nMessage-ID: <reply@b>\r\nIn-Reply-To: <orig@b>\r\n\r\nbody\r\n")
	got, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "<orig@b>" {
		t.Errorf("ThreadID = %q, want In-Reply-To fallback <orig@b>", got.ThreadID)
	}
}

func TestNormalize_ThreadIDFallsBackToMessageID(t *testing.T) {
	t.Parallel()
	raw := []byte("From: a@b\r\nSubject: hello\r\nMessage-ID: <root@b>\r\n\r\nbody\r\n")
	got, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ThreadID != "<root@b>" {
		t.Errorf("ThreadID = %q, want MessageID <root@b>", got.ThreadID)
	}
}

func TestNormalize_AttachmentSurfacedWithDetectedMIME(t *testing.T) {
	t.Parallel()
	raw := mustReadFixture(t, "synthetic/with_pdf_attachment.eml")
	got, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("Attachments len = %d, want 1", len(got.Attachments))
	}
	a := got.Attachments[0]
	if a.Filename == "" {
		t.Error("Attachment.Filename empty")
	}
	if a.DeclaredMIMEType != "application/pdf" {
		t.Errorf("DeclaredMIMEType = %q", a.DeclaredMIMEType)
	}
	if a.DetectedMIMEType == "" {
		t.Error("DetectedMIMEType should be populated by content sniff")
	}
	if len(a.Content.InlineBytes) == 0 {
		t.Error("Content.InlineBytes empty")
	}
	if a.Content.ArtifactURI != "" {
		t.Error("ArtifactURI should be empty in POC")
	}
	if a.SizeBytes != int64(len(a.Content.InlineBytes)) {
		t.Errorf("SizeBytes = %d, expected to match InlineBytes len %d", a.SizeBytes, len(a.Content.InlineBytes))
	}
}

func TestNormalize_InlineImagesNotInAttachments(t *testing.T) {
	raw := []byte(`From: a@b
Content-Type: multipart/related; boundary=BOUNDARY

--BOUNDARY
Content-Type: text/html

<p>see below</p>

--BOUNDARY
Content-Type: image/png
Content-ID: <embed1>
Content-Disposition: inline

iVBORw0KGgo=
--BOUNDARY--
`)
	raw = bytes.ReplaceAll(raw, []byte("\n"), []byte("\r\n"))
	got, err := Normalize(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Attachments) != 0 {
		t.Errorf("inline image leaked into Attachments: %+v", got.Attachments)
	}
}

func mustReadFixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "eml", rel))
	if err != nil {
		t.Fatalf("read fixture %s: %v", rel, err)
	}
	return b
}
