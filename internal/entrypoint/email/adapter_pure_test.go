// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestMountIntent(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{}
	got := a.MountIntent()
	if got.Kind != entrypoint.MountNamed {
		t.Fatalf("MountIntent kind = %v, want MountNamed", got.Kind)
	}
}

func TestEmailExternalKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		n    *NormalizedEmail
		want string
	}{
		{
			name: "thread-rooted",
			n:    &NormalizedEmail{ThreadID: "<root@example.com>", MessageID: "<later@example.com>"},
			want: "<root@example.com>",
		},
		{
			name: "fallback to message id",
			n:    &NormalizedEmail{MessageID: "<solo@example.com>"},
			want: "<solo@example.com>",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := emailExternalKey(tc.n); got != tc.want {
				t.Errorf("emailExternalKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveReplyTo_NoReplyTo(t *testing.T) {
	t.Parallel()
	n := &NormalizedEmail{From: EmailAddress{Address: "alice@vendor.com"}}
	got := resolveReplyTo(t.Context(), n, InternalTrustResult{}, nil)
	if got.Address != "alice@vendor.com" {
		t.Errorf("Address = %q, want alice@vendor.com", got.Address)
	}
}

func TestResolveReplyTo_AlignedHonored(t *testing.T) {
	t.Parallel()
	n := &NormalizedEmail{
		From:        EmailAddress{Address: "alice@vendor.com"},
		ReplyTo:     &EmailAddress{Address: "support@vendor.com"},
		AuthResults: AuthVerdict{AlignedFromDomain: "vendor.com"},
	}
	got := resolveReplyTo(t.Context(), n, InternalTrustResult{}, nil)
	if got.Address != "support@vendor.com" {
		t.Errorf("Address = %q, want support@vendor.com (aligned Reply-To)", got.Address)
	}
}

func TestResolveReplyTo_CrossDomainPivotBlocked(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelInfo}))
	n := &NormalizedEmail{
		From:        EmailAddress{Address: "ceo@vendor.com"},
		ReplyTo:     &EmailAddress{Address: "attacker@evil.com"},
		AuthResults: AuthVerdict{AlignedFromDomain: "vendor.com"},
	}
	got := resolveReplyTo(t.Context(), n, InternalTrustResult{}, logger)
	if got.Address != "ceo@vendor.com" {
		t.Errorf("Address = %q, want ceo@vendor.com (mismatched Reply-To must downgrade to From)", got.Address)
	}
	if !strings.Contains(logBuf.String(), "reply_to_rejected") {
		t.Error("expected an audit log line carrying event_type=email.reply_to_rejected")
	}
}

func TestResolveReplyTo_NoAlignedDomainFallsBackToFromDomain(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		replyTo     string
		wantAddress string
	}{
		{"same-domain reply-to honored", "ops@tenant.example", "ops@tenant.example"},
		{"cross-domain reply-to dropped", "attacker@evil.example", "user@tenant.example"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			n := &NormalizedEmail{
				From:    EmailAddress{Address: "user@tenant.example"},
				ReplyTo: &EmailAddress{Address: tc.replyTo},
			}
			got := resolveReplyTo(t.Context(), n, InternalTrustResult{Trusted: true}, nil)
			if got.Address != tc.wantAddress {
				t.Errorf("Address = %q, want %q", got.Address, tc.wantAddress)
			}
		})
	}
}

func TestResolveReplyTo_EmptyReplyToAddress(t *testing.T) {
	t.Parallel()
	n := &NormalizedEmail{
		From:    EmailAddress{Address: "alice@vendor.com"},
		ReplyTo: &EmailAddress{Address: ""},
	}
	got := resolveReplyTo(t.Context(), n, InternalTrustResult{}, nil)
	if got.Address != "alice@vendor.com" {
		t.Errorf("empty Reply-To should fall back to From; got %q", got.Address)
	}
}

func TestOnAgentDiscoveredAndRemoved(t *testing.T) {
	t.Parallel()
	t.Run("with logger", func(t *testing.T) {
		t.Parallel()
		a := &EmailAdapter{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
		a.OnAgentDiscovered(&a2a.AgentCard{Name: "TestAgent"})
		a.OnAgentRemoved("TestAgent")
	})
	t.Run("nil logger", func(t *testing.T) {
		t.Parallel()
		a := &EmailAdapter{}
		a.OnAgentDiscovered(&a2a.AgentCard{Name: "TestAgent"})
		a.OnAgentRemoved("TestAgent")
	})
}

func TestOnArtifactUpdate_UnknownTaskNoOp(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 3 * 1024 * 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	a.OnArtifactUpdate(t.Context(), "any-task", &a2a.TaskArtifactUpdateEvent{}, nil)
}

func TestOnArtifactUpdate_CapturesFilePart(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 3 * 1024 * 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := &EmailTaskMeta{}
	a.taskMeta.Store("t1", meta)

	a.OnArtifactUpdate(t.Context(), "t1", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{
			ArtifactID: "a1",
			Name:       "report.xlsx",
			Parts: []a2a.Part{{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     "report.xlsx",
					MIMEType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
					URI:      "artifact://app/u1/s1/report.xlsx?version=2",
				},
			}},
		},
	}, nil)

	if got := len(meta.artifacts); got != 1 {
		t.Fatalf("captured count = %d; want 1", got)
	}
	c := meta.artifacts[0]
	if c.Filename != "report.xlsx" || c.Version != 2 || c.URI == "" {
		t.Errorf("captured = %+v; want filename/version/URI populated", c)
	}
}

func TestOnArtifactUpdate_DedupsByFilenameVersion(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 3 * 1024 * 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := &EmailTaskMeta{}
	a.taskMeta.Store("t1", meta)

	ev := &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{
			ArtifactID: "a1",
			Parts: []a2a.Part{{
				Kind: "file",
				File: &a2a.FileContent{Name: "x.bin", URI: "artifact://app/u/s/x.bin?version=1"},
			}},
		},
	}
	a.OnArtifactUpdate(t.Context(), "t1", ev, nil)
	a.OnArtifactUpdate(t.Context(), "t1", ev, nil)
	if got := len(meta.artifacts); got != 1 {
		t.Errorf("captured count = %d; want 1 (dedup)", got)
	}
}

func TestOnArtifactUpdate_DropsNoURI(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 3 * 1024 * 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := &EmailTaskMeta{}
	a.taskMeta.Store("t1", meta)

	a.OnArtifactUpdate(t.Context(), "t1", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{
			Parts: []a2a.Part{{
				Kind: "file",
				File: &a2a.FileContent{Name: "x.bin", Bytes: "AAAA"},
			}},
		},
	}, nil)
	if got := len(meta.artifacts); got != 0 {
		t.Errorf("captured count = %d; want 0 (no URI)", got)
	}
}

func TestOnArtifactUpdate_SkipsWorkingTag(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 3 * 1024 * 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := &EmailTaskMeta{}
	a.taskMeta.Store("t1", meta)

	a.OnArtifactUpdate(t.Context(), "t1", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{
			ArtifactID: "a1",
			Name:       "scratch.bin",
			Tags:       []string{a2a.ArtifactTagWorking},
			Parts: []a2a.Part{{
				Kind: "file",
				File: &a2a.FileContent{Name: "scratch.bin", URI: "artifact://app/u/s/scratch.bin?version=1"},
			}},
		},
	}, nil)
	if got := len(meta.artifacts); got != 0 {
		t.Errorf("captured count = %d; want 0 (__working tag must skip)", got)
	}
}

func TestOnArtifactUpdate_DisabledByConfig(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 0},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := &EmailTaskMeta{}
	a.taskMeta.Store("t1", meta)
	a.OnArtifactUpdate(t.Context(), "t1", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{Parts: []a2a.Part{{
			Kind: "file",
			File: &a2a.FileContent{Name: "x.bin", URI: "artifact://app/u/s/x.bin?version=1"},
		}}},
	}, nil)
	if got := len(meta.artifacts); got != 0 {
		t.Errorf("captured count = %d; want 0 (disabled)", got)
	}
}

func TestReadArtifactSavedSignal(t *testing.T) {
	t.Parallel()
	t.Run("map", func(t *testing.T) {
		f, m, v, _, ok := readArtifactSavedSignal(map[string]any{
			"type":      "artifact_saved",
			"filename":  "report.xlsx",
			"version":   float64(2),
			"mime_type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		})
		if !ok || f != "report.xlsx" || v != 2 || m == "" {
			t.Errorf("got (%q,%q,%d,%v); want filename+mime+version+ok", f, m, v, ok)
		}
	})
	t.Run("struct", func(t *testing.T) {
		_, _, _, _, ok := readArtifactSavedSignal(a2a.ArtifactSavedData{
			Type:     "artifact_saved",
			Filename: "x.bin",
			Version:  3,
		})
		if !ok {
			t.Error("typed struct rejected")
		}
	})
	t.Run("wrong_type", func(t *testing.T) {
		_, _, _, _, ok := readArtifactSavedSignal(map[string]any{
			"type":     "tool_result",
			"filename": "x",
		})
		if ok {
			t.Error("wrong signal type accepted")
		}
	})
	t.Run("empty_filename", func(t *testing.T) {
		_, _, _, _, ok := readArtifactSavedSignal(map[string]any{
			"type":     "artifact_saved",
			"filename": "",
		})
		if ok {
			t.Error("empty filename accepted")
		}
	})
	t.Run("tags_returned", func(t *testing.T) {
		_, _, _, tags, ok := readArtifactSavedSignal(map[string]any{
			"type":     "artifact_saved",
			"filename": "x.bin",
			"tags":     []any{"__working", "custom"},
		})
		if !ok {
			t.Fatal("tagged signal rejected")
		}
		if len(tags) != 2 || tags[0] != "__working" || tags[1] != "custom" {
			t.Errorf("tags = %v; want [__working custom]", tags)
		}
	})
}

func TestVersionFromArtifactURI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		uri  string
		want int
	}{
		{"artifact://app/u/s/file.txt?version=3", 3},
		{"artifact://app/u/s/file.txt?version=0", 0},
		{"artifact://app/u/s/file.txt", 0},
		{"artifact://app/u/s/file.txt?other=1&version=7", 7},
		{"artifact://app/u/s/file.txt?version=abc", 0},
		{"https://example.com/file.txt", 0},
		{"not-a-uri", 0},
	}
	for _, tc := range cases {
		if got := versionFromArtifactURI(tc.uri); got != tc.want {
			t.Errorf("versionFromArtifactURI(%q) = %d; want %d", tc.uri, got, tc.want)
		}
	}
}

func TestOnFinalResponse_NoTaskMeta(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.OnFinalResponse(t.Context(), "unknown-task", &a2a.Task{}, nil)
}

func TestOnFinalResponse_NoReplier(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	a := &EmailAdapter{
		logger: slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	meta := &EmailTaskMeta{
		ReplyTo:           EmailAddress{Address: "alice@vendor.com"},
		Subject:           "Re: hello",
		OriginalMessageID: "<msg@vendor.com>",
	}
	a.taskMeta.Store("task-1", meta)
	a.OnFinalResponse(t.Context(), "task-1", nil, nil)
	if !strings.Contains(logBuf.String(), "email.reply_pending") {
		t.Errorf("expected event_type=email.reply_pending in log, got: %s", logBuf.String())
	}
	if _, stillThere := a.taskMeta.Load("task-1"); stillThere {
		t.Error("OnFinalResponse must LoadAndDelete the task meta, but it survived")
	}
}

func TestOnError_NoTaskMeta(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	a.OnError(t.Context(), "unknown-task", &a2a.Error{Message: "boom"}, nil)
}

func TestOnError_NoReplier(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	a := &EmailAdapter{
		logger: slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}
	meta := &EmailTaskMeta{
		ReplyTo:           EmailAddress{Address: "alice@vendor.com"},
		Subject:           "Re: hello",
		OriginalMessageID: "<msg@vendor.com>",
	}
	a.taskMeta.Store("task-1", meta)
	a.OnError(t.Context(), "task-1", &a2a.Error{Message: "agent crashed"}, nil)
	if !strings.Contains(logBuf.String(), "email.task_error") {
		t.Errorf("expected event_type=email.task_error in log, got: %s", logBuf.String())
	}
	if strings.Contains(logBuf.String(), "Re: hello") {
		t.Errorf("subject must not be logged, got: %s", logBuf.String())
	}
}

type fakeReplier struct {
	mu                  sync.Mutex
	calls               []ReplyRequest
	sendErr             error
	supportsAttachments bool
}

func newFakeReplier() *fakeReplier {
	return &fakeReplier{supportsAttachments: true}
}

func (f *fakeReplier) Send(_ context.Context, req ReplyRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	return f.sendErr
}

func (f *fakeReplier) SupportsAttachments() bool { return f.supportsAttachments }

func TestOnFinalResponse_WithReplier_Success(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	rep := newFakeReplier()
	a := &EmailAdapter{
		logger:  slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelInfo})),
		replier: rep,
	}
	meta := &EmailTaskMeta{
		ReplyTo:            EmailAddress{Address: "alice@vendor.com"},
		Subject:            "Re: hello",
		OriginalMessageID:  "<msg@vendor.com>",
		OriginalReferences: []string{"<root@vendor.com>"},
	}
	meta.streamingBuffer.WriteString("agent answer")
	a.taskMeta.Store("task-1", meta)

	a.OnFinalResponse(t.Context(), "task-1", nil, nil)

	if len(rep.calls) != 1 {
		t.Fatalf("replier.Send called %d times, want 1", len(rep.calls))
	}
	got := rep.calls[0]
	if got.To != "alice@vendor.com" || got.Subject != "Re: hello" || got.BodyText != "agent answer" {
		t.Errorf("Send received %+v; expected To/Subject/BodyText to be threaded from meta", got)
	}
	if got.OriginalMessageID != "<msg@vendor.com>" || len(got.OriginalReferences) != 1 {
		t.Errorf("Send threading headers wrong: %+v", got)
	}
	if !strings.Contains(logBuf.String(), "email.reply_sent") {
		t.Error("expected event_type=email.reply_sent on green path")
	}
}

func TestOnFinalResponse_WithReplier_FinalTaskBody(t *testing.T) {
	t.Parallel()
	rep := newFakeReplier()
	a := &EmailAdapter{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		replier: rep,
	}
	a.taskMeta.Store("task-1", &EmailTaskMeta{ReplyTo: EmailAddress{Address: "alice@vendor.com"}})
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			Message: &a2a.Message{
				Parts: []a2a.Part{
					{Kind: "text", Text: "hello"},
					{Kind: "text", Text: " world"},
				},
			},
		},
	}

	a.OnFinalResponse(t.Context(), "task-1", task, nil)

	if len(rep.calls) != 1 || rep.calls[0].BodyText != "hello world" {
		t.Errorf("expected BodyText assembled from terminal task parts; got %+v", rep.calls)
	}
}

func TestOnFinalResponse_WithReplier_NoDoubleDispatch(t *testing.T) {
	t.Parallel()
	rep := newFakeReplier()
	a := &EmailAdapter{
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		replier: rep,
	}
	meta := &EmailTaskMeta{ReplyTo: EmailAddress{Address: "alice@vendor.com"}}
	a.taskMeta.Store("task-1", meta)

	statusEv := &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			Message: &a2a.Message{
				Parts: []a2a.Part{
					{Kind: "text", Text: "hello "},
					{Kind: "text", Text: "world"},
				},
			},
		},
	}
	a.OnStatusUpdate(t.Context(), "task-1", statusEv, nil)

	task := &a2a.Task{
		Status: a2a.TaskStatus{
			Message: &a2a.Message{
				Parts: []a2a.Part{
					{Kind: "text", Text: "hello world"},
				},
			},
		},
	}
	a.OnFinalResponse(t.Context(), "task-1", task, nil)

	if len(rep.calls) != 1 {
		t.Fatalf("expected 1 reply Send call; got %d", len(rep.calls))
	}
	if got := rep.calls[0].BodyText; got != "hello world" {
		t.Errorf("BodyText = %q; want %q (final task message must not be appended when streaming buffer is non-empty)", got, "hello world")
	}
}

func TestOnFinalResponse_WithReplier_SendError(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	rep := &fakeReplier{sendErr: errFakeSendBoom, supportsAttachments: true}
	a := &EmailAdapter{
		logger:  slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelError})),
		replier: rep,
	}
	meta := &EmailTaskMeta{ReplyTo: EmailAddress{Address: "alice@vendor.com"}}
	meta.streamingBuffer.WriteString("body")
	a.taskMeta.Store("task-1", meta)

	a.OnFinalResponse(t.Context(), "task-1", nil, nil)

	if !strings.Contains(logBuf.String(), "email.reply_failed") {
		t.Errorf("expected event_type=email.reply_failed when Send errors, got: %s", logBuf.String())
	}
}

func TestOnError_WithReplier_Success(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	rep := newFakeReplier()
	a := &EmailAdapter{
		logger:  slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelInfo})),
		replier: rep,
	}
	a.taskMeta.Store("task-1", &EmailTaskMeta{ReplyTo: EmailAddress{Address: "alice@vendor.com"}})

	a.OnError(t.Context(), "task-1", &a2a.Error{Message: "internal: panic in /some/path/file.go:42"}, nil)

	if len(rep.calls) != 1 {
		t.Fatalf("replier.Send called %d times, want 1", len(rep.calls))
	}
	if strings.Contains(rep.calls[0].BodyText, "panic") || strings.Contains(rep.calls[0].BodyText, "/some/path/file.go") {
		t.Errorf("user-facing error body must not leak internal detail; got: %q", rep.calls[0].BodyText)
	}
	if !strings.Contains(logBuf.String(), "email.error_reply_sent") {
		t.Error("expected event_type=email.error_reply_sent")
	}
}

func TestOnError_WithReplier_SendError(t *testing.T) {
	t.Parallel()
	logBuf := &strings.Builder{}
	rep := &fakeReplier{sendErr: errFakeSendBoom, supportsAttachments: true}
	a := &EmailAdapter{
		logger:  slog.New(slog.NewTextHandler(stringWriter{logBuf}, &slog.HandlerOptions{Level: slog.LevelError})),
		replier: rep,
	}
	a.taskMeta.Store("task-1", &EmailTaskMeta{ReplyTo: EmailAddress{Address: "alice@vendor.com"}})

	a.OnError(t.Context(), "task-1", &a2a.Error{Message: "boom"}, nil)

	if !strings.Contains(logBuf.String(), "email.error_reply_failed") {
		t.Errorf("expected event_type=email.error_reply_failed; got: %s", logBuf.String())
	}
}

var errFakeSendBoom = errFakeSendBoomT{}

type errFakeSendBoomT struct{}

func (errFakeSendBoomT) Error() string { return "fake replier: send failed (test)" }

type stringWriter struct{ b *strings.Builder }

func (w stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

var _ io.Writer = stringWriter{}

var _ = context.Background

func TestEmailAdapter_Init_OAuth2ModeWiresGraphReplier(t *testing.T) {
	cfg := &EmailConfig{
		Mailbox:                 "sam@example.org",
		IMAPHost:                "outlook.office365.com",
		IMAPPort:                993,
		OAuth2TenantID:          "tenant",
		OAuth2ClientID:          "client",
		OAuth2ClientSecret:      "secret",
		SMTPHost:                "smtp.office365.com",
		SMTPPort:                587,
		PollIntervalSeconds:     30,
		FetchLimit:              25,
		WorkerCount:             2,
		QueueCapacity:           100,
		PolicyMode:              ModeRequireDMARC,
		RequireDMARCForIdentity: true,
		MaxMessageSizeBytes:     1024 * 1024,
		MaxAttachmentSizeBytes:  0,
		TrustedAuthservIDs:      []string{"example.org"},
	}
	a := &EmailAdapter{cfg: cfg}
	if err := a.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, ok := a.replier.(*GraphReplySender); !ok {
		t.Errorf("a.replier = %T; want *GraphReplySender for OAuth2 mode", a.replier)
	}
}
