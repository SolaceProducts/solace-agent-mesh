// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateStr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in string
		n        int
		want     string
	}{
		{"shorter than limit", "abc", 10, "abc"},
		{"exact limit", "abc", 3, "abc"},
		{"ascii cut", "abcdef", 3, "abc"},
		{"backs off a split 2-byte rune", "abé", 3, "ab"},
		{"keeps a rune that fits", "abé", 4, "abé"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := truncateStr(tc.in, tc.n); got != tc.want {
				t.Errorf("truncateStr(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestValidateConversationHistory(t *testing.T) {
	t.Parallel()

	t.Run("truncates to max", func(t *testing.T) {
		t.Parallel()
		history := make([]ConversationTurn, 60)
		for i := range history {
			history[i] = ConversationTurn{Role: "user", Content: "msg"}
		}
		result := validateConversationHistory(history)
		if len(result) != maxConversationHistory {
			t.Errorf("expected %d, got %d", maxConversationHistory, len(result))
		}
	})

	t.Run("filters invalid roles", func(t *testing.T) {
		t.Parallel()
		history := []ConversationTurn{
			{Role: "user", Content: "hello"},
			{Role: "system", Content: "bad"},
			{Role: "assistant", Content: "hi"},
			{Role: "admin", Content: "nope"},
		}
		result := validateConversationHistory(history)
		if len(result) != 2 {
			t.Fatalf("expected 2, got %d", len(result))
		}
		if result[0].Role != "user" || result[1].Role != "assistant" {
			t.Errorf("unexpected roles: %v, %v", result[0].Role, result[1].Role)
		}
	})

	t.Run("truncates long content", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", maxMessageContent+100)
		history := []ConversationTurn{{Role: "user", Content: long}}
		result := validateConversationHistory(history)
		if len(result[0].Content) != maxMessageContent {
			t.Errorf("expected content length %d, got %d", maxMessageContent, len(result[0].Content))
		}
	})

	t.Run("never splits a multi-byte rune at the cut", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("a", maxMessageContent-1) + "économie"
		history := []ConversationTurn{{Role: "user", Content: long}}
		result := validateConversationHistory(history)
		if !utf8.ValidString(result[0].Content) {
			t.Error("content contains invalid UTF-8")
		}
	})
}

func TestSanitizeCurrentTask(t *testing.T) {
	t.Parallel()

	t.Run("filters disallowed fields", func(t *testing.T) {
		t.Parallel()
		task := map[string]any{
			"name":        "My Task",
			"evil_field":  "hack",
			"description": "desc",
		}
		result := sanitizeCurrentTask(task)
		if _, ok := result["evil_field"]; ok {
			t.Error("disallowed field should be removed")
		}
		if result["name"] != "My Task" {
			t.Error("allowed field should be kept")
		}
	})

	t.Run("truncates long strings", func(t *testing.T) {
		t.Parallel()
		task := map[string]any{
			"name": strings.Repeat("x", maxTaskFieldLen+100),
		}
		result := sanitizeCurrentTask(task)
		if len(result["name"].(string)) != maxTaskFieldLen {
			t.Errorf("expected truncation to %d", maxTaskFieldLen)
		}
	})

	t.Run("never splits a multi-byte rune at the cut", func(t *testing.T) {
		t.Parallel()
		task := map[string]any{
			"name": strings.Repeat("x", maxTaskFieldLen-1) + "économie",
		}
		result := sanitizeCurrentTask(task)
		if !utf8.ValidString(result["name"].(string)) {
			t.Error("name contains invalid UTF-8")
		}
	})

	t.Run("nil input returns empty map", func(t *testing.T) {
		t.Parallel()
		result := sanitizeCurrentTask(nil)
		if len(result) != 0 {
			t.Error("expected empty map for nil input")
		}
	})
}

func TestValidateAvailableAgents(t *testing.T) {
	t.Parallel()

	makeNames := func(names ...string) []AvailableAgent {
		out := make([]AvailableAgent, len(names))
		for i, n := range names {
			out[i] = AvailableAgent{Name: n}
		}
		return out
	}

	t.Run("filters invalid names", func(t *testing.T) {
		t.Parallel()
		agents := makeNames("ValidAgent", "also-valid", "bad agent!", "good.name", "no spaces")
		result := validateAvailableAgents(agents)
		if len(result) != 3 {
			t.Fatalf("expected 3 valid agents, got %d: %v", len(result), result)
		}
	})

	t.Run("filters too long names", func(t *testing.T) {
		t.Parallel()
		agents := makeNames("ok", strings.Repeat("a", maxAgentNameLen+1))
		result := validateAvailableAgents(agents)
		if len(result) != 1 {
			t.Fatalf("expected 1 valid agent, got %d", len(result))
		}
	})

	t.Run("caps at max agents", func(t *testing.T) {
		t.Parallel()
		agents := make([]AvailableAgent, maxAgents+10)
		for i := range agents {
			agents[i] = AvailableAgent{Name: "agent"}
		}
		result := validateAvailableAgents(agents)
		if len(result) != maxAgents {
			t.Errorf("expected %d, got %d", maxAgents, len(result))
		}
	})

	t.Run("clamps display name and description", func(t *testing.T) {
		t.Parallel()
		agents := []AvailableAgent{{
			Name:        "RichAgent",
			DisplayName: strings.Repeat("D", maxAgentDisplayLen+10),
			Description: strings.Repeat("E", maxAgentDescriptionLen+10),
		}}
		result := validateAvailableAgents(agents)
		if len(result) != 1 {
			t.Fatalf("expected 1 agent, got %d", len(result))
		}
		if len(result[0].DisplayName) != maxAgentDisplayLen {
			t.Errorf("displayName not clamped: %d", len(result[0].DisplayName))
		}
		if len(result[0].Description) != maxAgentDescriptionLen {
			t.Errorf("description not clamped: %d", len(result[0].Description))
		}
	})

	t.Run("preserves skills, drops nameless entries, clamps count", func(t *testing.T) {
		t.Parallel()
		bigSkills := make([]Skill, maxSkillsPerAgent+5)
		for i := range bigSkills {
			bigSkills[i] = Skill{Name: "skill"}
		}
		agents := []AvailableAgent{
			{
				Name: "ReportAgent",
				Skills: []Skill{
					{Name: "Generate daily report", Description: "Produces a structured daily summary"},
					{Name: ""},
					{Name: "Email report"},
				},
			},
			{
				Name:   "BulkAgent",
				Skills: bigSkills,
			},
		}
		result := validateAvailableAgents(agents)
		if len(result) != 2 {
			t.Fatalf("expected 2 agents, got %d", len(result))
		}
		if len(result[0].Skills) != 2 {
			t.Fatalf("expected 2 sanitized skills on ReportAgent, got %d: %+v", len(result[0].Skills), result[0].Skills)
		}
		if result[0].Skills[0].Name != "Generate daily report" || result[0].Skills[1].Name != "Email report" {
			t.Errorf("skill names not preserved in order: %+v", result[0].Skills)
		}
		if len(result[1].Skills) != maxSkillsPerAgent {
			t.Errorf("skill count not clamped: got %d, want %d", len(result[1].Skills), maxSkillsPerAgent)
		}
	})

	t.Run("clamps skill name and description length", func(t *testing.T) {
		t.Parallel()
		agents := []AvailableAgent{{
			Name: "AgentX",
			Skills: []Skill{{
				Name:        strings.Repeat("n", maxAgentNameLen+50),
				Description: strings.Repeat("d", maxSkillDescriptionLen+50),
			}},
		}}
		result := validateAvailableAgents(agents)
		if len(result) != 1 || len(result[0].Skills) != 1 {
			t.Fatalf("expected 1 agent with 1 skill, got %+v", result)
		}
		if len(result[0].Skills[0].Name) != maxAgentNameLen {
			t.Errorf("skill name not clamped: %d", len(result[0].Skills[0].Name))
		}
		if len(result[0].Skills[0].Description) != maxSkillDescriptionLen {
			t.Errorf("skill description not clamped: %d", len(result[0].Skills[0].Description))
		}
	})
}

func TestAvailableAgentUnmarshalJSON_WithSkills(t *testing.T) {
	t.Parallel()

	t.Run("decodes skills array", func(t *testing.T) {
		t.Parallel()
		var a AvailableAgent
		raw := `{"name":"R","displayName":"Report Agent","description":"d","skills":[{"name":"Generate report","description":"daily"},{"name":"Email summary"}]}`
		if err := a.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		if len(a.Skills) != 2 {
			t.Fatalf("expected 2 skills, got %d", len(a.Skills))
		}
		if a.Skills[0].Name != "Generate report" || a.Skills[0].Description != "daily" {
			t.Errorf("first skill = %+v", a.Skills[0])
		}
		if a.Skills[1].Name != "Email summary" || a.Skills[1].Description != "" {
			t.Errorf("second skill = %+v", a.Skills[1])
		}
	})

	t.Run("object form without skills still works", func(t *testing.T) {
		t.Parallel()
		var a AvailableAgent
		if err := a.UnmarshalJSON([]byte(`{"name":"X"}`)); err != nil {
			t.Fatal(err)
		}
		if a.Name != "X" || len(a.Skills) != 0 {
			t.Errorf("expected name=X and no skills, got %+v", a)
		}
	})

	t.Run("null skills decodes to empty", func(t *testing.T) {
		t.Parallel()
		var a AvailableAgent
		if err := a.UnmarshalJSON([]byte(`{"name":"X","skills":null}`)); err != nil {
			t.Fatal(err)
		}
		if len(a.Skills) != 0 {
			t.Errorf("expected no skills from null, got %d: %+v", len(a.Skills), a.Skills)
		}
	})

	t.Run("empty skills array decodes to empty", func(t *testing.T) {
		t.Parallel()
		var a AvailableAgent
		if err := a.UnmarshalJSON([]byte(`{"name":"X","skills":[]}`)); err != nil {
			t.Fatal(err)
		}
		if len(a.Skills) != 0 {
			t.Errorf("expected no skills from [], got %d: %+v", len(a.Skills), a.Skills)
		}
	})
}

func TestAvailableAgentUnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("accepts bare string", func(t *testing.T) {
		t.Parallel()
		var a AvailableAgent
		if err := a.UnmarshalJSON([]byte(`"MyAgent"`)); err != nil {
			t.Fatal(err)
		}
		if a.Name != "MyAgent" {
			t.Errorf("name = %q, want MyAgent", a.Name)
		}
	})

	t.Run("accepts object form", func(t *testing.T) {
		t.Parallel()
		var a AvailableAgent
		if err := a.UnmarshalJSON([]byte(`{"name":"X","displayName":"X Agent","description":"desc"}`)); err != nil {
			t.Fatal(err)
		}
		if a.Name != "X" || a.DisplayName != "X Agent" || a.Description != "desc" {
			t.Errorf("decoded wrong: %+v", a)
		}
	})
}

func TestParseAndValidateResponse(t *testing.T) {
	t.Parallel()

	t.Run("valid JSON", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"Great, I can help!","taskUpdates":{"name":"Daily Report","scheduleType":"cron"},"confidence":0.8,"readyToSave":false}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.Message != "Great, I can help!" {
			t.Errorf("unexpected message: %s", resp.Message)
		}
		if resp.TaskUpdates["name"] != "Daily Report" {
			t.Error("expected name in task_updates")
		}
		if resp.Confidence != 0.8 {
			t.Errorf("expected confidence 0.8, got %f", resp.Confidence)
		}
		if resp.ReadyToSave {
			t.Error("expected ready_to_save false")
		}
	})

	t.Run("strips code fences", func(t *testing.T) {
		t.Parallel()
		input := "```json\n{\"message\":\"stripped\",\"confidence\":0.9}\n```"
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.Message != "stripped" {
			t.Errorf("expected 'stripped', got %q", resp.Message)
		}
	})

	t.Run("invalid JSON fallback", func(t *testing.T) {
		t.Parallel()
		resp := parseAndValidateResponse("This is not JSON at all", nil, nil, nil, nil)
		if resp.Message != fallbackMessage {
			t.Errorf("expected fallback message, got %q", resp.Message)
		}
	})

	t.Run("extracts JSON from surrounding text", func(t *testing.T) {
		t.Parallel()
		input := `Here is the response: {"message":"found it","confidence":0.7} end`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.Message != "found it" {
			t.Errorf("expected 'found it', got %q", resp.Message)
		}
	})

	t.Run("replaces generic message", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"I understand","confidence":0.5}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.Message != fallbackMessage {
			t.Errorf("expected fallback, got %q", resp.Message)
		}
	})

	t.Run("clamps confidence", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"test","confidence":5.0}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.Confidence != 1.0 {
			t.Errorf("expected clamped to 1.0, got %f", resp.Confidence)
		}
	})

	t.Run("filters disallowed task_update fields", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"test","taskUpdates":{"name":"ok","evil":"bad"}}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if _, ok := resp.TaskUpdates["evil"]; ok {
			t.Error("disallowed field should be stripped")
		}
		if resp.TaskUpdates["name"] != "ok" {
			t.Error("allowed field should be kept")
		}
	})

	t.Run("validates schedule_type", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"test","taskUpdates":{"scheduleType":"bad_type"}}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if _, ok := resp.TaskUpdates["scheduleType"]; ok {
			t.Error("invalid schedule_type should be removed")
		}
	})

	t.Run("validates target_type", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"test","taskUpdates":{"targetType":"agent"}}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.TaskUpdates["targetType"] != "agent" {
			t.Errorf("valid target_type should be kept, got %v", resp.TaskUpdates["targetType"])
		}
	})

	t.Run("truncates long task_update strings", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("z", maxTaskFieldLen+50)
		input := `{"message":"test","taskUpdates":{"name":"` + long + `"}}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if name, ok := resp.TaskUpdates["name"].(string); ok && len(name) != maxTaskFieldLen {
			t.Errorf("expected truncation to %d, got %d", maxTaskFieldLen, len(name))
		}
	})

	t.Run("numeric fields cast correctly", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"test","taskUpdates":{"maxRetries":3,"timeoutSeconds":600}}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.TaskUpdates["maxRetries"] != 3 {
			t.Errorf("expected max_retries=3, got %v", resp.TaskUpdates["maxRetries"])
		}
		if resp.TaskUpdates["timeoutSeconds"] != 600 {
			t.Errorf("expected timeout_seconds=600, got %v", resp.TaskUpdates["timeoutSeconds"])
		}
	})

	t.Run("boolean enabled field", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"test","taskUpdates":{"enabled":true}}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.TaskUpdates["enabled"] != true {
			t.Errorf("expected enabled=true, got %v", resp.TaskUpdates["enabled"])
		}
	})
}

func TestParseAndValidateResponse_AgentValidation(t *testing.T) {
	t.Parallel()

	agents := []AvailableAgent{
		{Name: "Orchestrator", Description: "Default coordinator"},
		{Name: "ReportAgent", Description: "Generates reports"},
		{Name: "AnalyticsAgent", Description: "Crunches numbers"},
	}
	allowed := agentNameIndex(agents)

	t.Run("resolves agent name case-insensitively and canonicalizes", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","taskUpdates":{"targetAgentName":"orchestrator"},"readyToSave":true}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if got := resp.TaskUpdates["targetAgentName"]; got != "Orchestrator" {
			t.Errorf("targetAgentName = %v, want canonical \"Orchestrator\"", got)
		}
		if resp.InlineComponent != nil {
			t.Error("a case-only difference must not trigger the did-you-mean picker")
		}
	})

	t.Run("resolves agent display name to the canonical routing name", func(t *testing.T) {
		t.Parallel()
		dnAgents := []AvailableAgent{
			{Name: "agent_019e9445_40b1_7652_a12b_a96dae36891a", DisplayName: "Orchestrator"},
		}
		idx := agentNameIndex(dnAgents)
		input := `{"message":"ok","taskUpdates":{"targetAgentName":"Orchestrator"},"readyToSave":true}`
		resp := parseAndValidateResponse(input, dnAgents, idx, nil, nil)
		if got := resp.TaskUpdates["targetAgentName"]; got != "agent_019e9445_40b1_7652_a12b_a96dae36891a" {
			t.Errorf("targetAgentName = %v, want canonical routing name", got)
		}
		if resp.InlineComponent != nil {
			t.Error("a resolvable display name must not trigger the did-you-mean picker")
		}
	})

	t.Run("drops hallucinated agent name", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","taskUpdates":{"name":"X","targetAgentName":"GhostAgent"},"readyToSave":true}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if _, ok := resp.TaskUpdates["targetAgentName"]; ok {
			t.Errorf("hallucinated agent should be dropped, got %v", resp.TaskUpdates["targetAgentName"])
		}
		if resp.TaskUpdates["name"] != "X" {
			t.Error("other fields should survive")
		}
		if resp.ReadyToSave {
			t.Error("readyToSave should be forced false when picker is surfaced")
		}
	})

	t.Run("synthesizes did-you-mean picker on hallucination", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","taskUpdates":{"targetAgentName":"Orchestratr"}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.InlineComponent == nil {
			t.Fatal("expected agent_picker InlineComponent")
		}
		if resp.InlineComponent.Type != "agent_picker" {
			t.Errorf("type = %q, want agent_picker", resp.InlineComponent.Type)
		}
		if len(resp.InlineComponent.Suggestions) == 0 {
			t.Fatal("expected suggestions")
		}
		if resp.InlineComponent.Suggestions[0].Name != "Orchestrator" {
			t.Errorf("top suggestion = %q, want Orchestrator", resp.InlineComponent.Suggestions[0].Name)
		}
	})

	t.Run("keeps valid agent name", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","taskUpdates":{"targetAgentName":"ReportAgent"}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.TaskUpdates["targetAgentName"] != "ReportAgent" {
			t.Errorf("valid agent should be kept, got %v", resp.TaskUpdates["targetAgentName"])
		}
		if resp.InlineComponent != nil {
			t.Errorf("no picker expected when agent is valid, got %+v", resp.InlineComponent)
		}
	})

	t.Run("suppresses picker when form already has target and LLM isn't changing it", func(t *testing.T) {
		t.Parallel()
		current := map[string]any{"targetAgentName": "ReportAgent"}
		input := `{"message":"ok","taskUpdates":{},"inlineComponent":{"type":"agent_picker","prompt":"pick one","suggestions":[{"name":"Orchestrator"}],"allowOther":true}}`
		resp := parseAndValidateResponse(input, agents, allowed, current, nil)
		if resp.InlineComponent != nil {
			t.Errorf("picker should be suppressed, got %+v", resp.InlineComponent)
		}
	})

	t.Run("allows picker when LLM is changing target", func(t *testing.T) {
		t.Parallel()
		current := map[string]any{"targetAgentName": "ReportAgent"}
		input := `{"message":"ok","taskUpdates":{"targetAgentName":"GhostAgent"}}`
		resp := parseAndValidateResponse(input, agents, allowed, current, nil)
		if resp.InlineComponent == nil {
			t.Error("picker should fire when LLM tries to change target to invalid name")
		}
	})

	t.Run("sanitizes LLM-emitted inlineComponent suggestions", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","inlineComponent":{"type":"agent_picker","prompt":"pick","suggestions":[{"name":"ReportAgent","reason":"good"},{"name":"GhostAgent","reason":"bad"}],"allowOther":false}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.InlineComponent == nil {
			t.Fatal("expected InlineComponent")
		}
		if len(resp.InlineComponent.Suggestions) != 1 {
			t.Fatalf("expected 1 suggestion, got %d", len(resp.InlineComponent.Suggestions))
		}
		if resp.InlineComponent.Suggestions[0].Name != "ReportAgent" {
			t.Errorf("expected ReportAgent, got %s", resp.InlineComponent.Suggestions[0].Name)
		}
	})

	t.Run("canonicalizes case-mismatched inlineComponent suggestions", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","inlineComponent":{"type":"agent_picker","prompt":"pick","suggestions":[{"name":"reportagent","reason":"good"}],"allowOther":false}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.InlineComponent == nil || len(resp.InlineComponent.Suggestions) != 1 {
			t.Fatalf("expected 1 suggestion, got %+v", resp.InlineComponent)
		}
		if resp.InlineComponent.Suggestions[0].Name != "ReportAgent" {
			t.Errorf("suggestion = %q, want canonical \"ReportAgent\"", resp.InlineComponent.Suggestions[0].Name)
		}
	})

	t.Run("drops targetAgentName from updates when picker is surfaced", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","taskUpdates":{"name":"X","targetAgentName":"GhostAgent"}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if _, ok := resp.TaskUpdates["targetAgentName"]; ok {
			t.Error("targetAgentName should be cleared when picker is surfaced")
		}
		if resp.TaskUpdates["name"] != "X" {
			t.Error("non-agent fields should survive")
		}
	})

	t.Run("no allowlist means no enforcement", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"ok","taskUpdates":{"targetAgentName":"WhateverAgent"}}`
		resp := parseAndValidateResponse(input, nil, nil, nil, nil)
		if resp.TaskUpdates["targetAgentName"] != "WhateverAgent" {
			t.Errorf("without allowlist, agent name should pass through, got %v", resp.TaskUpdates["targetAgentName"])
		}
		if resp.InlineComponent != nil {
			t.Errorf("no picker without agents, got %+v", resp.InlineComponent)
		}
	})

	t.Run("prose-offer backstop synthesizes picker when LLM forgets inlineComponent", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"For the agent, here are some good options. Which one fits best?","taskUpdates":{"name":"X"}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.InlineComponent == nil {
			t.Fatal("expected backstop agent_picker when message offers a choice without a component")
		}
		if resp.InlineComponent.Type != "agent_picker" {
			t.Errorf("type = %q, want agent_picker", resp.InlineComponent.Type)
		}
		if len(resp.InlineComponent.Suggestions) == 0 {
			t.Fatal("expected suggestions in synthesized picker")
		}
		if resp.ReadyToSave {
			t.Error("readyToSave should be forced false when a picker is surfaced")
		}
	})

	t.Run("prose-offer backstop does not fire when an agent is already on the form", func(t *testing.T) {
		t.Parallel()
		current := map[string]any{"targetAgentName": "ReportAgent"}
		input := `{"message":"For the agent, here are some good options. Which one fits best?","taskUpdates":{}}`
		resp := parseAndValidateResponse(input, agents, allowed, current, nil)
		if resp.InlineComponent != nil {
			t.Errorf("backstop should not override an existing form selection, got %+v", resp.InlineComponent)
		}
	})

	t.Run("prose-offer backstop does not fire on non-agent prose", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"Here are some schedule options. Which one fits best?","taskUpdates":{"name":"X"}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.InlineComponent != nil {
			t.Errorf("no picker expected for non-agent prose, got %+v", resp.InlineComponent)
		}
	})

	t.Run("prose-offer backstop does not fire on agent mention with only broad cues", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"The agent will read these configuration options; here are the defaults.","taskUpdates":{"name":"X"}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.InlineComponent != nil {
			t.Errorf("no picker expected when message merely mentions an agent, got %+v", resp.InlineComponent)
		}
	})

	t.Run("prose-offer backstop does not fire on agent substring words", func(t *testing.T) {
		t.Parallel()
		input := `{"message":"Your management options here are flexible — which one fits best?","taskUpdates":{"name":"X"}}`
		resp := parseAndValidateResponse(input, agents, allowed, nil, nil)
		if resp.InlineComponent != nil {
			t.Errorf("no picker expected for substring-only agent match, got %+v", resp.InlineComponent)
		}
	})
}

func TestCoerceIntervalExpression(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"5m", "5m", true},
		{"30s", "30s", true},
		{"5 minutes", "5m", true},
		{"5 min", "5m", true},
		{"every 5 minutes", "5m", true},
		{"2 hrs", "2h", true},
		{"1 hour", "1h", true},
		{"7 days", "7d", true},
		{"  10 SEC  ", "10s", true},
		{"*/5 * * * *", "", false},
		{"0 9 * * *", "", false},
		{"", "", false},
		{"soon", "", false},
	}
	for _, c := range cases {
		got, ok := coerceIntervalExpression(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("coerceIntervalExpression(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestValidateTaskUpdates_ScheduleNormalization(t *testing.T) {
	t.Parallel()

	t.Run("uppercase scheduleType normalized", func(t *testing.T) {
		got := validateTaskUpdates(map[string]any{"scheduleType": "INTERVAL"}, nil, nil)
		if got["scheduleType"] != "interval" {
			t.Errorf("scheduleType = %v, want interval", got["scheduleType"])
		}
	})

	t.Run("interval natural language coerced", func(t *testing.T) {
		got := validateTaskUpdates(map[string]any{"scheduleType": "interval", "scheduleExpression": "5 minutes"}, nil, nil)
		if got["scheduleExpression"] != "5m" {
			t.Errorf("scheduleExpression = %v, want 5m", got["scheduleExpression"])
		}
	})

	t.Run("cron-shaped interval expression dropped", func(t *testing.T) {
		got := validateTaskUpdates(map[string]any{"scheduleType": "interval", "scheduleExpression": "*/5 * * * *"}, nil, nil)
		if _, present := got["scheduleExpression"]; present {
			t.Errorf("expected unparseable interval expression to be dropped, got %v", got["scheduleExpression"])
		}
		if got["scheduleType"] != "interval" {
			t.Errorf("scheduleType = %v, want interval", got["scheduleType"])
		}
	})

	t.Run("cron expression passes through untouched", func(t *testing.T) {
		got := validateTaskUpdates(map[string]any{"scheduleType": "cron", "scheduleExpression": "0 9 * * *"}, nil, nil)
		if got["scheduleExpression"] != "0 9 * * *" {
			t.Errorf("scheduleExpression = %v, want unchanged cron", got["scheduleExpression"])
		}
	})

	t.Run("unknown scheduleType dropped", func(t *testing.T) {
		got := validateTaskUpdates(map[string]any{"scheduleType": "weekly"}, nil, nil)
		if _, present := got["scheduleType"]; present {
			t.Errorf("expected unknown scheduleType to be dropped, got %v", got["scheduleType"])
		}
	})

	t.Run("invalid scheduleType drops a non-interval expression too", func(t *testing.T) {
		got := validateTaskUpdates(map[string]any{"scheduleType": "weekly", "scheduleExpression": "0 9 * * *"}, nil, nil)
		if _, present := got["scheduleExpression"]; present {
			t.Errorf("expected expression dropped when type is invalid, got %v", got["scheduleExpression"])
		}
	})

	t.Run("non-string scheduleExpression dropped", func(t *testing.T) {
		got := validateTaskUpdates(map[string]any{"scheduleType": "interval", "scheduleExpression": 123.0}, nil, nil)
		if _, present := got["scheduleExpression"]; present {
			t.Errorf("expected non-string scheduleExpression to be dropped, got %v", got["scheduleExpression"])
		}
	})

	t.Run("over-long scheduleExpression truncated", func(t *testing.T) {
		long := strings.Repeat("x", maxTaskFieldLen+50)
		got := validateTaskUpdates(map[string]any{"scheduleType": "cron", "scheduleExpression": long}, nil, nil)
		s, _ := got["scheduleExpression"].(string)
		if len(s) != maxTaskFieldLen {
			t.Errorf("scheduleExpression len = %d, want %d", len(s), maxTaskFieldLen)
		}
	})

	t.Run("expression without type: interval coerced, cron passed through", func(t *testing.T) {
		coerced := validateTaskUpdates(map[string]any{"scheduleExpression": "10 minutes"}, nil, nil)
		if coerced["scheduleExpression"] != "10m" {
			t.Errorf("interval-shaped expression without type = %v, want 10m", coerced["scheduleExpression"])
		}
		passthrough := validateTaskUpdates(map[string]any{"scheduleExpression": "0 9 * * *"}, nil, nil)
		if passthrough["scheduleExpression"] != "0 9 * * *" {
			t.Errorf("cron expression without type = %v, want unchanged", passthrough["scheduleExpression"])
		}
	})
}

func TestIsGenericMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		msg  string
		want bool
	}{
		{"", true},
		{"   ", true},
		{"I understand", true},
		{"okay", true},
		{"Sure", true},
		{"This is a real response", false},
	}
	for _, tt := range tests {
		if got := isGenericMessage(tt.msg); got != tt.want {
			t.Errorf("isGenericMessage(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestAgentNameIndex(t *testing.T) {
	t.Parallel()
	agents := []AvailableAgent{
		{Name: "agent_uuid_1", DisplayName: "Orchestrator"},
		{Name: "ReportAgent"},
		{Name: "agent_uuid_2", DisplayName: "Dup"},
		{Name: "agent_uuid_3", DisplayName: "Dup"},
		{Name: "agent_uuid_4", DisplayName: "ReportAgent"},
	}
	idx := agentNameIndex(agents)

	if idx["agent_uuid_1"] != "agent_uuid_1" {
		t.Errorf("routing name should resolve to itself, got %q", idx["agent_uuid_1"])
	}
	if idx["reportagent"] != "ReportAgent" {
		t.Errorf("routing name (case-insensitive) should win, got %q", idx["reportagent"])
	}
	if idx["orchestrator"] != "agent_uuid_1" {
		t.Errorf("unique display name should resolve to routing name, got %q", idx["orchestrator"])
	}
	if got, ok := idx["dup"]; ok {
		t.Errorf("ambiguous display name must not resolve, got %q", got)
	}
}
