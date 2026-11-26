// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package chatv2

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/swaggest/jsonschema-go"
)

const (
	BubbleUser   = "user"
	BubbleAgent  = "agent"
	BubbleSystem = "system"
)

const saveChatTaskText = "Stores one turn of the conversation in the session's history, replacing the chat task with this id if the session already holds one. " +
	"The client writes the history: the gateway does not derive it from the task events. " +
	"Saving a turn is how a conversation is kept, shown in the session list, searched, shared and sent to the model again by a scheduled task, so a client must do it for every turn. " +
	"Send the turn's messages as messageBubbles. Each bubble needs a type and a text. Add an id, and parts that list every file the message produced or used, because a share link and a scheduled-task notification include only the files the history names. " +
	"The declared keys of a bubble (type, text, id, isError, parts) are validated and returned normalised, so an empty id or a null parts is not kept. Any other key of a bubble or of taskMetadata is kept and returned with the same keys and values, and the gateway does not read it. Key order and number precision may change. " +
	"A save can fail or never happen, for example when the network drops or the client closes before the final save. The turn is then not in the history until the client reads GET /sessions/{id}/events/unconsumed, replays the events and saves the result. The gateway keeps those events only for the completed-task retention of the event buffer (7 days by default), after which the turn is lost. " +
	"A taskMetadata with status pending marks the save sent with the user's message, before the agent has answered. " +
	"The task id must not belong to another user's task, and the session must be the caller's, or the answer is 404."

var misTypedProblem = map[string]string{
	"isError": "must be a boolean",
	"parts":   "must be an array of objects",
	"id":      "must be a string",
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

type Bubble struct {
	Type    string            `json:"type" example:"agent"`
	Text    string            `json:"text" example:"Revenue grew 12% over the quarter."`
	ID      string            `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	IsError *bool             `json:"isError" example:"false"`
	Parts   []json.RawMessage `json:"parts" example:"[{\"kind\":\"artifact\",\"file\":{\"uri\":\"artifact://0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c/user:report.pdf?version=2\"}}]"`

	hasText    bool
	extensions map[string]json.RawMessage
}

func (b Bubble) misTyped() (field string, ok bool) {
	for _, key := range []string{"isError", "parts", "id"} {
		if _, kept := b.extensions[key]; kept {
			return key, true
		}
	}
	return "", false
}

func (Bubble) PrepareJSONSchema(schema *jsonschema.Schema) error {
	allowed := true
	schema.AdditionalProperties = &jsonschema.SchemaOrBool{TypeBoolean: &allowed}
	return nil
}

func (b *Bubble) declared() map[string]any {
	d := map[string]any{"type": &b.Type, "text": &b.Text, "id": &b.ID, "isError": &b.IsError, "parts": &b.Parts}
	return d
}

func (b *Bubble) UnmarshalJSON(data []byte) error {
	extensions, present, err := splitOpenObject(data, b.declared())
	if err != nil {
		return err
	}
	b.extensions, b.hasText = extensions, present["text"]
	return nil
}

func (b Bubble) MarshalJSON() ([]byte, error) { return b.marshal(true) }

func (b Bubble) marshal(withNulls bool) ([]byte, error) {
	declared := map[string]any{}
	set := func(key string, present bool, value any) {
		switch {
		case present:
			declared[key] = value
		case withNulls:
			declared[key] = nil
		}
	}
	set("type", b.Type != "", b.Type)
	set("text", b.hasText || b.Text != "", b.Text)
	set("id", b.ID != "", b.ID)
	set("isError", b.IsError != nil, b.IsError)
	set("parts", b.Parts != nil, b.Parts)
	return joinOpenObject(declared, b.extensions)
}

func StoredBubbles(bubbles []Bubble) ([]byte, error) {
	out := make([]json.RawMessage, len(bubbles))
	for i, b := range bubbles {
		raw, err := b.marshal(false)
		if err != nil {
			return nil, err
		}
		out[i] = raw
	}
	return json.Marshal(out)
}

type ChatTaskMetadata struct {
	AgentName string `json:"agentName" example:"Support"`

	extensions map[string]json.RawMessage
}

func (ChatTaskMetadata) PrepareJSONSchema(schema *jsonschema.Schema) error {
	allowed := true
	schema.AdditionalProperties = &jsonschema.SchemaOrBool{TypeBoolean: &allowed}
	return nil
}

func (m *ChatTaskMetadata) UnmarshalJSON(data []byte) error {
	extensions, _, err := splitOpenObject(data, map[string]any{"agentName": &m.AgentName})
	if err != nil {
		return err
	}
	m.extensions = extensions
	return nil
}

func (m ChatTaskMetadata) MarshalJSON() ([]byte, error) { return m.marshal(true) }

func (m ChatTaskMetadata) marshal(withNulls bool) ([]byte, error) {
	declared := map[string]any{}
	switch {
	case m.AgentName != "":
		declared["agentName"] = m.AgentName
	case withNulls:
		declared["agentName"] = nil
	}
	return joinOpenObject(declared, m.extensions)
}

func StoredMetadata(m ChatTaskMetadata) ([]byte, error) { return m.marshal(false) }

type SessionChatTask struct {
	ID             string              `json:"id" example:"0190f5f3-7d8e-7f9a-8b0c-2d3e4f5a6b7c"`
	Type           string              `json:"type" example:"sessionChatTask"`
	SessionID      string              `json:"sessionId" example:"0190f5ee-1d2e-7f3a-9b4c-5d6e7f8a9b0c"`
	CreatedBy      string              `json:"createdBy" example:"jane.doe@example.com"`
	UserMessage    *string             `json:"userMessage" example:"Summarize the quarterly report."`
	MessageBubbles []Bubble            `json:"messageBubbles"`
	TaskMetadata   *ChatTaskMetadata   `json:"taskMetadata"`
	CreatedTime    apicontract.Instant `json:"createdTime"`
	UpdatedTime    apicontract.Instant `json:"updatedTime"`
}

type SaveChatTaskRequest struct {
	SessionID         string            `path:"sessionId" json:"-"`
	ID                string            `path:"id" json:"-"`
	UserMessage       *string           `json:"userMessage" example:"Summarize the quarterly report."`
	MessageBubbles    []Bubble          `json:"messageBubbles"`
	TaskMetadata      *ChatTaskMetadata `json:"taskMetadata"`
	AutoGenerateTitle bool              `json:"autoGenerateTitle" example:"true"`
	ClientSurface     string            `header:"X-Sam-Client-Surface" json:"-" example:"ui"`
}

func (r SaveChatTaskRequest) Problems() map[string][]string {
	problems := map[string][]string{}
	if len(r.MessageBubbles) == 0 {
		problems["messageBubbles"] = []string{"must hold at least one bubble"}
	}
	for i, b := range r.MessageBubbles {
		at := func(field string) string { return fmt.Sprintf("messageBubbles[%d].%s", i, field) }
		switch b.Type {
		case BubbleUser, BubbleAgent:
		case BubbleSystem:
			problems[at("type")] = []string{"system bubbles are written by the gateway and cannot be saved"}
		default:
			problems[at("type")] = []string{"must be user or agent"}
		}
		if !b.hasText {
			problems[at("text")] = []string{"is required"}
		}
		if field, bad := b.misTyped(); bad {
			problems[at(field)] = []string{misTypedProblem[field]}
		}
		for j, part := range b.Parts {
			if !isJSONObject(part) {
				problems[at(fmt.Sprintf("parts[%d]", j))] = []string{"must be an object"}
			}
		}
	}
	return problems
}

type ListChatTasksRequest struct {
	apicontract.StrictRequest
	ID   string           `path:"id" json:"-"`
	Page apicontract.Page `json:"-"`
}
