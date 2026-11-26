// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package render

import (
	"encoding/json"
	"fmt"
)

type storedTask struct {
	ID             string `json:"id"`
	MessageBubbles string `json:"messageBubbles"`
}

type storedBubble struct {
	Type         string       `json:"type"`
	Text         string       `json:"text"`
	Parts        []storedPart `json:"parts"`
	IsError      bool         `json:"isError"`
	IsWarning    bool         `json:"isWarning"`
	InputRequest *struct {
		RequestID string `json:"requestId"`
	} `json:"userInputRequest"`
	AuthLink json.RawMessage `json:"authenticationLink"`
}

type storedPart struct {
	Kind     string         `json:"kind"`
	Text     string         `json:"text"`
	Name     string         `json:"name"`
	Status   string         `json:"status"`
	Tags     any            `json:"tags"`
	File     *wireFile      `json:"file"`
	Metadata map[string]any `json:"metadata"`
}

func FromChatTasks(body []byte) ([]Bubble, error) {
	var page struct {
		Data []storedTask `json:"data"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("decoding chat tasks: %w", err)
	}
	var bubbles []Bubble
	for _, task := range page.Data {
		var stored []storedBubble
		if err := json.Unmarshal([]byte(task.MessageBubbles), &stored); err != nil {
			return nil, fmt.Errorf("decoding bubbles of task %s: %w", task.ID, err)
		}
		for _, sb := range stored {
			bubbles = append(bubbles, sb.bubble(task.ID))
		}
	}
	return bubbles, nil
}

func (sb storedBubble) bubble(taskID string) Bubble {
	b := Bubble{
		Role:        roleAgent,
		TaskID:      taskID,
		IsError:     sb.IsError,
		IsWarning:   sb.IsWarning,
		AuthRequest: len(sb.AuthLink) > 0 && string(sb.AuthLink) != "null",
		Complete:    true,
	}
	if sb.Type == roleUser {
		b.Role = roleUser
	}
	if sb.InputRequest != nil {
		b.InputRequest = sb.InputRequest.RequestID
	}
	for _, p := range sb.Parts {
		part := Part{Kind: p.Kind, Text: p.Text, Name: p.Name, Status: p.Status}
		switch p.Kind {
		case partFile:
			if p.File != nil {
				part.Name = p.File.Name
			}
			part.Hidden = hasWorkingTag(p.Metadata["tags"])
		case partArtifact:
			part.Hidden = p.Status != statusFailed && hasWorkingTag(p.Tags)
		}
		b.Parts = append(b.Parts, part)
	}
	if len(b.Parts) == 0 && sb.Text != "" {
		b.Parts = []Part{{Kind: partText, Text: sb.Text}}
	}
	return b
}
