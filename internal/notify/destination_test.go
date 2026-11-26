// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package notify

import (
	"strings"
	"testing"
)

func TestDestinationLabel(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		want string
	}{
		{"connector platform from str_binary", Channel{Type: "connector", Config: map[string]any{"str_binary": "slack", "tool_name": "slack_send_message_990e8400"}}, "Slack"},
		{"connector platform from tool_name prefix", Channel{Type: "connector", Config: map[string]any{"tool_name": "teams_send_message_x"}}, "Teams"},
		{"operator label wins", Channel{Type: "connector", Config: map[string]any{"label": "Slack — #alerts", "str_binary": "slack"}}, "Slack — #alerts"},
		{"webhook host (not the token URL)", Channel{Type: "webhook", Config: map[string]any{"url": "https://hooks.example.com/p?token=secret"}}, "hooks.example.com"},
		{"email address", Channel{Type: "email", Config: map[string]any{"address": "a@x.com"}}, "a@x.com"},
		{"learned slack is type-level", Channel{Type: "slack"}, ""},
	}
	for _, c := range cases {
		if got := destinationLabel(c.ch); got != c.want {
			t.Errorf("%s: label = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDestinationKey(t *testing.T) {
	if got := destinationKey(Channel{Type: "connector", Config: map[string]any{"tool_name": "slack_send_message_x"}}); got != "slack_send_message_x" {
		t.Errorf("connector key = %q, want the tool name", got)
	}
	k := destinationKey(Channel{Type: "webhook", Config: map[string]any{"url": "https://hooks.example.com/p?token=secret"}})
	if !strings.HasPrefix(k, "wh_") || strings.Contains(k, "secret") || strings.Contains(k, "hooks.example.com") {
		t.Errorf("webhook key should be a secret-free hash, got %q", k)
	}
	if got := destinationKey(Channel{Type: "slack"}); got != "" {
		t.Errorf("slack key should be empty (one destination per user), got %q", got)
	}
}
