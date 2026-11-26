// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	a2av1 "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/proxy"
)

const internalTimestampLayout = "2006-01-02T15:04:05.000000+00:00"

type sendParams struct {
	Message       a2a.Message `json:"message"`
	Configuration *struct {
		AcceptedOutputModes []string `json:"acceptedOutputModes"`
		HistoryLength       *int     `json:"historyLength"`
	} `json:"configuration"`
	Metadata map[string]any `json:"metadata"`
}

func toV1SendMessageRequest(params any, tenant string) (*a2av1.SendMessageRequest, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode message params: %w", err)
	}
	var p sendParams
	if err = json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode message params: %w", err)
	}
	msg, err := toV1Message(p.Message)
	if err != nil {
		return nil, err
	}
	req := &a2av1.SendMessageRequest{Tenant: tenant, Message: msg, Metadata: p.Metadata}
	if c := p.Configuration; c != nil {
		req.Config = &a2av1.SendMessageConfig{AcceptedOutputModes: c.AcceptedOutputModes, HistoryLength: c.HistoryLength}
	}
	return req, nil
}

func toV1Message(m a2a.Message) (*a2av1.Message, error) {
	parts := make(a2av1.ContentParts, 0, len(m.Parts))
	for i, p := range m.Parts {
		part, err := toV1Part(p)
		if err != nil {
			return nil, fmt.Errorf("message part %d: %w", i, err)
		}
		parts = append(parts, part)
	}
	id := m.MessageID
	if id == "" {
		id = a2av1.NewMessageID()
	}
	return &a2av1.Message{
		ID:        id,
		ContextID: m.ContextID,
		Metadata:  m.Metadata,
		Parts:     parts,
		Role:      toV1Role(m.Role),
	}, nil
}

func toV1Role(r a2a.Role) a2av1.MessageRole {
	if r == a2a.RoleAgent {
		return a2av1.MessageRoleAgent
	}
	return a2av1.MessageRoleUser
}

func toV1Part(p a2a.Part) (*a2av1.Part, error) {
	var part *a2av1.Part
	switch p.Kind {
	case "text":
		part = a2av1.NewTextPart(p.Text)
	case "data":
		part = a2av1.NewDataPart(p.Data)
	case "file":
		if p.File == nil {
			return nil, fmt.Errorf("file part has no file")
		}
		if p.File.URI != "" && p.File.Bytes == "" {
			part = &a2av1.Part{Content: a2av1.URL(p.File.URI)}
		} else {
			raw, err := base64.StdEncoding.DecodeString(p.File.Bytes)
			if err != nil {
				return nil, fmt.Errorf("decode file bytes: %w", err)
			}
			part = a2av1.NewRawPart(raw)
		}
		part.Filename = p.File.Name
		part.MediaType = p.File.MIMEType
	default:
		return nil, fmt.Errorf("unsupported part kind %q", p.Kind)
	}
	part.Metadata = p.Metadata
	return part, nil
}

func fromV1Event(ev a2av1.Event) ([]proxy.ResponseEvent, bool) {
	switch e := ev.(type) {
	case *a2av1.TaskStatusUpdateEvent:
		ends := endsStream(e.Status.State)
		return []proxy.ResponseEvent{{StatusUpdate: &a2a.TaskStatusUpdateEvent{
			TaskID:    string(e.TaskID),
			ContextID: e.ContextID,
			Kind:      "status-update",
			Status:    fromV1Status(e.Status),
			Final:     ends,
			Metadata:  e.Metadata,
		}}}, ends
	case *a2av1.TaskArtifactUpdateEvent:
		return []proxy.ResponseEvent{{ArtifactUpdate: &a2a.TaskArtifactUpdateEvent{
			TaskID:    string(e.TaskID),
			ContextID: e.ContextID,
			Kind:      "artifact-update",
			Artifact:  fromV1Artifact(e.Artifact),
			Append:    e.Append,
			LastChunk: e.LastChunk,
			Metadata:  e.Metadata,
		}}}, false
	case *a2av1.Task:
		return []proxy.ResponseEvent{{Task: fromV1Task(e)}}, endsStream(e.Status.State)
	case *a2av1.Message:
		return []proxy.ResponseEvent{{Task: completedTaskFromMessage(e)}}, true
	default:
		return nil, false
	}
}

func endsStream(s a2av1.TaskState) bool {
	return s.Terminal() || s == a2av1.TaskStateInputRequired || s == a2av1.TaskStateAuthRequired
}

func completedTaskFromMessage(m *a2av1.Message) *a2a.Task {
	return &a2a.Task{
		ID:        string(m.TaskID),
		ContextID: m.ContextID,
		Kind:      "task",
		Status: a2a.TaskStatus{
			State:     a2a.TaskStateCompleted,
			Message:   fromV1Message(m),
			Timestamp: time.Now().UTC().Format(internalTimestampLayout),
		},
	}
}

func fromV1Task(t *a2av1.Task) *a2a.Task {
	task := &a2a.Task{
		ID:        string(t.ID),
		ContextID: t.ContextID,
		Kind:      "task",
		Status:    fromV1Status(t.Status),
		Metadata:  t.Metadata,
	}
	for _, m := range t.History {
		if m != nil {
			task.History = append(task.History, *fromV1Message(m))
		}
	}
	for _, art := range t.Artifacts {
		task.Artifacts = append(task.Artifacts, fromV1Artifact(art))
	}
	return task
}

func fromV1Status(s a2av1.TaskStatus) a2a.TaskStatus {
	status := a2a.TaskStatus{State: fromV1State(s.State)}
	if s.Message != nil {
		status.Message = fromV1Message(s.Message)
	}
	if s.Timestamp != nil {
		status.Timestamp = s.Timestamp.UTC().Format(internalTimestampLayout)
	}
	return status
}

var stateFromV1 = map[a2av1.TaskState]a2a.TaskState{
	a2av1.TaskStateSubmitted:     a2a.TaskStateSubmitted,
	a2av1.TaskStateWorking:       a2a.TaskStateWorking,
	a2av1.TaskStateCompleted:     a2a.TaskStateCompleted,
	a2av1.TaskStateFailed:        a2a.TaskStateFailed,
	a2av1.TaskStateCanceled:      a2a.TaskStateCancelled,
	a2av1.TaskStateRejected:      a2a.TaskStateRejected,
	a2av1.TaskStateInputRequired: a2a.TaskStateInputNeeded,
	a2av1.TaskStateAuthRequired:  a2a.TaskStateAuthRequired,
}

func fromV1State(s a2av1.TaskState) a2a.TaskState {
	if state, ok := stateFromV1[s]; ok {
		return state
	}
	return a2a.TaskStateUnknown
}

func fromV1Message(m *a2av1.Message) *a2a.Message {
	role := a2a.RoleUser
	if m.Role == a2av1.MessageRoleAgent {
		role = a2a.RoleAgent
	}
	return &a2a.Message{
		Kind:      "message",
		MessageID: m.ID,
		Role:      role,
		Parts:     fromV1Parts(m.Parts),
		TaskID:    string(m.TaskID),
		ContextID: m.ContextID,
		Metadata:  m.Metadata,
	}
}

func fromV1Artifact(art *a2av1.Artifact) a2a.Artifact {
	if art == nil {
		return a2a.Artifact{Parts: []a2a.Part{}}
	}
	return a2a.Artifact{
		ArtifactID:  string(art.ID),
		Name:        art.Name,
		Description: art.Description,
		Parts:       fromV1Parts(art.Parts),
		Metadata:    art.Metadata,
		Extensions:  art.Extensions,
	}
}

func fromV1Parts(parts a2av1.ContentParts) []a2a.Part {
	out := make([]a2a.Part, 0, len(parts))
	for _, p := range parts {
		if part, ok := fromV1Part(p); ok {
			out = append(out, part)
		}
	}
	return out
}

func fromV1Part(p *a2av1.Part) (a2a.Part, bool) {
	if p == nil {
		return a2a.Part{}, false
	}
	switch c := p.Content.(type) {
	case a2av1.Text:
		return a2a.Part{Kind: "text", Text: string(c), Metadata: p.Metadata}, true
	case a2av1.Data:
		return a2a.Part{Kind: "data", Data: c.Value, Metadata: p.Metadata}, true
	case a2av1.Raw:
		return a2a.Part{Kind: "file", Metadata: p.Metadata, File: &a2a.FileContent{
			Name:     p.Filename,
			MIMEType: p.MediaType,
			Bytes:    base64.StdEncoding.EncodeToString(c),
		}}, true
	case a2av1.URL:
		return a2a.Part{Kind: "file", Metadata: p.Metadata, File: &a2a.FileContent{
			Name:     p.Filename,
			MIMEType: p.MediaType,
			URI:      string(c),
		}}, true
	default:
		return a2a.Part{}, false
	}
}
