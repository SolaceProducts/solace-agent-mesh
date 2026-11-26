// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/core/providers/openai"
	providerutils "github.com/maximhq/bifrost/core/providers/utils"
	"github.com/maximhq/bifrost/core/schemas"
)

const (
	extraParamReasoningEffort     = "reasoning_effort"
	extraParamMaxTokens           = "max_tokens"
	extraParamMaxCompletionTokens = "max_completion_tokens"
	extraParamResponseFormat      = "response_format"
	extraParamVerbosity           = "verbosity"
	extraParamToolChoice          = "tool_choice"
	extraParamReasoning           = "reasoning"

	finishReasonStop          = string(schemas.BifrostFinishReasonStop)
	finishReasonLength        = string(schemas.BifrostFinishReasonLength)
	finishReasonToolCalls     = string(schemas.BifrostFinishReasonToolCalls)
	finishReasonContentFilter = "content_filter"

	responsesAPIPath = "v1/responses"
)

func (c *BifrostClient) usesResponsesAPI(model string) bool {
	_, switched := c.responsesModels.Load(model)
	return switched
}

func (c *BifrostClient) redirectedToResponsesAPI(err error, model string) bool {
	_, pathReplaced := c.chatURLPath(model)
	return c.cfg.Provider == string(schemas.OpenAI) && !pathReplaced && isResponsesAPIRedirect(err)
}

func isResponsesAPIRedirect(err error) bool {
	llmErr, ok := errors.AsType[*Error](err)
	return ok && llmErr.StatusCode == http.StatusBadRequest && strings.Contains(llmErr.Message, responsesAPIPath)
}

func (c *BifrostClient) expectsResponsesRedirect(model string) func(*Error) bool {
	return func(err *Error) bool { return c.redirectedToResponsesAPI(err, model) }
}

func attemptFailureLevel(err *Error, expected func(*Error) bool) slog.Level {
	if expected != nil && expected(err) {
		return slog.LevelDebug
	}
	return slog.LevelWarn
}

func (c *BifrostClient) switchToResponsesAPI(model string) {
	if _, known := c.responsesModels.LoadOrStore(model, struct{}{}); !known {
		c.logger.Info("OpenAI requires the Responses API for this model; using it for the client's lifetime",
			"provider", c.cfg.Provider, "model", model)
	}
}

func (c *BifrostClient) completionAfterChatFailure(ctx context.Context, bfReq *schemas.BifrostChatRequest, err error) (*ChatResponse, error) {
	if !c.redirectedToResponsesAPI(err, bfReq.Model) {
		return nil, err
	}
	return c.responsesCompletion(ctx, bfReq, attemptsOf(err))
}

func (c *BifrostClient) streamAfterChatFailure(ctx context.Context, bfReq *schemas.BifrostChatRequest, err error) (<-chan StreamChunk, error) {
	if !c.redirectedToResponsesAPI(err, bfReq.Model) {
		return nil, err
	}
	return c.responsesCompletionStream(ctx, bfReq, attemptsOf(err))
}

func attemptsOf(err error) int {
	if llmErr, ok := errors.AsType[*Error](err); ok {
		return llmErr.Attempts
	}
	return 0
}

func (c *BifrostClient) buildResponsesRequest(chatReq *schemas.BifrostChatRequest) *schemas.BifrostResponsesRequest {
	c.logger.Debug("sending OpenAI request to the Responses API", "model", chatReq.Model)
	split := *chatReq
	split.Input = splitAssistantTextFromToolCalls(chatReq.Input)
	split.Provider = schemas.ModelProvider(c.cfg.Provider)
	rr := split.ToResponsesRequest()
	if chatReq.Params != nil {
		carryDeferLoadingToResponses(chatReq.Params.Tools, rr)
	}
	for i := range rr.Input {
		rr.Input[i].ID = nil
		normalizeFunctionCallOutput(&rr.Input[i])
	}
	if rr.Params == nil {
		rr.Params = &schemas.ResponsesParameters{}
	}
	rr.Params.Store = schemas.Ptr(false)
	dropped := applyChatExtraParams(rr.Params)
	if len(dropped) > 0 {
		c.logger.Debug("dropping extra params the Responses API cannot take", "params", dropped)
	}
	c.warnStoredParamsOffResponses(dropped)
	carryReasoningPastBifrost(rr)
	return rr
}

func carryReasoningPastBifrost(rr *schemas.BifrostResponsesRequest) {
	reasoning := rr.Params.Reasoning
	if reasoning == nil || openai.IsOpenAIReasoningModel(rr.Model) {
		return
	}
	raw, isMap := rr.Params.ExtraParams[extraParamReasoning].(map[string]any)
	if _, set := rr.Params.ExtraParams[extraParamReasoning]; set && !isMap {
		return
	}
	if _, rawEffort := raw["effort"]; rawEffort {
		return
	}
	effort := aliasReasoningEffort(reasoning, deref(rr.Params.MaxOutputTokens))
	if effort == "" {
		return
	}
	merged := maps.Clone(raw)
	if merged == nil {
		merged = map[string]any{}
	}
	merged["effort"] = effort
	if rr.Params.ExtraParams == nil {
		rr.Params.ExtraParams = map[string]any{}
	}
	rr.Params.ExtraParams[extraParamReasoning] = merged
}

func aliasReasoningEffort(reasoning *schemas.ResponsesParametersReasoning, maxOutput int) string {
	effort := deref(reasoning.Effort)
	if effort == "" && reasoning.MaxTokens != nil {
		effort = providerutils.GetReasoningEffortFromBudgetTokens(*reasoning.MaxTokens, openai.MinReasoningMaxTokens,
			cmp.Or(maxOutput, openai.DefaultCompletionMaxTokens))
	}
	switch effort {
	case "minimal":
		return "low"
	case "xhigh", "max":
		return "high"
	case "low", "medium", "high":
		return effort
	}
	return ""
}

func splitAssistantTextFromToolCalls(msgs []schemas.ChatMessage) []schemas.ChatMessage {
	out := make([]schemas.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != schemas.ChatMessageRoleAssistant || m.ChatAssistantMessage == nil ||
			len(m.ToolCalls) == 0 || !hasChatContent(m.Content) {
			out = append(out, m)
			continue
		}
		textOnly := *m.ChatAssistantMessage
		textOnly.ToolCalls = nil
		text := m
		text.ChatAssistantMessage = &textOnly
		calls := schemas.ChatMessage{
			Role:                 m.Role,
			ChatAssistantMessage: &schemas.ChatAssistantMessage{ToolCalls: m.ToolCalls},
		}
		out = append(out, text, calls)
	}
	return out
}

func hasChatContent(c *schemas.ChatMessageContent) bool {
	return c != nil && (deref(c.ContentStr) != "" || len(c.ContentBlocks) > 0)
}

func applyChatExtraParams(params *schemas.ResponsesParameters) (dropped []string) {
	extra := params.ExtraParams
	params.ExtraParams = nil
	for _, key := range slices.Sorted(maps.Keys(extra)) {
		switch translateChatExtraParam(params, extra, key) {
		case extraParamKept:
			if params.ExtraParams == nil {
				params.ExtraParams = map[string]any{}
			}
			params.ExtraParams[key] = extra[key]
		case extraParamDropped:
			dropped = append(dropped, key)
		}
	}
	return dropped
}

type extraParamOutcome int

const (
	extraParamKept extraParamOutcome = iota
	extraParamTranslated
	extraParamDropped
)

func translateChatExtraParam(params *schemas.ResponsesParameters, extra map[string]any, key string) extraParamOutcome {
	value := extra[key]
	switch key {
	case extraParamReasoningEffort:
		raw, _ := value.(string)
		effort := NormalizeReasoningEffort(raw)
		if effort == "" {
			return extraParamDropped
		}
		params.Reasoning = &schemas.ResponsesParametersReasoning{Effort: &effort}
	case extraParamMaxTokens, extraParamMaxCompletionTokens:
		if _, preferred := IntParam(extra[extraParamMaxCompletionTokens]); preferred && key == extraParamMaxTokens {
			return extraParamTranslated
		}
		n, ok := IntParam(value)
		if !ok {
			return extraParamDropped
		}
		params.MaxOutputTokens = &n
	case extraParamResponseFormat:
		format := schemas.ResponsesTextConfigFormatFromChatResponseFormat(&value)
		if format == nil {
			return extraParamDropped
		}
		textConfig(params).Format = format
	case extraParamVerbosity:
		verbosity, _ := value.(string)
		if verbosity == "" {
			return extraParamDropped
		}
		textConfig(params).Verbosity = &verbosity
	case extraParamToolChoice:
		return translateToolChoice(params, value)
	default:
		if isChatOnlyParam(key) {
			return extraParamDropped
		}
		return extraParamKept
	}
	return extraParamTranslated
}

func translateToolChoice(params *schemas.ResponsesParameters, value any) extraParamOutcome {
	if _, isString := value.(string); isString {
		return extraParamKept
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return extraParamDropped
	}
	var choice schemas.ChatToolChoice
	if err := json.Unmarshal(raw, &choice); err != nil || !isCompleteToolChoice(choice.ChatToolChoiceStruct) {
		return extraParamDropped
	}
	params.ToolChoice = choice.ToResponsesToolChoice()
	return extraParamTranslated
}

func isCompleteToolChoice(choice *schemas.ChatToolChoiceStruct) bool {
	if choice == nil {
		return false
	}
	switch choice.Type {
	case schemas.ChatToolChoiceTypeNone, schemas.ChatToolChoiceTypeAuto, schemas.ChatToolChoiceTypeRequired:
		return true
	case schemas.ChatToolChoiceTypeFunction:
		return choice.Function != nil && choice.Function.Name != ""
	case schemas.ChatToolChoiceTypeAllowedTools:
		return choice.AllowedTools != nil && len(choice.AllowedTools.Tools) > 0 &&
			!slices.ContainsFunc(choice.AllowedTools.Tools, isUnnamedAllowedTool)
	case schemas.ChatToolChoiceTypeCustom:
		return choice.Custom != nil && choice.Custom.Name != ""
	}
	return false
}

func isUnnamedAllowedTool(tool schemas.ChatToolChoiceAllowedToolsTool) bool {
	return tool.Type != string(schemas.ChatToolChoiceTypeFunction) || tool.Function.Name == ""
}

func isChatOnlyParam(key string) bool {
	switch key {
	case "frequency_penalty", "presence_penalty", "seed", "logit_bias", "logprobs", "n", "stop", "stream_options",
		"modalities", "audio", "prediction", "web_search_options", "functions", "function_call":
		return true
	}
	return false
}

func textConfig(params *schemas.ResponsesParameters) *schemas.ResponsesTextConfig {
	if params.Text == nil {
		params.Text = &schemas.ResponsesTextConfig{}
	}
	return params.Text
}

func normalizeFunctionCallOutput(item *schemas.ResponsesMessage) {
	if deref(item.Type) != schemas.ResponsesMessageTypeFunctionCallOutput ||
		item.ResponsesToolMessage == nil || item.Output == nil ||
		len(item.Output.ResponsesFunctionToolCallOutputBlocks) == 0 {
		return
	}
	blocks := slices.Clone(item.Output.ResponsesFunctionToolCallOutputBlocks)
	var text strings.Builder
	textOnly := true
	for i := range blocks {
		blocks[i].Type = responsesInputBlockType(blocks[i].Type)
		if blocks[i].Type == schemas.ResponsesInputMessageContentBlockTypeText {
			text.WriteString(deref(blocks[i].Text))
		} else {
			textOnly = false
		}
	}

	toolMsg := *item.ResponsesToolMessage
	if textOnly {
		toolMsg.Output = &schemas.ResponsesToolMessageOutputStruct{ResponsesToolCallOutputStr: schemas.Ptr(text.String())}
	} else {
		toolMsg.Output = &schemas.ResponsesToolMessageOutputStruct{ResponsesFunctionToolCallOutputBlocks: blocks}
	}
	item.ResponsesToolMessage = &toolMsg
}

func responsesInputBlockType(t schemas.ResponsesMessageContentBlockType) schemas.ResponsesMessageContentBlockType {
	switch t {
	case "text", schemas.ResponsesOutputMessageContentTypeText:
		return schemas.ResponsesInputMessageContentBlockTypeText
	case "image_url":
		return schemas.ResponsesInputMessageContentBlockTypeImage
	case "file":
		return schemas.ResponsesInputMessageContentBlockTypeFile
	}
	return t
}

func (c *BifrostClient) responsesBifrostContext(ctx context.Context, rr *schemas.BifrostResponsesRequest) (*schemas.BifrostContext, *schemas.BifrostError) {
	bfCtx, authErr := c.bifrostContext(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if len(rr.Params.ExtraParams) > 0 {
		bfCtx.SetValue(schemas.BifrostContextKeyPassthroughExtraParams, true)
	}
	return bfCtx, nil
}

func (c *BifrostClient) responsesCompletion(ctx context.Context, chatReq *schemas.BifrostChatRequest, priorAttempts int) (*ChatResponse, error) {
	rr := c.buildResponsesRequest(chatReq)

	resp, attempts, err := callWithRetries(ctx, c, "responses", nil,
		func() (*schemas.BifrostResponsesResponse, *schemas.BifrostError) {
			reqCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			bfCtx, authErr := c.responsesBifrostContext(reqCtx, rr)
			if authErr != nil {
				return nil, authErr
			}
			resp, bfErr := c.bf.ResponsesRequest(bfCtx, rr)
			if bfErr == nil && resp != nil && resp.Error != nil {
				return nil, inBandResponsesError(c.cfg.Provider, resp.Error)
			}
			return resp, bfErr
		})
	if err != nil {
		return nil, withPriorAttempts(err, priorAttempts)
	}
	c.switchToResponsesAPI(chatReq.Model)
	cr := responsesToChatResponse(resp, c.cfg.Provider)
	cr.Attempts, cr.Model = priorAttempts+attempts, c.sentModel(chatReq.Model)
	return cr, nil
}

func withPriorAttempts(err error, priorAttempts int) error {
	if llmErr, ok := errors.AsType[*Error](err); ok {
		llmErr.Attempts += priorAttempts
	}
	return err
}

func inBandResponsesError(provider string, e *schemas.ResponsesResponseError) *schemas.BifrostError {
	field := &schemas.ErrorField{Message: e.Message}
	if e.Code != "" {
		field.Code = schemas.Ptr(e.Code)
	}
	if e.Type != "" {
		field.Type = schemas.Ptr(e.Type)
	}
	return &schemas.BifrostError{
		StatusCode:  schemas.Ptr(inBandErrorStatus(e.Code)),
		Error:       field,
		ExtraFields: schemas.BifrostErrorExtraFields{RoutingInfo: schemas.RoutingInfo{Provider: schemas.ModelProvider(provider)}},
	}
}

func inBandErrorStatus(code string) int {
	if n, err := strconv.Atoi(code); err == nil && n >= 400 && n <= 599 {
		return n
	}
	switch code {
	case "", "server_error", "vector_store_timeout":
		return http.StatusBadGateway
	case "rate_limit_exceeded":
		return http.StatusTooManyRequests
	}
	return http.StatusBadRequest
}

func (c *BifrostClient) responsesCompletionStream(ctx context.Context, chatReq *schemas.BifrostChatRequest, priorAttempts int) (<-chan StreamChunk, error) {
	rr := c.buildResponsesRequest(chatReq)

	var reqCtx context.Context
	var cancel context.CancelFunc
	stream, attempts, err := callWithRetries(ctx, c, "responsesStream", nil,
		func() (chan *schemas.BifrostStreamChunk, *schemas.BifrostError) {
			attemptCtx, attemptCancel := context.WithCancel(ctx)
			bfCtx, authErr := c.responsesBifrostContext(attemptCtx, rr)
			if authErr != nil {
				attemptCancel()
				return nil, authErr
			}
			s, bfErr := c.bf.ResponsesStreamRequest(bfCtx, rr)
			if bfErr != nil {
				attemptCancel()
				return nil, bfErr
			}
			reqCtx, cancel = attemptCtx, attemptCancel
			return s, nil
		})
	if err != nil {
		return nil, withPriorAttempts(err, priorAttempts)
	}
	c.switchToResponsesAPI(chatReq.Model)

	out := make(chan StreamChunk, 16)
	go func() {
		defer cancel()
		defer close(out)
		c.forwardResponsesStream(reqCtx, stream, out, sentCall{model: c.sentModel(chatReq.Model), attempts: priorAttempts + attempts})
	}()
	return out, nil
}

func (c *BifrostClient) forwardResponsesStream(ctx context.Context, stream <-chan *schemas.BifrostStreamChunk, out chan<- StreamChunk, sent sentCall) {
	send := func(sc StreamChunk) bool {
		sent.stamp(&sc)
		select {
		case out <- sc:
			return true
		case <-ctx.Done():
			return false
		}
	}

	mapper := newResponsesStreamMapper(c.cfg.Provider)
	for {
		select {
		case <-ctx.Done():
			return
		case chunk, open := <-stream:
			if !c.forwardResponsesChunk(ctx, chunk, open, mapper, send) {
				return
			}
		}
	}
}

func (c *BifrostClient) forwardResponsesChunk(ctx context.Context, chunk *schemas.BifrostStreamChunk, open bool,
	mapper *responsesStreamMapper, send func(StreamChunk) bool) bool {
	if !open {
		if ctx.Err() == nil && !mapper.finished {
			send(c.endOfStreamChunk(""))
		}
		return false
	}
	if chunk.BifrostError != nil {
		c.invalidateTokenOnAuthError(chunk.BifrostError)
		send(StreamChunk{Err: c.providerError(chunk.BifrostError)})
		return false
	}
	if chunk.BifrostResponsesStreamResponse == nil || mapper.finished {
		return true
	}
	sc, ok := mapper.next(chunk.BifrostResponsesStreamResponse)
	return !ok || send(sc)
}

func responsesToChatResponse(resp *schemas.BifrostResponsesResponse, provider string) *ChatResponse {
	cr := &ChatResponse{}
	if resp == nil {
		return cr
	}
	var content, reasoning strings.Builder
	for _, item := range resp.Output {
		switch deref(item.Type) {
		case schemas.ResponsesMessageTypeMessage:
			content.WriteString(responsesText(item.Content))
		case schemas.ResponsesMessageTypeFunctionCall:
			if item.ResponsesToolMessage != nil {
				cr.ToolCalls = append(cr.ToolCalls, ToolCall{
					ID:        deref(item.CallID),
					Name:      deref(item.Name),
					Arguments: deref(item.Arguments),
				})
			}
		case schemas.ResponsesMessageTypeReasoning:
			if item.ResponsesReasoning != nil {
				for _, s := range item.Summary {
					reasoning.WriteString(s.Text)
				}
			}
		}
	}
	cr.Content = content.String()
	cr.ReasoningContent = reasoning.String()
	cr.FinishReason = responsesFinishReason(resp, len(cr.ToolCalls) > 0)
	cr.Usage = providerUsage(resp.Usage.ToBifrostLLMUsage(), provider)
	return cr
}

func responsesText(c *schemas.ResponsesMessageContent) string {
	if c == nil {
		return ""
	}
	if c.ContentStr != nil {
		return *c.ContentStr
	}
	var sb strings.Builder
	for _, block := range c.ContentBlocks {
		if block.Type == schemas.ResponsesOutputMessageContentTypeText && block.Text != nil {
			sb.WriteString(*block.Text)
		}
	}
	return sb.String()
}

func responsesFinishReason(resp *schemas.BifrostResponsesResponse, hasToolCalls bool) string {
	if resp != nil && deref(resp.Status) == schemas.ResponsesResponseStatusIncomplete && resp.IncompleteDetails != nil {
		switch resp.IncompleteDetails.Reason {
		case schemas.ResponsesResponseIncompleteReasonMaxOutputTokens:
			return finishReasonLength
		case schemas.ResponsesResponseIncompleteReasonContentFilter:
			return finishReasonContentFilter
		}
	}
	if hasToolCalls {
		return finishReasonToolCalls
	}
	return finishReasonStop
}

func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

type responsesStreamMapper struct {
	toolIndexByOutput map[int]int
	argsStreamed      map[int]bool
	textStreamed      map[int]bool
	finished          bool
	provider          string
}

func newResponsesStreamMapper(provider string) *responsesStreamMapper {
	return &responsesStreamMapper{
		provider:          provider,
		toolIndexByOutput: map[int]int{},
		argsStreamed:      map[int]bool{},
		textStreamed:      map[int]bool{},
	}
}

func (m *responsesStreamMapper) next(ev *schemas.BifrostResponsesStreamResponse) (StreamChunk, bool) {
	switch ev.Type {
	case schemas.ResponsesStreamResponseTypeOutputTextDelta:
		if deref(ev.Delta) == "" {
			return StreamChunk{}, false
		}
		m.textStreamed[deref(ev.OutputIndex)] = true
		return StreamChunk{ContentDelta: *ev.Delta}, true
	case schemas.ResponsesStreamResponseTypeReasoningSummaryTextDelta:
		return StreamChunk{ReasoningDelta: deref(ev.Delta)}, deref(ev.Delta) != ""
	case schemas.ResponsesStreamResponseTypeOutputItemAdded:
		return m.toolCallStarted(ev)
	case schemas.ResponsesStreamResponseTypeFunctionCallArgumentsDelta:
		return m.toolCallArguments(ev)
	case schemas.ResponsesStreamResponseTypeOutputItemDone:
		return m.itemFinished(ev)
	case schemas.ResponsesStreamResponseTypeCompleted, schemas.ResponsesStreamResponseTypeIncomplete:
		m.finished = true
		return StreamChunk{
			FinishReason: responsesFinishReason(ev.Response, len(m.toolIndexByOutput) > 0),
			Usage:        providerUsage(responsesUsage(ev.Response), m.provider),
		}, true
	}
	return StreamChunk{}, false
}

func responsesUsage(resp *schemas.BifrostResponsesResponse) *schemas.BifrostLLMUsage {
	if resp == nil {
		return nil
	}
	return resp.Usage.ToBifrostLLMUsage()
}

func isFunctionCallItem(item *schemas.ResponsesMessage) bool {
	return item != nil && item.ResponsesToolMessage != nil &&
		deref(item.Type) == schemas.ResponsesMessageTypeFunctionCall
}

func (m *responsesStreamMapper) toolCallStarted(ev *schemas.BifrostResponsesStreamResponse) (StreamChunk, bool) {
	if !isFunctionCallItem(ev.Item) || ev.OutputIndex == nil {
		return StreamChunk{}, false
	}
	delta, _ := m.toolCall(*ev.OutputIndex, ev.Item)
	return StreamChunk{ToolCallDeltas: []ToolCallDelta{delta}}, true
}

func (m *responsesStreamMapper) toolCall(outputIndex int, item *schemas.ResponsesMessage) (ToolCallDelta, bool) {
	if index, known := m.toolIndexByOutput[outputIndex]; known {
		return ToolCallDelta{Index: index}, false
	}
	index := len(m.toolIndexByOutput)
	m.toolIndexByOutput[outputIndex] = index
	return ToolCallDelta{Index: index, ID: deref(item.CallID), Name: deref(item.Name)}, true
}

func (m *responsesStreamMapper) toolCallArguments(ev *schemas.BifrostResponsesStreamResponse) (StreamChunk, bool) {
	if ev.OutputIndex == nil || deref(ev.Delta) == "" {
		return StreamChunk{}, false
	}
	index, ok := m.toolIndexByOutput[*ev.OutputIndex]
	if !ok {
		return StreamChunk{}, false
	}
	m.argsStreamed[*ev.OutputIndex] = true
	return StreamChunk{ToolCallDeltas: []ToolCallDelta{{Index: index, ArgumentsDelta: *ev.Delta}}}, true
}

func (m *responsesStreamMapper) itemFinished(ev *schemas.BifrostResponsesStreamResponse) (StreamChunk, bool) {
	if ev.OutputIndex == nil || ev.Item == nil {
		return StreamChunk{}, false
	}
	outputIndex := *ev.OutputIndex
	switch {
	case isFunctionCallItem(ev.Item):
		delta, opened := m.toolCall(outputIndex, ev.Item)
		if !m.argsStreamed[outputIndex] {
			delta.ArgumentsDelta = deref(ev.Item.Arguments)
			m.argsStreamed[outputIndex] = true
		}
		if !opened && delta.ArgumentsDelta == "" {
			return StreamChunk{}, false
		}
		return StreamChunk{ToolCallDeltas: []ToolCallDelta{delta}}, true
	case deref(ev.Item.Type) == schemas.ResponsesMessageTypeMessage && !m.textStreamed[outputIndex]:
		m.textStreamed[outputIndex] = true
		text := responsesText(ev.Item.Content)
		return StreamChunk{ContentDelta: text}, text != ""
	}
	return StreamChunk{}, false
}
