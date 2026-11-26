// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newArtifactAdapter(t *testing.T) (*Adapter, *artifact.ScopedService) {
	t.Helper()
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped))
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{})
	core.SetServices(rt.Services())

	a := &Adapter{
		core:       core,
		cfg:        mustParseConfig(t, nil),
		toolMap:    make(map[string]toolMapping),
		agentTools: make(map[string][]string),
		tasks:      make(map[string]*taskState),
		logger:     noopLogger(),
	}
	return a, scoped
}

func newArtifactAdapterWithLimit(t *testing.T, limitBytes int) (*Adapter, *artifact.ScopedService) {
	t.Helper()
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	rt := runtime.NewRuntime(brk, nil, runtime.WithArtifacts(scoped))
	core := entrypoint.NewGatewayCore(&entrypoint.LoggingAdapter{}, map[string]any{
		"namespace":                            "default",
		"gateway_id":                           "test-mcp-gw",
		"gateway_artifact_content_limit_bytes": limitBytes,
	})
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init gateway core: %v", err)
	}
	a := &Adapter{
		core:       core,
		cfg:        mustParseConfig(t, nil),
		toolMap:    make(map[string]toolMapping),
		agentTools: make(map[string][]string),
		tasks:      make(map[string]*taskState),
		logger:     noopLogger(),
	}
	return a, scoped
}

func saveArtifact(t *testing.T, scoped *artifact.ScopedService, agent, user, session, filename string, content []byte, mime string) {
	t.Helper()
	loc := scoped.Resolve(agent, user, session, filename)
	if _, err := scoped.Inner.Save(t.Context(), loc, content, mime); err != nil {
		t.Fatalf("save artifact %q: %v", filename, err)
	}
}

func artifactCompletedEvent(filename, mime string, version int) *a2a.TaskStatusUpdateEvent {
	return &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{
			Message: &a2a.Message{
				Parts: []a2a.Part{{
					Kind: "data",
					Data: map[string]any{
						"type":      "artifact_creation_progress",
						"status":    "completed",
						"filename":  filename,
						"mime_type": mime,
						"version":   float64(version),
					},
				}},
			},
		},
	}
}

func TestArtifactSignal_InlineImage(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "ChartAgent", "alice", "sess-img"
		taskID               = "task-img"
		filename             = "chart.png"
	)
	imgBytes := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
	saveArtifact(t, scoped, agent, user, session, filename, imgBytes, "image/png")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "chart", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "image/png", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("Here is your chart.")}}},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	img := findImageContent(t, res.contents)
	if img.MIMEType != "image/png" {
		t.Errorf("image mime = %q, want image/png", img.MIMEType)
	}
	if want := base64.StdEncoding.EncodeToString(imgBytes); img.Data != want {
		t.Errorf("image data = %q, want %q", img.Data, want)
	}
}

func TestArtifactSignal_BlobEmbedded(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "DeckAgent", "bob", "sess-pptx"
		taskID               = "task-pptx"
		filename             = "deck.pptx"
		pptxMime             = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	)
	pptxBytes := []byte("PK\x03\x04fake-pptx")
	saveArtifact(t, scoped, agent, user, session, filename, pptxBytes, pptxMime)

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "pptx", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, pptxMime, 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("Slides ready.")}}},
	}, nil)

	res := <-ts.done
	blob := findBlobResource(t, res.contents)
	if blob.MIMEType != pptxMime {
		t.Errorf("blob mime = %q, want %q", blob.MIMEType, pptxMime)
	}
	if want := base64.StdEncoding.EncodeToString(pptxBytes); blob.Blob != want {
		t.Errorf("blob data = %q, want %q", blob.Blob, want)
	}
}

func findImageContent(t *testing.T, contents []mcp.Content) mcp.ImageContent {
	t.Helper()
	for _, c := range contents {
		if img, ok := c.(mcp.ImageContent); ok {
			return img
		}
	}
	t.Fatalf("no ImageContent in result (%d contents)", len(contents))
	return mcp.ImageContent{}
}

func findBlobResource(t *testing.T, contents []mcp.Content) mcp.BlobResourceContents {
	t.Helper()
	for _, c := range contents {
		if er, ok := c.(mcp.EmbeddedResource); ok {
			if blob, ok := er.Resource.(mcp.BlobResourceContents); ok {
				return blob
			}
		}
	}
	t.Fatalf("no EmbeddedResource/BlobResourceContents in result (%d contents)", len(contents))
	return mcp.BlobResourceContents{}
}

func TestArtifactSignal_TextEmbedded(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "DataAgent", "carol", "sess-txt"
		taskID               = "task-txt"
		filename             = "report.json"
	)
	body := []byte(`{"ok":true}`)
	saveArtifact(t, scoped, agent, user, session, filename, body, "application/json")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "data", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "application/json", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("done")}}},
	}, nil)

	res := <-ts.done
	txt := findTextResource(t, res.contents)
	if txt.Text != string(body) {
		t.Errorf("text resource = %q, want %q", txt.Text, body)
	}
	if txt.URI != "artifact://"+session+"/"+filename {
		t.Errorf("text resource uri = %q", txt.URI)
	}
}

func TestArtifactSignal_ResourceLinkAndRead(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	a.cfg.InlineBinaryMaxBytes = 4
	const (
		agent, user, session = "BigAgent", "dan", "sess-big"
		taskID               = "task-big"
		filename             = "big.bin"
	)
	blob := []byte("this binary is larger than four bytes")
	saveArtifact(t, scoped, agent, user, session, filename, blob, "application/octet-stream")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "big", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "application/octet-stream", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("big file ready")}}},
	}, nil)

	res := <-ts.done
	link := findResourceLink(t, res.contents)
	wantURI := "artifact://" + session + "/" + filename
	if link.URI != wantURI {
		t.Errorf("resource link uri = %q, want %q", link.URI, wantURI)
	}
	if link.Name != filename {
		t.Errorf("resource link name = %q, want %q", link.Name, filename)
	}

	ref := artifactRef{agentName: agent, userID: user, sessionID: session, filename: filename, mimeType: "application/octet-stream"}
	contents, err := a.readArtifactResource(t.Context(), ref, link.URI)
	if err != nil {
		t.Fatalf("readArtifactResource: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 resource content, got %d", len(contents))
	}
	rc, ok := contents[0].(mcp.BlobResourceContents)
	if !ok {
		t.Fatalf("expected BlobResourceContents, got %T", contents[0])
	}
	if want := base64.StdEncoding.EncodeToString(blob); rc.Blob != want {
		t.Errorf("read blob = %q, want %q", rc.Blob, want)
	}
}

func TestArtifactSignal_DedupSameFilename(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "DupAgent", "erin", "sess-dup"
		taskID               = "task-dup"
		filename             = "once.png"
	)
	saveArtifact(t, scoped, agent, user, session, filename, []byte("img"), "image/png")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "dup", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "image/png", 0), nil)
	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "image/png", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("here")}}},
	}, nil)

	res := <-ts.done
	count := 0
	for _, c := range res.contents {
		if _, ok := c.(mcp.ImageContent); ok {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 image content after dedup, got %d", count)
	}
}

func TestBuildToolResult_TextOnlyUnchanged(t *testing.T) {
	got := buildToolResult("hello world", nil)
	want := mcp.NewToolResultText("hello world")
	if len(got.Content) != 1 {
		t.Fatalf("text-only result has %d contents, want 1", len(got.Content))
	}
	gt, ok := got.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want TextContent", got.Content[0])
	}
	wt := want.Content[0].(mcp.TextContent)
	if gt.Text != wt.Text || gt.Type != wt.Type {
		t.Errorf("text-only result = %+v, want %+v", gt, wt)
	}
}

func findTextResource(t *testing.T, contents []mcp.Content) mcp.TextResourceContents {
	t.Helper()
	for _, c := range contents {
		if er, ok := c.(mcp.EmbeddedResource); ok {
			if txt, ok := er.Resource.(mcp.TextResourceContents); ok {
				return txt
			}
		}
	}
	t.Fatalf("no EmbeddedResource/TextResourceContents in result (%d contents)", len(contents))
	return mcp.TextResourceContents{}
}

func findResourceLink(t *testing.T, contents []mcp.Content) mcp.ResourceLink {
	t.Helper()
	for _, c := range contents {
		if link, ok := c.(mcp.ResourceLink); ok {
			return link
		}
	}
	t.Fatalf("no ResourceLink in result (%d contents)", len(contents))
	return mcp.ResourceLink{}
}

func TestBuildToolResult_EmptyTextWithContents(t *testing.T) {
	img := mcp.ImageContent{Type: "image", Data: "abc", MIMEType: "image/png"}
	got := buildToolResult("", []mcp.Content{img})
	if len(got.Content) != 1 {
		t.Fatalf("got %d contents, want 1 (no empty leading text)", len(got.Content))
	}
	if _, ok := got.Content[0].(mcp.ImageContent); !ok {
		t.Errorf("content[0] = %T, want ImageContent", got.Content[0])
	}
}

func TestArtifactSignal_InlineAudio(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "VoiceAgent", "frank", "sess-aud"
		taskID               = "task-aud"
		filename             = "reply.mp3"
	)
	audio := []byte("ID3fake-mp3-bytes")
	saveArtifact(t, scoped, agent, user, session, filename, audio, "audio/mpeg")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "voice", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "audio/mpeg", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("audio ready")}}},
	}, nil)

	res := <-ts.done
	var aud *mcp.AudioContent
	for _, c := range res.contents {
		if ac, ok := c.(mcp.AudioContent); ok {
			aud = &ac
		}
	}
	if aud == nil {
		t.Fatalf("no AudioContent in result (%d contents)", len(res.contents))
	}
	if aud.MIMEType != "audio/mpeg" {
		t.Errorf("audio mime = %q, want audio/mpeg", aud.MIMEType)
	}
	if want := base64.StdEncoding.EncodeToString(audio); aud.Data != want {
		t.Errorf("audio data mismatch")
	}
}

func TestArtifactSignal_LoadFailureThenRetry(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "RetryAgent", "gwen", "sess-retry"
		taskID               = "task-retry"
		filename             = "late.png"
	)
	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "retry", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "image/png", 0), nil)
	ts.mu.Lock()
	got := len(ts.contents)
	ts.mu.Unlock()
	if got != 0 {
		t.Fatalf("expected no content after failed load, got %d", got)
	}

	imgBytes := []byte("\x89PNGlate")
	saveArtifact(t, scoped, agent, user, session, filename, imgBytes, "image/png")
	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "image/png", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("ok")}}},
	}, nil)

	res := <-ts.done
	img := findImageContent(t, res.contents)
	if want := base64.StdEncoding.EncodeToString(imgBytes); img.Data != want {
		t.Errorf("retried image data mismatch")
	}
}

func TestOnArtifactUpdate_FilePart(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "ArtAgent", "heidi", "sess-au"
		taskID               = "task-au"
		filename             = "diagram.png"
	)
	img := []byte("\x89PNGdiagram")
	saveArtifact(t, scoped, agent, user, session, filename, img, "image/png")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "art", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	uri := "artifact://" + agent + "/" + user + "/" + session + "/" + filename
	a.OnArtifactUpdate(t.Context(), taskID, &a2a.TaskArtifactUpdateEvent{
		Artifact: a2a.Artifact{
			ArtifactID: "a1",
			Parts:      []a2a.Part{{Kind: "file", File: &a2a.FileContent{Name: filename, MIMEType: "image/png", URI: uri}}},
		},
	}, nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("ok")}}},
	}, nil)

	res := <-ts.done
	img2 := findImageContent(t, res.contents)
	if want := base64.StdEncoding.EncodeToString(img); img2.Data != want {
		t.Errorf("OnArtifactUpdate image data mismatch")
	}
}

func TestOnFinalResponse_FilePart(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "FinAgent", "ivan", "sess-fin"
		taskID               = "task-fin"
		filename             = "notes.txt"
	)
	body := []byte("final notes")
	saveArtifact(t, scoped, agent, user, session, filename, body, "text/plain")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "fin", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	uri := "artifact://" + agent + "/" + user + "/" + session + "/" + filename
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("see attached"),
			{Kind: "file", File: &a2a.FileContent{Name: filename, MIMEType: "text/plain", URI: uri}},
		}}},
	}, nil)

	res := <-ts.done
	if res.text != "see attached" {
		t.Errorf("final text = %q, want %q", res.text, "see attached")
	}
	txt := findTextResource(t, res.contents)
	if txt.Text != string(body) {
		t.Errorf("text resource = %q, want %q", txt.Text, body)
	}
}

func TestOnFinalResponse_StripsArtifactReturnDirective(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "ReportAgent", "carol", "sess-ar"
		taskID               = "task-ar"
		filename             = "report.pdf"
	)
	saveArtifact(t, scoped, agent, user, session, filename, []byte("%PDF-1.4 fake"), "application/pdf")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "report", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "application/pdf", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("Here is your report. «artifact_return:report.pdf»"),
		}}},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if strings.Contains(res.text, "artifact_return") {
		t.Errorf("artifact_return directive leaked into final text: %q", res.text)
	}
	if strings.ContainsAny(res.text, "«»") {
		t.Errorf("embed guillemets leaked into final text: %q", res.text)
	}
	if !strings.Contains(res.text, "Here is your report.") {
		t.Errorf("surrounding text should be preserved, got: %q", res.text)
	}
	if blob := findBlobResource(t, res.contents); blob.MIMEType != "application/pdf" {
		t.Errorf("artifact blob mime = %q, want application/pdf", blob.MIMEType)
	}
}

func TestOnFinalResponse_ArtifactReturnOnlyText(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "ReportAgent", "carol", "sess-aro"
		taskID               = "task-aro"
		filename             = "report.pdf"
	)
	saveArtifact(t, scoped, agent, user, session, filename, []byte("%PDF-1.4 fake"), "application/pdf")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "report", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "application/pdf", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("«artifact_return:report.pdf»"),
		}}},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if strings.TrimSpace(res.text) != "" {
		t.Errorf("directive-only text should collapse to empty, got: %q", res.text)
	}
	if findBlobResource(t, res.contents).MIMEType != "application/pdf" {
		t.Errorf("artifact content missing from result")
	}
}

func TestOnFinalResponse_InlinesArtifactContent(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "DocAgent", "dave", "sess-ac"
		taskID               = "task-ac"
	)
	saveArtifact(t, scoped, agent, user, session, "summary.txt", []byte("Q4 revenue: $1M"), "text/plain")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "doc", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("Summary: «artifact_content:summary.txt»"),
		}}},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if strings.Contains(res.text, "artifact_content") || strings.ContainsAny(res.text, "«»") {
		t.Errorf("artifact_content directive leaked: %q", res.text)
	}
	if !strings.Contains(res.text, "Q4 revenue: $1M") {
		t.Errorf("artifact content not inlined: %q", res.text)
	}
}

func TestOnFinalResponse_BinaryArtifactContentDelivered(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "ChartAgent", "carol", "sess-bac"
		taskID               = "task-bac"
		filename             = "chart.png"
	)
	imgBytes := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
	saveArtifact(t, scoped, agent, user, session, filename, imgBytes, "image/png")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "chart", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("«artifact_content:chart.png»"),
		}}},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if strings.ContainsAny(res.text, "«»") {
		t.Errorf("binary artifact_content directive leaked into text: %q", res.text)
	}
	img := findImageContent(t, res.contents)
	if want := base64.StdEncoding.EncodeToString(imgBytes); img.Data != want {
		t.Errorf("inline-binary content not delivered as MCP image content")
	}
}

func TestOnFinalResponse_TextBufDirective(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "StreamAgent", "eve", "sess-buf-dir"
		taskID               = "task-buf-dir"
		filename             = "output.pdf"
	)
	saveArtifact(t, scoped, agent, user, session, filename, []byte("%PDF-1.4 fake"), "application/pdf")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "s", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	ts.textBuf.WriteString("Here you go. «artifact_return:output.pdf»")
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, artifactCompletedEvent(filename, "application/pdf", 0), nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{State: "completed"},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if strings.Contains(res.text, "artifact_return") || strings.ContainsAny(res.text, "«»") {
		t.Errorf("directive from textBuf leaked into result: %q", res.text)
	}
	if !strings.Contains(res.text, "Here you go.") {
		t.Errorf("surrounding buffer text should be preserved, got: %q", res.text)
	}
}

func TestOnFinalResponse_ArtifactContentSizeCap(t *testing.T) {
	const limit = 16
	a, scoped := newArtifactAdapterWithLimit(t, limit)
	const (
		agent, user, session = "DocAgent", "dave", "sess-cap"
		taskID               = "task-cap"
	)
	big := []byte("this text is definitely longer than sixteen bytes")
	if len(big) <= limit {
		t.Fatalf("test fixture must exceed the cap")
	}
	saveArtifact(t, scoped, agent, user, session, "big.txt", big, "text/plain")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "doc", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("Body: «artifact_content:big.txt»"),
		}}},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if !strings.Contains(res.text, "[Truncated: artifact size limit reached]") {
		t.Errorf("expected truncation marker (cap=%d applied), got: %q", limit, res.text)
	}
	if strings.Contains(res.text, string(big)) {
		t.Errorf("oversized artifact body should not be inlined: %q", res.text)
	}
}

func TestOnFinalResponse_MultipleDirectives(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "MixAgent", "mia", "sess-mix"
		taskID               = "task-mix"
	)
	img := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
	saveArtifact(t, scoped, agent, user, session, "summary.txt", []byte("Q4 revenue: $1M"), "text/plain")
	saveArtifact(t, scoped, agent, user, session, "chart.png", img, "image/png")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "mix", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("Report: «artifact_content:summary.txt» — chart: «artifact_content:chart.png»"),
		}}},
	}, nil)

	res := <-ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	if strings.ContainsAny(res.text, "«»") {
		t.Errorf("directives leaked: %q", res.text)
	}
	if !strings.Contains(res.text, "Q4 revenue: $1M") {
		t.Errorf("text artifact_content should be inlined, got: %q", res.text)
	}
	if got := findImageContent(t, res.contents); got.Data != base64.StdEncoding.EncodeToString(img) {
		t.Errorf("binary artifact_content not delivered as MCP image content")
	}
}

func TestArtifactSignal_InlineBytesFilePart(t *testing.T) {
	a, _ := newArtifactAdapter(t)
	const (
		agent, user, session = "InlAgent", "judy", "sess-inl"
		taskID               = "task-inl"
		filename             = "inline.png"
	)
	img := []byte("\x89PNGinline")
	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "inl", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	a.OnStatusUpdate(t.Context(), taskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			{Kind: "file", File: &a2a.FileContent{
				Name: filename, MIMEType: "image/png",
				Bytes: base64.StdEncoding.EncodeToString(img),
			}},
		}}},
	}, nil)
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart("ok")}}},
	}, nil)

	res := <-ts.done
	got := findImageContent(t, res.contents)
	if want := base64.StdEncoding.EncodeToString(img); got.Data != want {
		t.Errorf("inline-bytes file part image data mismatch")
	}
}

func TestOnFinalResponse_FilePartNameDiffersFromURI(t *testing.T) {
	a, scoped := newArtifactAdapter(t)
	const (
		agent, user, session = "FinAgent", "ivan", "sess-disp"
		taskID               = "task-disp"
		storedName           = "report_final_v2.txt"
		displayName          = "Quarterly Report.txt"
	)
	body := []byte("quarterly numbers")
	saveArtifact(t, scoped, agent, user, session, storedName, body, "text/plain")

	_ = a.core.Tasks().Register(taskID, agent, user, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "fin", done: make(chan taskResult, 1), agentName: agent, userID: user, sessionID: session}
	a.tasks[taskID] = ts

	uri := "artifact://" + agent + "/" + user + "/" + session + "/" + storedName
	a.OnFinalResponse(t.Context(), taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
			a2a.NewTextPart("see attached"),
			{Kind: "file", File: &a2a.FileContent{Name: displayName, MIMEType: "text/plain", URI: uri}},
		}}},
	}, nil)

	res := <-ts.done
	txt := findTextResource(t, res.contents)
	if txt.Text != string(body) {
		t.Fatalf("artifact dropped or wrong bytes: text resource = %q, want %q", txt.Text, body)
	}
	if want := "artifact://" + session + "/" + storedName; txt.URI != want {
		t.Errorf("resource uri = %q, want %q", txt.URI, want)
	}
}
