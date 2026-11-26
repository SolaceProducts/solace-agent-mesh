// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func loadArtifact(t *testing.T, art *artifact.ScopedService, appName, userID, sessionID, filename, mime string, content []byte) capturedArtifact {
	t.Helper()
	loc := art.Resolve(appName, userID, sessionID, filename)
	v, err := art.Inner.Save(t.Context(), loc, content, mime)
	if err != nil {
		t.Fatalf("save fixture: %v", err)
	}
	uri := "artifact://" + loc.AppName + "/" + loc.UserID + "/" + loc.SessionID + "/" + loc.Filename
	if v >= 0 {
		uri += "?version=" + strconv.Itoa(v)
	}
	return capturedArtifact{
		Filename:    filename,
		ContentType: mime,
		URI:         uri,
		Version:     v,
	}
}

func TestBuildOutboundAttachments_HappyPath(t *testing.T) {
	t.Parallel()
	art := newTestArtSvc()
	body := []byte("hello world")
	c := loadArtifact(t, art, "test-ns", "u1", "s1", "report.txt", "text/plain", body)

	got, dropped := buildOutboundAttachmentsImpl(t.Context(), []capturedArtifact{c}, art, 1024, slog.New(slog.NewTextHandler(io.Discard, nil)), "task-1")
	if dropped {
		t.Errorf("dropAny = true; want false")
	}
	if len(got) != 1 {
		t.Fatalf("got %d attachments; want 1", len(got))
	}
	if !bytes.Equal(got[0].Bytes, body) {
		t.Errorf("bytes = %q; want %q", got[0].Bytes, body)
	}
	if got[0].Filename != "report.txt" {
		t.Errorf("filename = %q; want report.txt", got[0].Filename)
	}
}

func TestBuildOutboundAttachments_SizeCap(t *testing.T) {
	t.Parallel()
	art := newTestArtSvc()
	small := loadArtifact(t, art, "ns", "u", "s", "small.bin", "application/octet-stream", bytes.Repeat([]byte("a"), 100))
	big := loadArtifact(t, art, "ns", "u", "s", "big.bin", "application/octet-stream", bytes.Repeat([]byte("b"), 200))

	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	got, dropped := buildOutboundAttachmentsImpl(t.Context(), []capturedArtifact{small, big}, art, 150, logger, "t1")
	if !dropped {
		t.Error("dropAny = false; want true after size-cap exceedance")
	}
	if len(got) != 1 || got[0].Filename != "small.bin" {
		t.Errorf("got attachments = %+v; want [small.bin]", got)
	}
	if !strings.Contains(logBuf.String(), "email.reply_attachment_dropped") || !strings.Contains(logBuf.String(), "size_cap_exceeded") {
		t.Errorf("expected size_cap_exceeded audit event, got log: %s", logBuf.String())
	}
}

func TestBuildOutboundAttachments_DisabledByCapZero(t *testing.T) {
	t.Parallel()
	art := newTestArtSvc()
	c := loadArtifact(t, art, "ns", "u", "s", "any.bin", "application/octet-stream", []byte("x"))
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	got, dropped := buildOutboundAttachmentsImpl(t.Context(), []capturedArtifact{c}, art, 0, logger, "t1")
	if !dropped {
		t.Error("dropAny = false; want true")
	}
	if len(got) != 0 {
		t.Errorf("got %d attachments; want 0 (disabled)", len(got))
	}
	if !strings.Contains(logBuf.String(), "outbound_attachments_disabled") {
		t.Errorf("expected outbound_attachments_disabled reason in audit log, got: %s", logBuf.String())
	}
}

func TestBuildOutboundAttachments_NoArtifactService(t *testing.T) {
	t.Parallel()
	c := capturedArtifact{Filename: "x.bin", URI: "artifact://ns/u/s/x.bin?version=0"}
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	got, dropped := buildOutboundAttachmentsImpl(t.Context(), []capturedArtifact{c}, nil, 1024, logger, "t1")
	if !dropped || len(got) != 0 {
		t.Errorf("got=%v dropped=%v; want empty + dropped", got, dropped)
	}
	if !strings.Contains(logBuf.String(), "no_artifact_service") {
		t.Errorf("expected no_artifact_service reason, got: %s", logBuf.String())
	}
}

func TestBuildOutboundAttachments_LoadFailure(t *testing.T) {
	t.Parallel()
	inner := &errorOnLoadStore{}
	art := &artifact.ScopedService{Inner: inner, Scope: "namespace", Namespace: "ns"}
	c := capturedArtifact{Filename: "x.bin", URI: "artifact://ns/u/s/x.bin?version=1", Version: 1}
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	got, dropped := buildOutboundAttachmentsImpl(t.Context(), []capturedArtifact{c}, art, 1024, logger, "t1")
	if !dropped {
		t.Error("dropAny = false; want true")
	}
	if len(got) != 0 {
		t.Errorf("got %d attachments after load failure; want 0 (no partial bytes leaked)", len(got))
	}
	if !strings.Contains(logBuf.String(), "artifact_load_error") {
		t.Errorf("expected artifact_load_error reason, got: %s", logBuf.String())
	}
}

func TestBuildOutboundAttachments_NotFound(t *testing.T) {
	t.Parallel()
	art := newTestArtSvc()
	c := capturedArtifact{Filename: "missing.bin", URI: "artifact://test-ns/u/s/missing.bin?version=0"}
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, dropped := buildOutboundAttachmentsImpl(t.Context(), []capturedArtifact{c}, art, 1024, logger, "t1")
	if !dropped {
		t.Error("dropAny = false; want true")
	}
	if !strings.Contains(logBuf.String(), "artifact_not_found") {
		t.Errorf("expected artifact_not_found reason, got: %s", logBuf.String())
	}
}

func TestBuildOutboundAttachments_URIParseError(t *testing.T) {
	t.Parallel()
	art := newTestArtSvc()
	c := capturedArtifact{Filename: "x.bin", URI: "not-a-uri"}
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	_, dropped := buildOutboundAttachmentsImpl(t.Context(), []capturedArtifact{c}, art, 1024, logger, "t1")
	if !dropped {
		t.Error("dropAny = false; want true")
	}
	if !strings.Contains(logBuf.String(), "uri_parse_error") {
		t.Errorf("expected uri_parse_error reason, got: %s", logBuf.String())
	}
}

func TestBuildArtifactURI_RoundTripsSpecialChars(t *testing.T) {
	t.Parallel()
	cases := []string{
		"hello.txt",
		"Financial Sample.xlsx",
		"weird?name.txt",
		"a/b.txt",
		"with#hash.txt",
		"plus+sign.txt",
		"with&amp.txt",
	}
	for _, fn := range cases {
		t.Run(fn, func(t *testing.T) {
			t.Parallel()
			loc := artifact.Locator{AppName: "default", UserID: "alice@vendor.com", SessionID: "email-thread-1", Filename: fn}
			uri := buildArtifactURI(loc, 4)
			got, ver, err := parseArtifactURI(uri)
			if err != nil {
				t.Fatalf("parse %q: %v", uri, err)
			}
			if got.AppName != loc.AppName || got.UserID != loc.UserID || got.SessionID != loc.SessionID || got.Filename != loc.Filename {
				t.Errorf("round-trip lost data:\n uri=%s\n want=%+v\n got=%+v", uri, loc, got)
			}
			if ver != 4 {
				t.Errorf("version = %d; want 4 from URI %s", ver, uri)
			}
		})
	}
}

func TestParseArtifactURI(t *testing.T) {
	t.Parallel()
	loc, v, err := parseArtifactURI("artifact://my-app/u1/s1/report.xlsx?version=4")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if loc.AppName != "my-app" || loc.UserID != "u1" || loc.SessionID != "s1" || loc.Filename != "report.xlsx" {
		t.Errorf("locator = %+v; want app=my-app user=u1 session=s1 file=report.xlsx", loc)
	}
	if v != 4 {
		t.Errorf("version = %d; want 4", v)
	}

	loc, _, err = parseArtifactURI("artifact://app/u/s/Financial%20Sample.xlsx?version=1")
	if err != nil {
		t.Fatalf("parse spaces: %v", err)
	}
	if loc.Filename != "Financial Sample.xlsx" {
		t.Errorf("filename = %q; want decoded with space", loc.Filename)
	}

	if _, _, err := parseArtifactURI("https://example.com/x"); err == nil {
		t.Error("expected error for non-artifact:// URI")
	}

	if _, _, err := parseArtifactURI("artifact://app/u"); err == nil {
		t.Error("expected error for short path")
	}
}

func TestFilterUnsupportedAttachments_DropsWhenTransportCantCarry(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	atts := []ReplyAttachment{
		{Filename: "a.txt", ContentType: "text/plain", Bytes: []byte("hi")},
		{Filename: "b.txt", ContentType: "text/plain", Bytes: []byte("yo")},
	}
	got, dropped := filterUnsupportedAttachments(t.Context(), logger, "task-1", false, atts)
	if !dropped || len(got) != 0 {
		t.Errorf("got=%v dropped=%v; want empty + dropped=true", got, dropped)
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "transport_unsupported") {
		t.Errorf("expected transport_unsupported reason in audit log; got: %s", logs)
	}
	if !strings.Contains(logs, "a.txt") || !strings.Contains(logs, "b.txt") {
		t.Errorf("expected per-attachment drop events; got: %s", logs)
	}
}

func TestFilterUnsupportedAttachments_PassThroughWhenSupported(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	atts := []ReplyAttachment{{Filename: "a.txt", Bytes: []byte("hi")}}
	got, dropped := filterUnsupportedAttachments(t.Context(), logger, "task-1", true, atts)
	if dropped {
		t.Error("dropped=true; want false when transport supports attachments")
	}
	if len(got) != 1 || got[0].Filename != "a.txt" {
		t.Errorf("got=%+v; want pass-through", got)
	}
	if strings.Contains(logBuf.String(), "transport_unsupported") {
		t.Errorf("unexpected drop event when supported; logs: %s", logBuf.String())
	}
}

func TestFilterUnsupportedAttachments_EmptyInputNoDrop(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, supports := range []bool{true, false} {
		got, dropped := filterUnsupportedAttachments(t.Context(), logger, "task-1", supports, nil)
		if dropped || len(got) != 0 {
			t.Errorf("supports=%v: got=%v dropped=%v; want empty + dropped=false", supports, got, dropped)
		}
	}
}

type errorOnLoadStore struct {
	memory.Store
}

func (e *errorOnLoadStore) Load(_ context.Context, _ artifact.Locator, _ *int) ([]byte, string, error) {
	return nil, "", errors.New("simulated backend outage")
}
