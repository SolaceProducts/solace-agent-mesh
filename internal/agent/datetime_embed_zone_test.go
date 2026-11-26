// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestDatetimeEmbedRendersInUserZone(t *testing.T) {
	now := time.Date(2026, 9, 23, 14, 29, 53, 0, time.UTC)
	const layout = "%I:%M:%S %p on %A, %B %d, %Y"

	tests := []struct {
		name, tz, want string
	}{
		{"user zone", "America/Toronto", "10:29:53 AM on Wednesday, September 23, 2026"},
		{"date rolls over", "Asia/Tokyo", "11:29:53 PM on Wednesday, September 23, 2026"},
		{"no zone renders UTC", "", "02:29:53 PM on Wednesday, September 23, 2026"},
		{"unresolvable zone renders UTC", "Not/A_Real_Zone", "02:29:53 PM on Wednesday, September 23, 2026"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveDatetimeAt(now, layout, tc.tz)
			if err != nil {
				t.Fatalf("resolveDatetimeAt: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveDatetimeAt(%q) = %q, want %q", tc.tz, got, tc.want)
			}
		})
	}
}

func TestDatetimeEmbedISOCarriesUserOffset(t *testing.T) {
	now := time.Date(2026, 9, 23, 14, 29, 53, 0, time.UTC)
	got, err := resolveDatetimeAt(now, "iso", "America/Toronto")
	if err != nil {
		t.Fatalf("resolveDatetimeAt: %v", err)
	}
	if want := "2026-09-23T10:29:53.000000-04:00"; got != want {
		t.Errorf("iso = %q, want %q", got, want)
	}
}

func TestEmbedResolverUsesItsUserTimezone(t *testing.T) {
	r := NewEmbedResolver(nil, "agent", "alice", "sess", "Asia/Tokyo", nil)

	got, err := r.Resolve(t.Context(), &EmbedRef{Type: "datetime", Params: map[string]string{"value": "%Z"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !strings.Contains(got, "JST") {
		t.Errorf("datetime embed rendered %q, want Tokyo's JST abbreviation", got)
	}
}

func TestHandleTaskRendersDatetimeEmbedInUserZone(t *testing.T) {
	agent, _, _ := newTestAgent(&llm.ChatResponse{
		Content:      "Your zone is «datetime:%Z».",
		FinishReason: "stop",
	})

	resp, err := agent.HandleTask(t.Context(), "task-tz", newUserMessage("what time is it?"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1", A2AUserConfig: `{"user_timezone":"Asia/Tokyo"}`})
	if err != nil {
		t.Fatal(err)
	}
	task, ok := resp.Result.(a2a.Task)
	if !ok {
		t.Fatalf("expected Task result, got %T", resp.Result)
	}
	if len(task.Status.Message.Parts) == 0 {
		t.Fatal("expected response parts")
	}
	if got := task.Status.Message.Parts[0].Text; got != "Your zone is JST." {
		t.Errorf("response = %q, want the embed rendered in the user's zone (JST)", got)
	}
}

func TestHandleTaskRendersToolArgDatetimeEmbedInUserZone(t *testing.T) {
	agent, _, _ := newTestAgent(
		&llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "call-1", Name: "note", Arguments: `{"text": "zone «datetime:%Z»"}`}},
		},
		&llm.ChatResponse{Content: "done", FinishReason: "stop"},
	)
	agent.svc.(*mockServices).arts = &artifact.ScopedService{Inner: artmem.New()}
	var got string
	noteTool := &testBuiltinTool{
		name:        "note",
		description: "Records a note",
		params: &tool.JSONSchema{
			Type:       "object",
			Properties: map[string]any{"text": map[string]any{"type": "string"}},
		},
		execFn: func(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
			got, _ = args["text"].(string)
			return tool.OK("ok"), nil
		},
	}
	if err := agent.Tools().Register(noteTool); err != nil {
		t.Fatal(err)
	}

	if _, err := agent.HandleTask(t.Context(), "task-tz-tool", newUserMessage("note the zone"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1", A2AUserConfig: `{"user_timezone":"Asia/Tokyo"}`}); err != nil {
		t.Fatal(err)
	}
	if got != "zone JST" {
		t.Errorf("tool received %q, want the embed rendered in the user's zone (zone JST)", got)
	}
}
