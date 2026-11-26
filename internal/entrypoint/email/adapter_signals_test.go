// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newAdapterWithArtifactService(t *testing.T) (*EmailAdapter, *artifact.ScopedService) {
	t.Helper()
	artSvc := newTestArtSvc()
	rt := runtime.NewRuntime(nil, nil, runtime.WithArtifacts(artSvc))
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 3 * 1024 * 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	core := entrypoint.NewGatewayCore(a, map[string]any{})
	core.SetServices(rt.Services())
	a.core = core
	return a, artSvc
}

func TestEmailAdapter_OnStatusUpdate_CapturesArtifactSavedSignal(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterWithArtifactService(t)

	taskID := "task-1"
	_ = a.core.Tasks().Register(taskID, "Manager", "alice@vendor.com", entrypoint.TaskIdentity{})
	meta := &EmailTaskMeta{UserID: "alice@vendor.com", SessionID: "email-thread-1"}
	a.taskMeta.Store(taskID, meta)

	a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{
			Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":      string(a2a.SignalArtifactSaved),
				"filename":  "report.xlsx",
				"version":   float64(2),
				"mime_type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
			})},
		}},
	}, nil)

	if got := len(meta.artifacts); got != 1 {
		t.Fatalf("captured count = %d; want 1", got)
	}
	c := meta.artifacts[0]
	if c.Filename != "report.xlsx" {
		t.Errorf("captured filename = %q; want report.xlsx", c.Filename)
	}
	if c.Version != 2 {
		t.Errorf("captured version = %d; want 2", c.Version)
	}
	if !strings.HasPrefix(c.URI, "artifact://") {
		t.Errorf("captured URI = %q; want artifact:// prefix", c.URI)
	}
	if !strings.Contains(c.URI, "report.xlsx") {
		t.Errorf("captured URI = %q; want filename embedded", c.URI)
	}
}

func TestEmailAdapter_OnStatusUpdate_ArtifactSavedDisabledByCfg(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterWithArtifactService(t)
	a.cfg.MaxOutboundAttachmentSizeBytes = 0

	taskID := "task-1"
	_ = a.core.Tasks().Register(taskID, "Manager", "alice@vendor.com", entrypoint.TaskIdentity{})
	meta := &EmailTaskMeta{}
	a.taskMeta.Store(taskID, meta)

	a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{
			Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":     string(a2a.SignalArtifactSaved),
				"filename": "x.bin",
				"version":  float64(1),
			})},
		}},
	}, nil)

	if got := len(meta.artifacts); got != 0 {
		t.Errorf("captured count = %d; want 0 when disabled", got)
	}
}

func TestEmailAdapter_OnStatusUpdate_ArtifactSavedSkipsWorkingTag(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterWithArtifactService(t)

	taskID := "task-1"
	_ = a.core.Tasks().Register(taskID, "Manager", "alice@vendor.com", entrypoint.TaskIdentity{})
	meta := &EmailTaskMeta{}
	a.taskMeta.Store(taskID, meta)

	a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{
			Parts: []a2a.Part{a2a.NewDataPart(map[string]any{
				"type":     string(a2a.SignalArtifactSaved),
				"filename": "scratch.bin",
				"version":  float64(1),
				"tags":     []any{a2a.ArtifactTagWorking},
			})},
		}},
	}, nil)

	if got := len(meta.artifacts); got != 0 {
		t.Errorf("captured count = %d; want 0 (__working tag must skip)", got)
	}
}

func TestEmailAdapter_OnStatusUpdate_ArtifactSavedDeduplicates(t *testing.T) {
	t.Parallel()
	a, _ := newAdapterWithArtifactService(t)

	taskID := "task-1"
	_ = a.core.Tasks().Register(taskID, "Manager", "alice@vendor.com", entrypoint.TaskIdentity{})
	meta := &EmailTaskMeta{}
	a.taskMeta.Store(taskID, meta)

	signal := a2a.NewDataPart(map[string]any{
		"type":     string(a2a.SignalArtifactSaved),
		"filename": "report.xlsx",
		"version":  float64(1),
	})
	for range 2 {
		a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
			Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{signal}}},
		}, nil)
	}

	if got := len(meta.artifacts); got != 1 {
		t.Errorf("captured count = %d; want 1 after duplicate signal", got)
	}
}

func TestEmailAdapter_OnFinalResponse_AttachmentsSent(t *testing.T) {
	t.Parallel()
	a, art := newAdapterWithArtifactService(t)

	rep := newFakeReplier()
	a.replier = rep

	body := []byte("spreadsheet bytes")
	c := loadArtifact(t, art, "test-ns", "alice@vendor.com", "email-thread-1", "report.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", body)

	meta := &EmailTaskMeta{
		ReplyTo:   EmailAddress{Address: "alice@vendor.com"},
		Subject:   "Re: report",
		UserID:    "alice@vendor.com",
		SessionID: "email-thread-1",
		artifacts: []capturedArtifact{c},
	}
	meta.streamingBuffer.WriteString("here is your report")
	a.taskMeta.Store("task-1", meta)

	a.OnFinalResponse(t.Context(), "task-1", nil, nil)

	if len(rep.calls) != 1 {
		t.Fatalf("replier.Send called %d times; want 1", len(rep.calls))
	}
	if got := len(rep.calls[0].Attachments); got != 1 {
		t.Fatalf("attachments len = %d; want 1", got)
	}
	att := rep.calls[0].Attachments[0]
	if att.Filename != "report.xlsx" {
		t.Errorf("attachment filename = %q; want report.xlsx", att.Filename)
	}
	if string(att.Bytes) != string(body) {
		t.Errorf("attachment bytes = %q; want %q", att.Bytes, body)
	}
	if strings.Contains(rep.calls[0].BodyText, "could not be included") {
		t.Errorf("body unexpectedly carries dropped-attachment footer: %q", rep.calls[0].BodyText)
	}
}

func TestEmailAdapter_OnFinalResponse_DroppedAttachmentsAddFooter(t *testing.T) {
	t.Parallel()
	a, art := newAdapterWithArtifactService(t)

	rep := &fakeReplier{supportsAttachments: false}
	a.replier = rep

	c := loadArtifact(t, art, "test-ns", "alice@vendor.com", "email-thread-1", "report.xlsx",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", []byte("xxx"))

	meta := &EmailTaskMeta{
		ReplyTo:   EmailAddress{Address: "alice@vendor.com"},
		Subject:   "Re: report",
		UserID:    "alice@vendor.com",
		SessionID: "email-thread-1",
		artifacts: []capturedArtifact{c},
	}
	meta.streamingBuffer.WriteString("body")
	a.taskMeta.Store("task-1", meta)

	a.OnFinalResponse(t.Context(), "task-1", nil, nil)

	if len(rep.calls) != 1 {
		t.Fatalf("replier.Send called %d times; want 1", len(rep.calls))
	}
	got := rep.calls[0]
	if len(got.Attachments) != 0 {
		t.Errorf("attachments len = %d; want 0 (transport-dropped)", len(got.Attachments))
	}
	if !strings.Contains(got.BodyText, "could not be included") {
		t.Errorf("body missing dropped-attachment footer; got %q", got.BodyText)
	}
}

func TestEmailAdapter_OnArtifactUpdate_NilEvent(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	a.OnArtifactUpdate(t.Context(), "task-1", nil, nil)
}

func TestEmailAdapter_OnArtifactUpdate_NonFilePartSkipped(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := &EmailTaskMeta{}
	a.taskMeta.Store("t1", meta)

	a.OnArtifactUpdate(t.Context(), "t1", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{
			Parts: []a2a.Part{
				a2a.NewDataPart(map[string]any{"k": "v"}),
				{Kind: "file", File: nil},
			},
		},
	}, nil)

	if got := len(meta.artifacts); got != 0 {
		t.Errorf("captured count = %d; want 0 (non-file parts must skip)", got)
	}
}

func TestEmailAdapter_OnArtifactUpdate_FilenameFallsBackToArtifactName(t *testing.T) {
	t.Parallel()
	a := &EmailAdapter{
		cfg:    &EmailConfig{MaxOutboundAttachmentSizeBytes: 1024},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := &EmailTaskMeta{}
	a.taskMeta.Store("t1", meta)

	a.OnArtifactUpdate(t.Context(), "t1", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{
			Name: "fallback.txt",
			Parts: []a2a.Part{{
				Kind: "file",
				File: &a2a.FileContent{
					URI: "artifact://app/u/s/fallback.txt?version=1",
				},
			}},
		},
	}, nil)

	if got := len(meta.artifacts); got != 1 {
		t.Fatalf("captured count = %d; want 1", got)
	}
	if meta.artifacts[0].Filename != "fallback.txt" {
		t.Errorf("filename = %q; want fallback.txt (from Artifact.Name)", meta.artifacts[0].Filename)
	}
}
