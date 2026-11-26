// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/notify/store"
)

func TestParseActionMeta(t *testing.T) {
	tests := []struct {
		name      string
		payload   string
		wantNil   bool
		wantLabel string
		wantSess  string
		wantAgent string
		wantShare string
		wantPath  string
	}{
		{name: "empty payload", payload: "", wantNil: true},
		{name: "no session or share reference", payload: `{"scheduled_task_id":"t1","status":"completed"}`, wantNil: true},
		{name: "invalid json", payload: `{not json`, wantNil: true},
		{
			name:      "session with explicit label and agent",
			payload:   `{"session_id":"sess-1","agent_id":"Reporter","link_label":"View result"}`,
			wantLabel: "View result",
			wantSess:  "sess-1",
			wantAgent: "Reporter",
		},
		{
			name:      "session without label falls back to default",
			payload:   `{"session_id":"sess-2"}`,
			wantLabel: defaultSessionActionLabel,
			wantSess:  "sess-2",
		},
		{
			name:      "share without label falls back to the share default",
			payload:   `{"share_id":"share-1","share_url":"https://host/#/share/share-1","access_level":"resource_viewer"}`,
			wantLabel: defaultShareActionLabel,
			wantShare: "share-1",
		},
		{
			name:      "share with explicit label",
			payload:   `{"share_id":"share-2","link_label":"Open the chat"}`,
			wantLabel: "Open the chat",
			wantShare: "share-2",
		},
		{
			name:      "session and share together keep both references and the session label",
			payload:   `{"session_id":"sess-3","share_id":"share-3"}`,
			wantLabel: defaultSessionActionLabel,
			wantSess:  "sess-3",
			wantShare: "share-3",
		},
		{
			name:      "in-app path with explicit label",
			payload:   `{"link_path":"/budgets/b-1/usage?tab=usage","link_label":"Review budget"}`,
			wantLabel: "Review budget",
			wantPath:  "/budgets/b-1/usage?tab=usage",
		},
		{
			name:      "in-app path without label falls back to the path default",
			payload:   `{"link_path":"/budgets"}`,
			wantLabel: defaultPathActionLabel,
			wantPath:  "/budgets",
		},
		{name: "protocol-relative path is refused", payload: `{"link_path":"//evil.example/x"}`, wantNil: true},
		{name: "backslash path is refused", payload: `{"link_path":"/\\evil.example/x"}`, wantNil: true},
		{name: "absolute URL is refused", payload: `{"link_path":"https://evil.example/x"}`, wantNil: true},
		{name: "script URL is refused", payload: `{"link_path":"javascript:alert(1)"}`, wantNil: true},
		{name: "relative path is refused", payload: `{"link_path":"budgets"}`, wantNil: true},
		{name: "control character is refused", payload: `{"link_path":"/budgets\tx"}`, wantNil: true},
		{
			name:      "an encoded backslash stays a same-origin path",
			payload:   `{"link_path":"/%5Cevil.example"}`,
			wantLabel: defaultPathActionLabel,
			wantPath:  "/%5Cevil.example",
		},
		{
			name:      "encoded slashes stay a same-origin path",
			payload:   `{"link_path":"/%2F%2Fevil.example"}`,
			wantLabel: defaultPathActionLabel,
			wantPath:  "/%2F%2Fevil.example",
		},
		{
			name:      "refused path does not hide a session reference",
			payload:   `{"session_id":"sess-4","link_path":"//evil.example"}`,
			wantLabel: defaultSessionActionLabel,
			wantSess:  "sess-4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseActionMeta(tt.payload)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil action, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected an action, got nil")
			}
			if got.Label != tt.wantLabel || got.SessionID != tt.wantSess || got.AgentID != tt.wantAgent || got.ShareID != tt.wantShare || got.Path != tt.wantPath {
				t.Errorf("got %+v, want {Label:%q SessionID:%q AgentID:%q ShareID:%q Path:%q}", got, tt.wantLabel, tt.wantSess, tt.wantAgent, tt.wantShare, tt.wantPath)
			}
		})
	}
}

func TestToDTO_SurfacesAction(t *testing.T) {
	withSession := toDTO(store.Notification{
		ID:      "11111111-1111-1111-1111-111111111111",
		Title:   "Scheduled task: daily-report",
		Body:    "Status: completed",
		Status:  "unread",
		Payload: `{"scheduled_task_id":"t1","session_id":"sess-9","link_label":"View result"}`,
	})
	if withSession.Action == nil {
		t.Fatalf("expected Action on a payload carrying session_id")
	}
	if withSession.Action.SessionID != "sess-9" || withSession.Action.Label != "View result" {
		t.Errorf("unexpected action: %+v", withSession.Action)
	}

	plain := toDTO(store.Notification{
		ID:      "22222222-2222-2222-2222-222222222222",
		Title:   "FYI",
		Body:    "done",
		Status:  "unread",
		Payload: `{"task_id":"a2a-1"}`,
	})
	if plain.Action != nil {
		t.Errorf("fire-and-forget row must not carry an action, got %+v", plain.Action)
	}

	withShare := toDTO(store.Notification{
		ID:      "33333333-3333-3333-3333-333333333333",
		Title:   "owner@example.com shared a chat with you",
		Body:    "\"Stonehenge Overview and History\"\nhttps://host/#/share/share-9",
		Status:  "unread",
		Source:  "chat_share",
		Payload: `{"share_id":"share-9","share_url":"https://host/#/share/share-9","owner_email":"owner@example.com","access_level":"resource_viewer","link_label":"View shared chat"}`,
	})
	if withShare.Action == nil {
		t.Fatalf("expected Action on a payload carrying share_id")
	}
	if withShare.Action.ShareID != "share-9" || withShare.Action.Label != "View shared chat" {
		t.Errorf("unexpected action: %+v", withShare.Action)
	}
	if withShare.Action.SessionID != "" {
		t.Errorf("viewer share action must not carry a session reference, got %q", withShare.Action.SessionID)
	}

	withEditorShare := toDTO(store.Notification{
		ID:      "44444444-4444-4444-4444-444444444444",
		Title:   "owner@example.com shared a chat with you",
		Status:  "unread",
		Source:  "chat_share",
		Payload: `{"share_id":"share-9","session_id":"sess-9","access_level":"resource_editor","link_label":"View shared chat"}`,
	})
	if withEditorShare.Action == nil {
		t.Fatalf("expected Action on an editor share payload")
	}
	if withEditorShare.Action.SessionID != "sess-9" || withEditorShare.Action.ShareID != "share-9" {
		t.Errorf("editor share action must carry both references, got %+v", withEditorShare.Action)
	}
	if withEditorShare.Action.Label != "View shared chat" {
		t.Errorf("producer link_label must win over the session default, got %q", withEditorShare.Action.Label)
	}
}
