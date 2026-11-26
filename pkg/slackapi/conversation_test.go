// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"fmt"
	"testing"
)

func TestInMemoryGetConversationReplies_OldestFirstAndScoped(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedConversationMessage("C1", "t.1", "t.1", "U-a", "parent")
	api.SeedConversationMessage("C1", "t.1", "t.2", "U-b", "reply-1")
	api.SeedConversationMessage("C1", "t.1", "t.3", "U-a", "reply-2")
	api.SeedConversationMessage("C1", "other.1", "t.9", "U-c", "different thread")
	api.SeedConversationMessage("C2", "t.1", "t.4", "U-d", "different channel")

	msgs, err := api.GetConversationReplies(context.Background(), "C1", "t.1", 50)
	if err != nil {
		t.Fatalf("replies: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3 (thread t.1 in C1 only)", len(msgs))
	}
	want := []string{"parent", "reply-1", "reply-2"}
	for i, w := range want {
		if msgs[i].Text != w {
			t.Errorf("msg[%d] = %q, want %q (oldest-first)", i, msgs[i].Text, w)
		}
	}
}

func TestInMemoryGetConversationReplies_KeepsNewestWhenOverLimit(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedConversationMessage("C1", "t.1", "t.1", "U-a", "parent")
	api.SeedConversationMessage("C1", "t.1", "t.2", "U-b", "r2")
	api.SeedConversationMessage("C1", "t.1", "t.3", "U-a", "r3")
	api.SeedConversationMessage("C1", "t.1", "t.4", "U-b", "r4")
	api.SeedConversationMessage("C1", "t.1", "t.5", "U-a", "r5")

	msgs, err := api.GetConversationReplies(context.Background(), "C1", "t.1", 3)
	if err != nil {
		t.Fatalf("replies: %v", err)
	}
	want := []string{"r3", "r4", "r5"}
	if len(msgs) != len(want) {
		t.Fatalf("got %d messages, want 3 newest", len(msgs))
	}
	for i, w := range want {
		if msgs[i].Text != w {
			t.Errorf("msg[%d] = %q, want %q", i, msgs[i].Text, w)
		}
	}
}

func TestInMemoryGetConversationHistory_NewestFirstAndLimited(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SeedConversationMessage("C1", "", "h.1", "U-a", "m1")
	api.SeedConversationMessage("C1", "", "h.2", "U-b", "m2")
	api.SeedConversationMessage("C1", "", "h.3", "U-a", "m3")

	msgs, err := api.GetConversationHistory(context.Background(), "C1", 2)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (limit)", len(msgs))
	}
	if msgs[0].Text != "m3" || msgs[1].Text != "m2" {
		t.Errorf("history order = [%q, %q], want [m3, m2]", msgs[0].Text, msgs[1].Text)
	}
}

func TestInMemoryConversation_ErrorInjection(t *testing.T) {
	api := NewInMemorySlackAPI()
	api.SetNextError("GetConversationReplies", fmt.Errorf("channel_not_found"))
	if _, err := api.GetConversationReplies(context.Background(), "C1", "t.1", 10); err == nil {
		t.Fatal("expected injected error from GetConversationReplies")
	}
	if _, err := api.GetConversationReplies(context.Background(), "C1", "t.1", 10); err != nil {
		t.Fatalf("error should be one-shot, got %v", err)
	}
}
