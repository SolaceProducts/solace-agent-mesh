// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

func TestToolSearchWireFor(t *testing.T) {
	cases := []struct {
		provider schemas.ModelProvider
		model    string
		want     toolSearchWire
	}{
		{schemas.Anthropic, "claude-sonnet-5", toolSearchAnthropic},
		{schemas.Vertex, "claude-sonnet-5", toolSearchAnthropic},
		{schemas.Bedrock, "eu.anthropic.claude-sonnet-5-5", toolSearchAnthropic},
		{schemas.Vertex, "gemini-2.5-pro", toolSearchNone},
		{schemas.Bedrock, "meta.llama3-70b-instruct-v1:0", toolSearchNone},
		{schemas.OpenAI, "gpt-5.6-terra", toolSearchOpenAI},
		{schemas.Azure, "gpt-5.6-luna", toolSearchOpenAI},
		{schemas.OpenAI, "gpt-4o", toolSearchNone},
	}
	for _, tc := range cases {
		if got := toolSearchWireFor(tc.provider, tc.model); got != tc.want {
			t.Errorf("toolSearchWireFor(%s, %s) = %d, want %d", tc.provider, tc.model, got, tc.want)
		}
	}
}

func TestDecideToolSearch(t *testing.T) {
	small := []ToolDefinition{deferredTool("get_jira_issue", 1)}
	large := largeDeferredCatalog()
	cached := objectTool("_notify_artifact_save")
	cached.DeferLoading = true
	cached.CacheControl = &CacheControl{Type: "ephemeral"}

	cases := map[string]struct {
		tools []ToolDefinition
		want  string
	}{
		"nothing deferrable":                  {[]ToolDefinition{objectTool("load_artifact")}, toolSearchOutcomeNoDeferrable},
		"cache-marked tool is not deferrable": {[]ToolDefinition{cached}, toolSearchOutcomeNoDeferrable},
		"small catalog":                       {small, toolSearchOutcomeBelowThreshold},
		"large catalog":                       {large, toolSearchOutcomeDeferred},
	}
	c := &BifrostClient{}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := c.decideToolSearch(&ChatRequest{Tools: tc.tools}, schemas.Anthropic, toolSearchModel)
			if d.outcome != tc.want {
				t.Errorf("outcome = %q, want %q (deferrable tokens %d)", d.outcome, tc.want, d.deferrableToks)
			}
		})
	}

	t.Run("GPT defers only once on the Responses API", func(t *testing.T) {
		c := &BifrostClient{}
		if d := c.decideToolSearch(&ChatRequest{Tools: large}, schemas.OpenAI, "gpt-5.6-terra"); d.outcome != toolSearchOutcomeChatAPI {
			t.Errorf("on chat completions: outcome = %q, want %q", d.outcome, toolSearchOutcomeChatAPI)
		}
		c.responsesModels.Store("gpt-5.6-terra", struct{}{})
		if d := c.decideToolSearch(&ChatRequest{Tools: large}, schemas.OpenAI, "gpt-5.6-terra"); d.outcome != toolSearchOutcomeDeferred {
			t.Errorf("on the Responses API: outcome = %q, want %q", d.outcome, toolSearchOutcomeDeferred)
		}
	})

	t.Run("a model whose provider rejected tool search", func(t *testing.T) {
		c := &BifrostClient{}
		c.toolSearchRejected.Store(toolSearchModel, struct{}{})
		d := c.decideToolSearch(&ChatRequest{Tools: large}, schemas.Anthropic, toolSearchModel)
		if d.outcome != toolSearchOutcomeRejected {
			t.Errorf("outcome = %q, want %q", d.outcome, toolSearchOutcomeRejected)
		}
	})
}

func largeDeferredCatalog() []ToolDefinition {
	var out []ToolDefinition
	for i := 0; approxCatalogTokens(out) < 2*toolSearchMinTokens; i++ {
		out = append(out, deferredTool("connector_tool", i))
	}
	return out
}

func approxCatalogTokens(tools []ToolDefinition) int {
	n := 0
	for _, t := range tools {
		n += approxDefinitionTokens(t)
	}
	return n
}

func largeDeferredTool(name string) ToolDefinition {
	t := objectTool(name)
	t.Description += strings.Repeat(" Returns the record with every field.", toolSearchMinTokens/8)
	t.DeferLoading = true
	return t
}

func deferredTool(prefix string, i int) ToolDefinition {
	t := objectTool(prefix + "_" + string(rune('a'+i%26)) + string(rune('a'+i/26%26)))
	t.Description = "Looks up a record in a connected system and returns it with owners, timestamps, status history, related links and every custom field the system stores for it."
	t.DeferLoading = true
	return t
}
