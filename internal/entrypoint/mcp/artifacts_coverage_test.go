// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newArtifactAdapterNoStore(t *testing.T) *Adapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	rt := runtime.NewRuntime(brk, nil)
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{})
	core.SetServices(rt.Services())
	return &Adapter{
		core: core, cfg: mustParseConfig(t, nil),
		toolMap: map[string]toolMapping{}, agentTools: map[string][]string{},
		tasks: map[string]*taskState{}, logger: noopLogger(),
	}
}

func newTrackedTask(t *testing.T, a *Adapter, taskID, agent, user, session string) *taskState {
	t.Helper()
	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "tool", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts
	return ts
}

func TestArtifactSignal_NoArtifactService(t *testing.T) {
	a := newArtifactAdapterNoStore(t)
	ts := newTrackedTask(t, a, "task-nosvc", "Ag", "u", "s")
	a.OnStatusUpdate(t.Context(), "task-nosvc", artifactCompletedEvent("x.png", "image/png", 0), nil)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.contents) != 0 {
		t.Errorf("expected no content when artifact service is nil, got %d", len(ts.contents))
	}
}

func TestArtifactSignal_TaskNotTracked(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	ts := &taskState{toolName: "t", done: make(chan taskResult, 1), sessionID: "s"}
	a.tasks["task-untracked"] = ts
	a.OnStatusUpdate(t.Context(), "task-untracked", artifactCompletedEvent("y.png", "image/png", 0), nil)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.contents) != 0 {
		t.Errorf("expected no content when task not in registry, got %d", len(ts.contents))
	}
}

func TestArtifactSignal_EmptyArtifactSkipped(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	saveArtifact(t, scoped, "Ag", "u", "s-empty", "empty.txt", []byte{}, "text/plain")
	ts := newTrackedTask(t, a, "task-empty", "Ag", "u", "s-empty")
	a.OnStatusUpdate(t.Context(), "task-empty", artifactCompletedEvent("empty.txt", "text/plain", 0), nil)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.contents) != 0 {
		t.Errorf("expected empty artifact to be skipped, got %d content(s)", len(ts.contents))
	}
}

func TestHandleFilePart_NilAndEmpty(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	ts := &taskState{toolName: "t", done: make(chan taskResult, 1), sessionID: "s"}
	a.handleFilePart(t.Context(), "tk", ts, a2a.Part{Kind: "file"})
	a.handleFilePart(t.Context(), "tk", ts, a2a.Part{Kind: "file", File: &a2a.FileContent{Name: "n"}})
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.contents) != 0 {
		t.Errorf("expected no content for nil/empty file parts, got %d", len(ts.contents))
	}
}

func TestHandleFilePart_NonArtifactURIBecomesLink(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	ts := &taskState{toolName: "t", done: make(chan taskResult, 1), sessionID: "s"}
	a.handleFilePart(t.Context(), "tk", ts, a2a.Part{Kind: "file", File: &a2a.FileContent{
		Name: "report.html", MIMEType: "text/html", URI: "https://example.com/report.html",
	}})
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.contents) != 1 {
		t.Fatalf("expected 1 resource link, got %d", len(ts.contents))
	}
	link, ok := ts.contents[0].(mcp.ResourceLink)
	if !ok || link.URI != "https://example.com/report.html" {
		t.Errorf("expected ResourceLink to the https URI, got %#v", ts.contents[0])
	}
}

func TestHandleFilePart_InlineBytesDecodeError(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	ts := &taskState{toolName: "t", done: make(chan taskResult, 1), sessionID: "s"}
	a.handleFilePart(t.Context(), "tk", ts, a2a.Part{Kind: "file", File: &a2a.FileContent{
		Name: "bad.bin", MIMEType: "application/octet-stream", Bytes: "!!!not base64!!!",
	}})
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if len(ts.contents) != 0 {
		t.Errorf("expected no content on base64 decode error, got %d", len(ts.contents))
	}
}

func TestAppendArtifactContent_EmptyMimeDefaultsToBlob(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	ts := &taskState{toolName: "t", done: make(chan taskResult, 1), sessionID: "s"}
	a.handleFilePart(t.Context(), "tk", ts, a2a.Part{Kind: "file", File: &a2a.FileContent{
		Name: "blob.dat", Bytes: base64.StdEncoding.EncodeToString([]byte("raw")),
	}})
	ts.mu.Lock()
	defer ts.mu.Unlock()
	er, ok := ts.contents[0].(mcp.EmbeddedResource)
	if !ok {
		t.Fatalf("expected EmbeddedResource, got %#v", ts.contents[0])
	}
	if blob, ok := er.Resource.(mcp.BlobResourceContents); !ok || blob.MIMEType != "application/octet-stream" {
		t.Errorf("expected octet-stream blob, got %#v", er.Resource)
	}
}

func TestReadArtifactResource_TextAndErrors(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	saveArtifact(t, scoped, "Ag", "u", "s", "notes.md", []byte("# hi"), "text/markdown")
	ref := artifactRef{agentName: "Ag", userID: "u", sessionID: "s", filename: "notes.md", mimeType: "text/markdown"}

	got, err := a.readArtifactResource(t.Context(), ref, "artifact://s/notes.md")
	if err != nil {
		t.Fatalf("readArtifactResource: %v", err)
	}
	txt, ok := got[0].(mcp.TextResourceContents)
	if !ok || txt.Text != "# hi" {
		t.Errorf("expected TextResourceContents '# hi', got %#v", got[0])
	}

	noStore := newArtifactAdapterNoStore(t)
	if _, err := noStore.readArtifactResource(t.Context(), ref, "artifact://s/notes.md"); err == nil {
		t.Error("expected error when no artifact service configured")
	}
}

func TestOnArtifactUpdate_UnknownTask(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	a.OnArtifactUpdate(t.Context(), "ghost", &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{Parts: []a2a.Part{{Kind: "file", File: &a2a.FileContent{Name: "x"}}}},
	}, nil)
}

func TestOnFinalResponse_UnknownTask(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	a.OnFinalResponse(t.Context(), "ghost", &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("hi")}}},
	}, nil)
}

func TestBuildToolResult_TextPlusContents(t *testing.T) {
	img := mcp.ImageContent{Type: "image", Data: "abc", MIMEType: "image/png"}
	res := buildToolResult("here you go", []mcp.Content{img})
	if len(res.Content) != 2 {
		t.Fatalf("expected text + image (2 contents), got %d", len(res.Content))
	}
	if tc, ok := res.Content[0].(mcp.TextContent); !ok || tc.Text != "here you go" {
		t.Errorf("content[0] should be the final text, got %#v", res.Content[0])
	}
	if _, ok := res.Content[1].(mcp.ImageContent); !ok {
		t.Errorf("content[1] should be the image, got %#v", res.Content[1])
	}
}
