// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
)

func TestUserTimezoneDoesNotEnterSystemPrompt(t *testing.T) {
	agent, _, _ := newTestAgent()
	sess := &session.Session{}

	utc, _ := agent.buildMessages(context.Background(), nil, sess, "hello", gatewayContext{})
	toronto, _ := agent.buildMessages(context.Background(), nil, sess, "hello", gatewayContext{UserTimezone: "America/Toronto"})
	tokyo, _ := agent.buildMessages(context.Background(), nil, sess, "hello", gatewayContext{UserTimezone: "Asia/Tokyo"})

	if utc[0].Role != "system" {
		t.Fatalf("expected a system message first, got %q", utc[0].Role)
	}
	if utc[0].Content != toronto[0].Content || utc[0].Content != tokyo[0].Content {
		t.Error("system prompt differs by user timezone — the cached prefix would be per-timezone")
	}
	for _, forbidden := range []string{"America/Toronto", "Asia/Tokyo", currentTimePrefix} {
		if strings.Contains(utc[0].Content, forbidden) || strings.Contains(toronto[0].Content, forbidden) {
			t.Errorf("system prompt contains %q; it must stay free of request-scoped values", forbidden)
		}
	}
}

func TestHistoryTurnsAreNotRestamped(t *testing.T) {
	agent, _, _ := newTestAgent()
	const stampedTurn1 = "[Current time: Monday, 2020-01-06T09:00:00Z (UTC)]\nfirst question"
	sess := &session.Session{
		Messages: []session.Message{
			{Role: "user", Content: stampedTurn1},
			{Role: "assistant", Content: "first answer"},
		},
	}

	msgs, _ := agent.buildMessages(context.Background(), nil, sess, "second question", gatewayContext{UserTimezone: "America/Toronto"})

	var userTurns []string
	for _, m := range msgs {
		if m.Role == "user" {
			userTurns = append(userTurns, m.Content)
		}
	}
	if len(userTurns) != 2 {
		t.Fatalf("expected 2 user turns, got %d", len(userTurns))
	}
	if userTurns[0] != stampedTurn1 {
		t.Errorf("history turn was rewritten:\n got %q\nwant %q", userTurns[0], stampedTurn1)
	}
	if !strings.HasPrefix(userTurns[1], currentTimePrefix) {
		t.Errorf("current turn is missing its stamp: %q", userTurns[1])
	}
	if strings.Count(userTurns[1], currentTimePrefix) != 1 {
		t.Errorf("current turn carries %d stamps, want 1: %q", strings.Count(userTurns[1], currentTimePrefix), userTurns[1])
	}
}

func TestOnlyTheNewestTurnChangesBetweenBuilds(t *testing.T) {
	agent, _, _ := newTestAgent()
	sess := &session.Session{
		Messages: []session.Message{
			{Role: "user", Content: "[Current time: Monday, 2020-01-06T09:00:00Z (UTC)]\nq1"},
			{Role: "assistant", Content: "a1"},
			{Role: "user", Content: "[Current time: Monday, 2020-01-06T09:05:00Z (UTC)]\nq2"},
			{Role: "assistant", Content: "a2"},
		},
	}

	first, _ := agent.buildMessages(context.Background(), nil, sess, "q3", gatewayContext{UserTimezone: "America/Toronto"})
	second, _ := agent.buildMessages(context.Background(), nil, sess, "q3", gatewayContext{UserTimezone: "America/Toronto"})

	if len(first) != len(second) {
		t.Fatalf("message count changed between builds: %d vs %d", len(first), len(second))
	}
	for i := range len(first) - 1 {
		if first[i].Content != second[i].Content {
			t.Errorf("prefix message %d (role %q) changed between builds — cache would miss:\n got %q\nwant %q",
				i, first[i].Role, second[i].Content, first[i].Content)
		}
	}
}
