// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestToV1SendMessageRequest(t *testing.T) {
	fileBytes := []byte("%PDF-1.7 report")
	params := map[string]any{
		"message": map[string]any{
			"kind":      "message",
			"messageId": "msg-1",
			"role":      "user",
			"contextId": "session-1",
			"taskId":    "sam-task-1",
			"parts": []any{
				map[string]any{"kind": "text", "text": "Summarise this"},
				map[string]any{"kind": "data", "data": map[string]any{"region": "eu"}},
				map[string]any{"kind": "file", "file": map[string]any{"name": "report.pdf", "mimeType": "application/pdf", "bytes": base64.StdEncoding.EncodeToString(fileBytes)}},
				map[string]any{"kind": "file", "file": map[string]any{"name": "logo.png", "mimeType": "image/png", "uri": "https://cdn.example.com/logo.png"}},
			},
		},
		"configuration": map[string]any{"acceptedOutputModes": []any{"text/plain"}, "blocking": false},
		"metadata":      map[string]any{"agent_name": "remote"},
	}

	req, err := toV1SendMessageRequest(params, "acme")
	if err != nil {
		t.Fatalf("toV1SendMessageRequest: %v", err)
	}
	if req.Tenant != "acme" {
		t.Errorf("Tenant = %q", req.Tenant)
	}
	msg := req.Message
	if msg.ID != "msg-1" || msg.ContextID != "session-1" || msg.Role != a2av1.MessageRoleUser {
		t.Errorf("message = %+v", msg)
	}
	if msg.TaskID != "" {
		t.Errorf("TaskID = %q, want the SAM task ID dropped", msg.TaskID)
	}
	if len(msg.Parts) != 4 {
		t.Fatalf("parts = %d, want 4", len(msg.Parts))
	}
	if got := msg.Parts[0].Text(); got != "Summarise this" {
		t.Errorf("text part = %q", got)
	}
	if got, ok := msg.Parts[1].Data().(map[string]any); !ok || got["region"] != "eu" {
		t.Errorf("data part = %#v", msg.Parts[1].Data())
	}
	if got := msg.Parts[2]; string(got.Raw()) != string(fileBytes) || got.Filename != "report.pdf" || got.MediaType != "application/pdf" {
		t.Errorf("bytes file part = %+v", got)
	}
	if got := msg.Parts[3]; got.URL() != "https://cdn.example.com/logo.png" || got.Filename != "logo.png" || got.MediaType != "image/png" {
		t.Errorf("uri file part = %+v", got)
	}
	if req.Config == nil || len(req.Config.AcceptedOutputModes) != 1 || req.Config.ReturnImmediately {
		t.Errorf("config = %+v, want accepted modes kept and the call left blocking", req.Config)
	}
	if req.Metadata["agent_name"] != "remote" {
		t.Errorf("metadata = %v", req.Metadata)
	}
}

func TestToV1SendMessageRequest_EmptyFileIsAnEmptyRawPart(t *testing.T) {
	params := map[string]any{"message": map[string]any{"role": "user", "parts": []any{
		map[string]any{"kind": "file", "file": map[string]any{"name": "empty.txt", "mimeType": "text/plain"}},
	}}}
	req, err := toV1SendMessageRequest(params, "")
	if err != nil {
		t.Fatalf("toV1SendMessageRequest: %v", err)
	}
	raw, err := json.Marshal(req.Message.Parts[0])
	if err != nil {
		t.Fatalf("marshal part: %v", err)
	}
	if !strings.Contains(string(raw), `"raw":""`) || !strings.Contains(string(raw), `"filename":"empty.txt"`) {
		t.Errorf("part = %s, want an empty raw part named empty.txt", raw)
	}
}

func TestToV1SendMessageRequest_Rejects(t *testing.T) {
	tests := []struct {
		name string
		part map[string]any
	}{
		{"unknown part kind", map[string]any{"kind": "video"}},
		{"file part without a file", map[string]any{"kind": "file"}},
		{"file bytes that are not base64", map[string]any{"kind": "file", "file": map[string]any{"bytes": "not base64!"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"message": map[string]any{"role": "user", "parts": []any{tt.part}}}
			if _, err := toV1SendMessageRequest(params, ""); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

func TestFromV1Event_StatusUpdateFinalFollowsState(t *testing.T) {
	tests := []struct {
		state     a2av1.TaskState
		wantState a2a.TaskState
		wantFinal bool
	}{
		{a2av1.TaskStateSubmitted, a2a.TaskStateSubmitted, false},
		{a2av1.TaskStateWorking, a2a.TaskStateWorking, false},
		{a2av1.TaskStateCompleted, a2a.TaskStateCompleted, true},
		{a2av1.TaskStateFailed, a2a.TaskStateFailed, true},
		{a2av1.TaskStateCanceled, a2a.TaskStateCancelled, true},
		{a2av1.TaskStateRejected, a2a.TaskStateRejected, true},
		{a2av1.TaskStateInputRequired, a2a.TaskStateInputNeeded, true},
		{a2av1.TaskStateAuthRequired, a2a.TaskStateAuthRequired, true},
		{a2av1.TaskStateUnspecified, a2a.TaskStateUnknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.state.String(), func(t *testing.T) {
			events, ends := fromV1Event(&a2av1.TaskStatusUpdateEvent{TaskID: "remote-1", ContextID: "ctx", Status: a2av1.TaskStatus{State: tt.state}})
			if len(events) != 1 || events[0].StatusUpdate == nil {
				t.Fatalf("events = %+v, want one status update", events)
			}
			su := events[0].StatusUpdate
			if su.Status.State != tt.wantState || su.Final != tt.wantFinal || ends != tt.wantFinal {
				t.Errorf("state = %q final = %v ends = %v, want %q %v", su.Status.State, su.Final, ends, tt.wantState, tt.wantFinal)
			}
			if su.Kind != "status-update" || su.TaskID != "remote-1" {
				t.Errorf("status update = %+v", su)
			}
		})
	}
}

func TestFromV1Event_ArtifactUpdate(t *testing.T) {
	part := a2av1.NewRawPart([]byte("csv,data"))
	part.Filename, part.MediaType = "out.csv", "text/csv"
	events, ends := fromV1Event(&a2av1.TaskArtifactUpdateEvent{
		TaskID:    "remote-1",
		Artifact:  &a2av1.Artifact{ID: "art-1", Name: "out", Parts: a2av1.ContentParts{part}},
		LastChunk: true,
	})
	if ends || len(events) != 1 || events[0].ArtifactUpdate == nil {
		t.Fatalf("events = %+v ends = %v", events, ends)
	}
	au := events[0].ArtifactUpdate
	if au.Kind != "artifact-update" || au.Artifact.ArtifactID != "art-1" || !au.LastChunk {
		t.Errorf("artifact update = %+v", au)
	}
	file := au.Artifact.Parts[0].File
	if au.Artifact.Parts[0].Kind != "file" || file.Name != "out.csv" || file.MIMEType != "text/csv" || file.Bytes != base64.StdEncoding.EncodeToString([]byte("csv,data")) {
		t.Errorf("file part = %+v", au.Artifact.Parts[0])
	}
}

func TestFromV1Event_ArtifactChunkKeepsAppendAndExtensions(t *testing.T) {
	events, _ := fromV1Event(&a2av1.TaskArtifactUpdateEvent{
		TaskID:   "remote-1",
		Artifact: &a2av1.Artifact{ID: "art-1", Parts: a2av1.ContentParts{a2av1.NewTextPart("more")}, Extensions: []string{"https://example.com/ext"}},
		Append:   true,
	})
	au := events[0].ArtifactUpdate
	if !au.Append || au.LastChunk {
		t.Errorf("append = %v lastChunk = %v, want a middle chunk appended to the artifact", au.Append, au.LastChunk)
	}
	if len(au.Artifact.Extensions) != 1 || au.Artifact.Extensions[0] != "https://example.com/ext" {
		t.Errorf("extensions = %v", au.Artifact.Extensions)
	}
}

func TestFromV1Event_TaskKeepsMetadata(t *testing.T) {
	events, _ := fromV1Event(&a2av1.Task{ID: "remote-1", Status: a2av1.TaskStatus{State: a2av1.TaskStateWorking}, Metadata: map[string]any{"region": "eu"}})
	if got := events[0].Task.Metadata["region"]; got != "eu" {
		t.Errorf("metadata region = %v", got)
	}
}

func TestFromV1Event_Task(t *testing.T) {
	ts := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	task := func(state a2av1.TaskState) *a2av1.Task {
		return &a2av1.Task{ID: "remote-1", ContextID: "ctx", Status: a2av1.TaskStatus{State: state, Timestamp: &ts}}
	}

	t.Run("a working task is a progress notification", func(t *testing.T) {
		events, ends := fromV1Event(task(a2av1.TaskStateWorking))
		if ends || len(events) != 1 || events[0].Task == nil {
			t.Fatalf("events = %+v ends = %v", events, ends)
		}
		if got := events[0].Task.Status.Timestamp; got != "2026-10-30T12:00:00.000000+00:00" {
			t.Errorf("timestamp = %q", got)
		}
	})

	t.Run("a completed task ends the stream as itself", func(t *testing.T) {
		events, ends := fromV1Event(task(a2av1.TaskStateCompleted))
		if !ends || len(events) != 1 || events[0].Task == nil || events[0].Task.Status.State != a2a.TaskStateCompleted {
			t.Fatalf("events = %+v ends = %v", events, ends)
		}
	})

	for _, state := range []a2av1.TaskState{a2av1.TaskStateRejected, a2av1.TaskStateInputRequired, a2av1.TaskStateAuthRequired} {
		t.Run(state.String()+" ends the stream as itself, artifacts included", func(t *testing.T) {
			snapshot := task(state)
			snapshot.Artifacts = []*a2av1.Artifact{{ID: "art-1", Parts: a2av1.ContentParts{a2av1.NewTextPart("draft")}}}
			events, ends := fromV1Event(snapshot)
			if !ends || len(events) != 1 || events[0].Task == nil {
				t.Fatalf("events = %+v ends = %v", events, ends)
			}
			got := events[0].Task
			if got.Status.State != fromV1State(state) || len(got.Artifacts) != 1 || got.Artifacts[0].ArtifactID != "art-1" {
				t.Errorf("task = %+v, want state %q with its artifact", got, fromV1State(state))
			}
		})
	}
}

func TestFromV1Event_TaskKeepsHistoryArtifactsAndEveryPartKind(t *testing.T) {
	raw := a2av1.NewRawPart([]byte("png-bytes"))
	raw.Filename, raw.MediaType = "chart.png", "image/png"
	link := a2av1.NewFileURLPart("https://cdn.example.com/report.pdf", "application/pdf")
	link.Filename = "report.pdf"
	unknown := &a2av1.Part{}
	task := &a2av1.Task{
		ID:        "remote-1",
		ContextID: "ctx",
		Status:    a2av1.TaskStatus{State: a2av1.TaskStateCompleted},
		History:   []*a2av1.Message{a2av1.NewMessage(a2av1.MessageRoleUser, a2av1.NewTextPart("question")), nil},
		Artifacts: []*a2av1.Artifact{{
			ID:    "art-1",
			Name:  "results",
			Parts: a2av1.ContentParts{a2av1.NewTextPart("summary"), a2av1.NewDataPart(map[string]any{"rows": 3}), raw, link, unknown, nil},
		}},
	}

	events, ends := fromV1Event(task)
	if !ends || len(events) != 1 || events[0].Task == nil {
		t.Fatalf("events = %+v ends = %v", events, ends)
	}
	got := events[0].Task
	if len(got.History) != 1 || got.History[0].Role != a2a.RoleUser || got.History[0].Parts[0].Text != "question" {
		t.Errorf("history = %+v, want the one user message", got.History)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].ArtifactID != "art-1" || got.Artifacts[0].Name != "results" {
		t.Fatalf("artifacts = %+v", got.Artifacts)
	}
	parts := got.Artifacts[0].Parts
	if len(parts) != 4 {
		t.Fatalf("parts = %+v, want text, data, bytes and url with the empty and nil parts dropped", parts)
	}
	if parts[0].Kind != "text" || parts[0].Text != "summary" {
		t.Errorf("text part = %+v", parts[0])
	}
	if data, ok := parts[1].Data.(map[string]any); parts[1].Kind != "data" || !ok || data["rows"] != 3 {
		t.Errorf("data part = %+v", parts[1])
	}
	if f := parts[2].File; parts[2].Kind != "file" || f.Name != "chart.png" || f.MIMEType != "image/png" || f.Bytes != base64.StdEncoding.EncodeToString([]byte("png-bytes")) {
		t.Errorf("bytes part = %+v", parts[2])
	}
	if f := parts[3].File; parts[3].Kind != "file" || f.Name != "report.pdf" || f.URI != "https://cdn.example.com/report.pdf" || f.MIMEType != "application/pdf" {
		t.Errorf("url part = %+v", parts[3])
	}
}

func TestFromV1Event_MessageIsACompletedTask(t *testing.T) {
	msg := a2av1.NewMessage(a2av1.MessageRoleAgent, a2av1.NewTextPart("Hello"))
	msg.ContextID = "ctx"
	events, ends := fromV1Event(msg)
	if !ends || len(events) != 1 || events[0].Task == nil {
		t.Fatalf("events = %+v ends = %v", events, ends)
	}
	task := events[0].Task
	if task.Kind != "task" || task.Status.State != a2a.TaskStateCompleted || task.ContextID != "ctx" {
		t.Errorf("task = %+v", task)
	}
	reply := task.Status.Message
	if reply == nil || reply.Role != a2a.RoleAgent || len(reply.Parts) != 1 || reply.Parts[0].Text != "Hello" {
		t.Errorf("status message = %+v", reply)
	}
}
