// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"
)

const toolSearchMinTokens = 20000

const deferredToolsHeading = "\n\n## Tools to load on demand\nSearch your tools by name or purpose to load one of these before calling it: "

type toolSearchWire int

const (
	toolSearchNone toolSearchWire = iota
	toolSearchAnthropic
	toolSearchOpenAI
)

const (
	toolSearchOutcomeDeferred       = "deferred"
	toolSearchOutcomeNoDeferrable   = "no_deferrable_tools"
	toolSearchOutcomeUnsupported    = "unsupported_model"
	toolSearchOutcomeChatAPI        = "chat_completions_api"
	toolSearchOutcomeRejected       = "rejected_by_provider"
	toolSearchOutcomeBelowThreshold = "below_threshold"
)

type toolSearchDecision struct {
	wire           toolSearchWire
	outcome        string
	deferrable     int
	deferrableToks int
}

func (c *BifrostClient) decideToolSearch(req *ChatRequest, provider schemas.ModelProvider, model string) toolSearchDecision {
	var d toolSearchDecision
	for _, t := range req.Tools {
		if isDeferrable(t) {
			d.deferrable++
			d.deferrableToks += approxDefinitionTokens(t)
		}
	}
	if d.deferrable == 0 {
		d.outcome = toolSearchOutcomeNoDeferrable
		return d
	}
	wire := toolSearchWireFor(provider, model)
	_, rejected := c.toolSearchRejected.Load(model)
	switch {
	case wire == toolSearchNone:
		d.outcome = toolSearchOutcomeUnsupported
	case wire == toolSearchOpenAI && !c.usesResponsesAPI(model):
		d.outcome = toolSearchOutcomeChatAPI
	case rejected:
		d.outcome = toolSearchOutcomeRejected
	case d.deferrableToks < toolSearchMinTokens:
		d.outcome = toolSearchOutcomeBelowThreshold
	default:
		d.wire, d.outcome = wire, toolSearchOutcomeDeferred
	}
	return d
}

func isDeferrable(t ToolDefinition) bool {
	return t.DeferLoading && t.CacheControl == nil
}

func withDeferredToolNames(msgs []ChatMessage, tools []ToolDefinition, d toolSearchDecision) []ChatMessage {
	if d.wire == toolSearchNone {
		return msgs
	}
	var names []string
	for _, t := range tools {
		if isDeferrable(t) {
			names = append(names, t.Name)
		}
	}
	section := deferredToolsHeading + strings.Join(names, ", ") + "."
	out := slices.Clone(msgs)
	for i := range out {
		if out[i].Role == "system" {
			out[i].Content += section
			return out
		}
	}
	return append([]ChatMessage{{Role: "system", Content: strings.TrimLeft(section, "\n")}}, out...)
}

func toolSearchWireFor(provider schemas.ModelProvider, model string) toolSearchWire {
	claude := provider == schemas.Anthropic || IsClaudeModel(model)
	switch {
	case claude && anthropic.SupportsToolSearch(provider, model):
		return toolSearchAnthropic
	case !claude && openai.SupportsToolSearch(provider, model):
		return toolSearchOpenAI
	default:
		return toolSearchNone
	}
}

func approxDefinitionTokens(t ToolDefinition) int {
	size := len(t.Name) + len(t.Description)
	if params, err := json.Marshal(t.Parameters); err == nil {
		size += len(params)
	}
	return size / 4
}

func applyToolSearch(tools []schemas.ChatTool, d toolSearchDecision) []schemas.ChatTool {
	if d.wire == toolSearchNone {
		for i := range tools {
			tools[i].DeferLoading = nil
		}
		return tools
	}
	for i := range tools {
		if tools[i].CacheControl != nil {
			tools[i].DeferLoading = nil
		}
	}
	if d.wire == toolSearchOpenAI {
		return tools
	}
	search := schemas.ChatTool{
		Type: schemas.ChatToolType(anthropic.AnthropicToolTypeToolSearchBM2520251119),
		Name: string(anthropic.AnthropicToolNameToolSearchBM25),
	}
	return append([]schemas.ChatTool{search}, tools...)
}

func (c *BifrostClient) logToolSearchDecision(d toolSearchDecision, model string) {
	if d.outcome == toolSearchOutcomeNoDeferrable {
		return
	}
	c.logger.Debug("tool search decision",
		"model", model, "outcome", d.outcome,
		"deferrableTools", d.deferrable, "deferrableTokens", d.deferrableToks)
	if d.outcome != toolSearchOutcomeDeferred {
		return
	}
	if _, seen := c.toolSearchLogged.LoadOrStore(model, struct{}{}); !seen {
		c.logger.Info("tool search on for this model: tools load on demand",
			"provider", c.cfg.Provider, "model", model,
			"deferrableTools", d.deferrable, "deferrableTokens", d.deferrableToks)
	}
}

func (c *BifrostClient) recordToolSearchRejection(bfReq *schemas.BifrostChatRequest, err error) bool {
	if bfReq.Params == nil || !slices.ContainsFunc(bfReq.Params.Tools, isDeferredChatTool) || !isToolSearchRejection(err) {
		return false
	}
	c.toolSearchRejected.Store(bfReq.Model, struct{}{})
	c.logger.Warn("provider rejected tool search; sending every tool in full to this model for the client's lifetime",
		"provider", c.cfg.Provider, "model", bfReq.Model)
	return true
}

func isDeferredChatTool(t schemas.ChatTool) bool {
	return t.DeferLoading != nil && *t.DeferLoading
}

func isToolSearchRejection(err error) bool {
	llmErr, ok := errors.AsType[*Error](err)
	if !ok || llmErr.StatusCode != http.StatusBadRequest {
		return false
	}
	msg := strings.ToLower(llmErr.Message)
	return strings.Contains(msg, "defer_loading") || strings.Contains(msg, "tool_search") || strings.Contains(msg, "tool search")
}

func carryDeferLoadingToResponses(chatTools []schemas.ChatTool, rr *schemas.BifrostResponsesRequest) {
	if rr.Params == nil {
		return
	}
	deferred := map[string]bool{}
	for _, t := range chatTools {
		if isDeferredChatTool(t) && t.Function != nil {
			deferred[t.Function.Name] = true
		}
	}
	if len(deferred) == 0 {
		return
	}
	for i := range rr.Params.Tools {
		if name := rr.Params.Tools[i].Name; name != nil && deferred[*name] {
			rr.Params.Tools[i].DeferLoading = schemas.Ptr(true)
		}
	}
}
