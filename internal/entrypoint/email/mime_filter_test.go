// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"net/http"
	"testing"
)

func TestMIMEFilter_EmptyAllowsEverything(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{}
	cases := []struct{ declared, detected string }{
		{"text/plain", "text/plain"},
		{"application/pdf", "application/pdf"},
		{"application/x-msdownload", "application/x-msdownload"},
		{"", ""},
	}
	for _, tc := range cases {
		ok, reason := f.Decision(tc.declared, tc.detected)
		if !ok {
			t.Errorf("(%q,%q) → allowed=false reason=%q, want allowed", tc.declared, tc.detected, reason)
		}
	}
}

func TestMIMEFilter_NilReceiverAllows(t *testing.T) {
	t.Parallel()
	var f *MIMEFilter
	ok, _ := f.Decision("application/x-msdownload", "application/x-msdownload")
	if !ok {
		t.Error("nil filter should allow everything")
	}
}

func TestMIMEFilter_DenyOnly(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{Deny: []string{"application/x-msdownload", "application/x-executable"}}
	if ok, _ := f.Decision("text/plain", "text/plain"); !ok {
		t.Error("text/plain should pass deny-only filter")
	}
	if ok, reason := f.Decision("application/x-msdownload", "application/x-msdownload"); ok || reason != "mime_denied" {
		t.Errorf("denied type should be rejected; ok=%v reason=%q", ok, reason)
	}
}

func TestMIMEFilter_AllowOnlyRequiresPresence(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{Allow: []string{"text/plain", "application/pdf"}}
	if ok, _ := f.Decision("text/plain", "text/plain"); !ok {
		t.Error("listed allow type should pass")
	}
	if ok, reason := f.Decision("application/zip", "application/zip"); ok || reason != "mime_not_allowed" {
		t.Errorf("unlisted type should be rejected; ok=%v reason=%q", ok, reason)
	}
}

func TestMIMEFilter_DenyTakesPrecedenceOverAllow(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{
		Allow: []string{"application/pdf"},
		Deny:  []string{"application/pdf"},
	}
	ok, reason := f.Decision("application/pdf", "application/pdf")
	if ok || reason != "mime_denied" {
		t.Errorf("deny should win over allow; ok=%v reason=%q", ok, reason)
	}
}

func TestMIMEFilter_WildcardAllow(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{Allow: []string{"image/*"}}
	for _, tc := range []struct {
		declared, detected string
		want               bool
	}{
		{"image/png", "image/png", true},
		{"image/jpeg", "image/jpeg", true},
		{"application/pdf", "application/pdf", false},
	} {
		got, _ := f.Decision(tc.declared, tc.detected)
		if got != tc.want {
			t.Errorf("(%q,%q) → %v, want %v", tc.declared, tc.detected, got, tc.want)
		}
	}
}

func TestMIMEFilter_WildcardDeny(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{Deny: []string{"application/*"}}
	if ok, reason := f.Decision("application/x-msdownload", "application/x-msdownload"); ok || reason != "mime_denied" {
		t.Errorf("wildcard deny should reject; ok=%v reason=%q", ok, reason)
	}
	if ok, _ := f.Decision("text/plain", "text/plain"); !ok {
		t.Error("non-matching type should not be denied by wildcard")
	}
}

func TestMIMEFilter_DisagreementDrops(t *testing.T) {
	t.Parallel()
	deny := &MIMEFilter{Deny: []string{"application/x-msdownload"}}
	if ok, reason := deny.Decision("text/plain", "application/x-msdownload"); ok || reason != "mime_denied" {
		t.Errorf("deny should fire on detected mismatch; ok=%v reason=%q", ok, reason)
	}

	allow := &MIMEFilter{Allow: []string{"text/plain"}}
	if ok, reason := allow.Decision("text/plain", "application/x-msdownload"); ok || reason != "mime_not_allowed" {
		t.Errorf("allow should require both readings to match; ok=%v reason=%q", ok, reason)
	}
}

func TestMIMEFilter_DetectedEmptyFallsBackToDeclared(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{Deny: []string{"application/x-msdownload"}}
	ok, reason := f.Decision("application/x-msdownload", "")
	if ok || reason != "mime_denied" {
		t.Errorf("declared-only deny should still fire; ok=%v reason=%q", ok, reason)
	}
}

func TestMIMEFilter_ParametersStripped(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{Allow: []string{"text/plain"}}
	if ok, _ := f.Decision("text/plain; charset=utf-8", "text/plain"); !ok {
		t.Error("parameter suffix on declared should not break allow match")
	}
}

func TestMIMEFilter_BothEmptyWithAllowlistRejects(t *testing.T) {
	t.Parallel()
	f := &MIMEFilter{Allow: []string{"text/plain"}}
	ok, reason := f.Decision("", "")
	if ok || reason != "mime_not_allowed" {
		t.Errorf("empty-on-both with allowlist should reject; ok=%v reason=%q", ok, reason)
	}
}

func TestMIMEFilter_IntersectionCatchesSpoofedZip(t *testing.T) {
	t.Parallel()
	zipBytes := []byte{'P', 'K', 0x03, 0x04, 0x14, 0x00}
	detected := http.DetectContentType(zipBytes)
	if detected != "application/zip" {
		t.Fatalf("sanity: stdlib sniffer returned %q for ZIP signature; test premise broken", detected)
	}

	declared := "text/plain"

	f := &MIMEFilter{Deny: append(DefaultMIMEDenylist(), "application/zip")}
	ok, reason := f.Decision(declared, detected)
	if ok || reason != "mime_denied" {
		t.Errorf("spoofed ZIP-as-text/plain should be denied via the intersection rule; ok=%v reason=%q", ok, reason)
	}
}

func TestMIMEFilter_LimitsOnSpoofedExe(t *testing.T) {
	t.Parallel()
	exeBytes := []byte{'M', 'Z', 0x90, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0xff, 0xff}
	detected := http.DetectContentType(exeBytes)
	if detected != "application/octet-stream" {
		t.Fatalf("stdlib sniffer behavior changed: MZ now returns %q; revisit MIME/AV hand-off doc", detected)
	}

	f := &MIMEFilter{Deny: DefaultMIMEDenylist()}
	ok, _ := f.Decision("text/plain", detected)
	if !ok {
		t.Error("expected MIME filter to ADMIT spoofed-exe-as-text/plain (limitation case); got drop")
	}
}
