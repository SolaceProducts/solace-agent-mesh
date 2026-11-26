// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

type StatusPublisher struct {
	broker    broker.Broker
	topic     string
	taskID    string
	contextID string
	agentName string

	clientID   string
	userID     string
	userConfig string
	traceID    string

	seqGen *EventSeqGen

	publishMu sync.Mutex

	llmInvMu        sync.Mutex
	emittedContents int
	lastConfigPrint string

	modelMu      sync.Mutex
	model        usagefacts.Model
	modelStamped bool

	logger *slog.Logger
}

const maxStatusEventTextBytes = 16384

type StatusPublisherConfig struct {
	StatusTopic string
	TaskID      string
	ContextID   string
	AgentName   string
	ClientID    string
	UserID      string
	UserConfig  string
	TraceID     string
	SeqGen      *EventSeqGen
}

func NewStatusPublisher(b broker.Broker, cfg StatusPublisherConfig, logger *slog.Logger) *StatusPublisher {
	return &StatusPublisher{
		broker:     b,
		topic:      cfg.StatusTopic,
		taskID:     cfg.TaskID,
		contextID:  cfg.ContextID,
		agentName:  cfg.AgentName,
		clientID:   cfg.ClientID,
		userID:     cfg.UserID,
		userConfig: cfg.UserConfig,
		traceID:    cfg.TraceID,
		seqGen:     cfg.SeqGen,
		logger:     logger,
	}
}

func (sp *StatusPublisher) Enabled() bool {
	return sp.topic != ""
}

func (sp *StatusPublisher) PublishText(ctx context.Context, text string) {
	if !sp.Enabled() {
		return
	}
	parts := []a2a.Part{a2a.NewTextPart(text)}
	metadata := map[string]any{"agent_name": sp.agentName}
	sp.publish(ctx, parts, false, metadata)
}

func (sp *StatusPublisher) PublishProgressUpdate(ctx context.Context, statusText string) {
	sp.PublishProgressUpdateWithPartMeta(ctx, statusText, nil)
}

func (sp *StatusPublisher) PublishProgressUpdateWithPartMeta(ctx context.Context, statusText string, partMeta map[string]any) {
	if !sp.Enabled() {
		return
	}
	data := a2a.AgentProgressUpdateData{
		Type:       string(a2a.SignalAgentProgressUpdate),
		StatusText: statusText,
	}
	sp.publishSignalWithPartMeta(ctx, data, string(a2a.SignalAgentProgressUpdate), partMeta)
}

func (sp *StatusPublisher) PublishLLMInvocation(ctx context.Context, req *llm.ChatRequest, reportModel string) {
	if !sp.Enabled() {
		return
	}
	sp.emitLLMInvocation(ctx, formatLLMRequest(req, reportModel))
}

func (sp *StatusPublisher) PublishLLMInvocationDelta(ctx context.Context, req *llm.ChatRequest, reportModel string, lastAllowed bool) {
	if !sp.Enabled() {
		return
	}
	requestMap := sp.buildLLMInvocationDelta(req, reportModel)
	if lastAllowed {
		requestMap[a2a.LLMRequestLastAllowedCall] = true
	}
	sp.emitLLMInvocation(ctx, requestMap)
}

func (sp *StatusPublisher) emitLLMInvocation(ctx context.Context, requestMap map[string]any) {
	sanitizeBinaryInPlace(requestMap)

	truncateStatusText(requestMap, maxStatusEventTextBytes)

	data := a2a.LlmInvocationData{
		Type:    string(a2a.SignalLLMInvocation),
		Request: requestMap,
		Usage:   nil,
	}
	sp.publishSignal(ctx, data, string(a2a.SignalLLMInvocation))
}

func (sp *StatusPublisher) buildLLMInvocationDelta(req *llm.ChatRequest, reportModel string) map[string]any {
	sp.llmInvMu.Lock()
	defer sp.llmInvMu.Unlock()

	fp := configFingerprint(req)
	includeConfig := fp != sp.lastConfigPrint

	requestMap, total := formatLLMRequestDelta(req, reportModel, sp.emittedContents, includeConfig)
	if total < sp.emittedContents {
		requestMap, total = formatLLMRequestDelta(req, reportModel, 0, includeConfig)
	}

	sp.emittedContents = total
	sp.lastConfigPrint = fp
	return requestMap
}

func configFingerprint(req *llm.ChatRequest) string {
	h := fnv.New64a()
	for i := range req.Messages {
		if req.Messages[i].Role == "system" {
			_, _ = io.WriteString(h, req.Messages[i].Content)
			break
		}
	}
	for i := range req.Tools {
		_, _ = h.Write([]byte{0})
		_, _ = io.WriteString(h, req.Tools[i].Name)
		_, _ = h.Write([]byte{1})
		_, _ = io.WriteString(h, req.Tools[i].Description)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func truncateStatusText(v any, maxBytes int) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			if s, ok := vv.(string); ok {
				if capped, truncated := capStatusText(s, maxBytes); truncated {
					t[k] = capped
				}
				continue
			}
			truncateStatusText(vv, maxBytes)
		}
	case []map[string]any:
		for _, vv := range t {
			truncateStatusText(vv, maxBytes)
		}
	case []any:
		for i, vv := range t {
			if s, ok := vv.(string); ok {
				if capped, truncated := capStatusText(s, maxBytes); truncated {
					t[i] = capped
				}
				continue
			}
			truncateStatusText(vv, maxBytes)
		}
	}
}

func capStatusText(s string, maxBytes int) (string, bool) {
	return tool.CapText(s, maxBytes)
}

const minStatusPreviewBytes = 256

func sanitizeBinaryInPlace(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, vv := range t {
			if replacement, ok := sanitizeBinaryValue(vv); ok {
				t[k] = replacement
			} else {
				sanitizeBinaryInPlace(vv)
			}
		}
	case []any:
		for i, vv := range t {
			if replacement, ok := sanitizeBinaryValue(vv); ok {
				t[i] = replacement
			} else {
				sanitizeBinaryInPlace(vv)
			}
		}
	case []map[string]any:
		for _, vv := range t {
			sanitizeBinaryInPlace(vv)
		}
	}
}

func binaryDataPlaceholder(size int) string {
	return fmt.Sprintf("<binary data: %d bytes>", size)
}

func sanitizeBinaryValue(v any) (string, bool) {
	switch b := v.(type) {
	case []byte:
		return binaryDataPlaceholder(len(b)), true
	case json.RawMessage:
		return binaryDataPlaceholder(len(b)), true
	case string:
		if size, ok := sanitizeImageDataURI(b); ok {
			return binaryDataPlaceholder(size), true
		}
	}
	return "", false
}

func inlineDataURIInfo(s string) (string, int) {
	if !strings.HasPrefix(s, "data:") {
		return "", 0
	}
	comma := strings.Index(s, ",")
	if comma < 0 {
		return "", 0
	}
	header := s[len("data:"):comma]
	mimeType, rest, found := strings.Cut(header, ";")
	if !found || rest != "base64" {
		return "", 0
	}
	return mimeType, base64.StdEncoding.DecodedLen(len(s[comma+1:]))
}

func sanitizeImageDataURI(s string) (int, bool) {
	if !strings.HasPrefix(s, "data:image/") {
		return 0, false
	}
	comma := strings.Index(s, ",")
	if comma < 0 {
		return 0, false
	}
	header := s[:comma]
	if !strings.Contains(header, ";base64") {
		return 0, false
	}
	payload := s[comma+1:]
	return base64.StdEncoding.DecodedLen(len(payload)), true
}

func formatLLMRequest(req *llm.ChatRequest, reportModel string) map[string]any {
	out, _ := formatLLMRequestDelta(req, reportModel, 0, true)
	return out
}

func formatLLMRequestDelta(req *llm.ChatRequest, reportModel string, skip int, includeConfig bool) (map[string]any, int) {
	var contents, trailing []map[string]any
	var systemText string
	historyLen := 0
	for i := range req.Messages {
		m := &req.Messages[i]
		if m.Role == "system" {
			systemText = m.Content
			continue
		}
		if m.NoCacheBreakpoint {
			trailing = append(trailing, formatTrailingTurn(m))
			continue
		}
		idx := historyLen
		historyLen++
		if idx < skip {
			continue
		}
		contents = append(contents, formatMessageContent(m))
	}

	result := map[string]any{"model": reportModel}
	if len(contents) > 0 {
		result["contents"] = contents
	}
	if len(trailing) > 0 {
		result[a2a.LLMRequestTrailing] = trailing
	}
	if includeConfig {
		if config := buildLLMRequestConfig(systemText, req); len(config) > 0 {
			result["config"] = config
		}
	}
	return result, historyLen
}

const omittedTrailingText = "[per-request context omitted from the trace]"

func formatTrailingTurn(m *llm.ChatMessage) map[string]any {
	if !m.OmitFromTrace {
		return formatMessageContent(m)
	}
	return map[string]any{
		"role":  m.Role,
		"parts": []map[string]any{{"text": omittedTrailingText}},
	}
}

func formatMessageContent(m *llm.ChatMessage) map[string]any {
	parts := []map[string]any{}
	if m.ToolCallID != "" {
		var responseDict any
		if err := json.Unmarshal([]byte(m.Content), &responseDict); err != nil {
			responseDict = m.Content
		}
		responseDict = redactValue(responseDict)
		fnResp := map[string]any{
			"id":       m.ToolCallID,
			"response": responseDict,
		}
		if m.ToolName != "" {
			fnResp["name"] = m.ToolName
		}
		parts = append(parts, map[string]any{
			"function_response": fnResp,
		})
	} else if len(m.ContentBlocks) > 0 {
		for _, cb := range m.ContentBlocks {
			switch cb.Type {
			case "text":
				if cb.Text != "" {
					parts = append(parts, map[string]any{"text": cb.Text})
				}
			case "image_url":
				mimeType, size := inlineDataURIInfo(cb.ImageURL)
				if size <= 0 {
					continue
				}
				parts = append(parts, map[string]any{
					"inline_data": map[string]any{
						"mime_type": mimeType,
						"data":      binaryDataPlaceholder(size),
					},
				})
			case "file":
				mimeType, size := inlineDataURIInfo(cb.FileData)
				if mimeType == "" {
					mimeType = cb.FileType
				}
				if size <= 0 {
					continue
				}
				parts = append(parts, map[string]any{
					"inline_data": map[string]any{
						"mime_type": mimeType,
						"data":      binaryDataPlaceholder(size),
					},
				})
			}
		}
	} else if m.Content != "" {
		parts = append(parts, map[string]any{"text": m.Content})
	}
	for _, tc := range m.ToolCalls {
		parts = append(parts, map[string]any{
			"function_call": map[string]any{
				"id":   tc.ID,
				"name": tc.Name,
				"args": rawToolArgsDroppedBySanitizeBinary(tc.Arguments),
			},
		})
	}
	role := m.Role
	if role == "tool" {
		role = "user"
	}
	return map[string]any{
		"role":  role,
		"parts": parts,
	}
}

func rawToolArgsDroppedBySanitizeBinary(args string) json.RawMessage {
	return json.RawMessage(args)
}

func buildLLMRequestConfig(systemText string, req *llm.ChatRequest) map[string]any {
	config := map[string]any{}
	if systemText != "" {
		config["system_instruction"] = systemText
	}
	if len(req.Tools) > 0 {
		var toolDefs []map[string]any
		for _, t := range req.Tools {
			td := map[string]any{
				"name":        t.Name,
				"description": t.Description,
			}
			if t.Parameters != nil {
				td["parameters"] = t.Parameters
			}
			toolDefs = append(toolDefs, td)
		}
		config["tools"] = []map[string]any{
			{"function_declarations": toolDefs},
		}
	}
	genConfig := map[string]any{}
	if req.MaxTokens > 0 {
		genConfig["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	}
	if len(genConfig) > 0 {
		config["generation_config"] = genConfig
	}
	return config
}

type LLMResponseToolCall struct {
	Name     string
	Args     map[string]any
	ID       string
	Injected bool
}

func (sp *StatusPublisher) PublishLLMResponse(ctx context.Context, model string, responseContent string, usage *llm.Usage, toolCalls ...LLMResponseToolCall) {
	if !sp.Enabled() {
		return
	}

	var inputTokens, outputTokens, cachedTokens, reasoningTokens int
	if usage != nil {
		inputTokens = usage.PromptTokens
		outputTokens = usage.CompletionTokens
		cachedTokens = usage.CachedTokens
		reasoningTokens = usage.ReasoningTokens
	}

	var parts []map[string]any
	for _, tc := range toolCalls {
		if tc.Injected {
			parts = append(parts, map[string]any{
				"function_call": map[string]any{
					"name": tc.Name,
					"args": redactToolArgs(tc.Args),
					"id":   tc.ID,
				},
			})
		}
	}
	if responseContent != "" || len(toolCalls) == 0 {
		parts = append(parts, map[string]any{"text": responseContent})
	}
	for _, tc := range toolCalls {
		if !tc.Injected {
			parts = append(parts, map[string]any{
				"function_call": map[string]any{
					"name": tc.Name,
					"args": redactToolArgs(tc.Args),
					"id":   tc.ID,
				},
			})
		}
	}

	hasInjectedCalls := false
	for _, tc := range toolCalls {
		if tc.Injected {
			hasInjectedCalls = true
			break
		}
	}

	responseData := map[string]any{
		"content": map[string]any{
			"parts": parts,
			"role":  "model",
		},
		"partial": false,
	}
	if hasInjectedCalls {
		responseData["turn_complete"] = true
	}

	if inputTokens > 0 || outputTokens > 0 {
		responseData["usage_metadata"] = map[string]any{
			"prompt_token_count":     inputTokens,
			"candidates_token_count": outputTokens,
			"total_token_count":      inputTokens + outputTokens,
		}
	}

	reportedUsage := map[string]any{
		"input_tokens":        inputTokens,
		"output_tokens":       outputTokens,
		"cached_input_tokens": cachedTokens,
		"reasoning_tokens":    reasoningTokens,
		"model":               model,
	}
	if usage != nil && usage.CacheWritesReported {
		reportedUsage["cache_write_tokens"] = usage.CacheWriteTokens
	}
	data := a2a.LlmResponseData{
		Type:  string(a2a.SignalLLMResponse),
		Data:  responseData,
		Usage: reportedUsage,
	}
	sp.publishSignal(ctx, data, string(a2a.SignalLLMResponse))
}

func (sp *StatusPublisher) PublishToolInvocationStart(ctx context.Context, toolName string, toolArgs map[string]any, callID string, parallelGroupID string) {
	if !sp.Enabled() {
		return
	}
	var pgid *string
	if parallelGroupID != "" {
		pgid = &parallelGroupID
	}
	data := a2a.ToolInvocationStartData{
		Type:            string(a2a.SignalToolInvocationStart),
		ToolName:        toolName,
		ToolArgs:        redactToolArgs(toolArgs),
		FunctionCallID:  callID,
		ParallelGroupID: pgid,
	}
	sp.publishSignal(ctx, data, string(a2a.SignalToolInvocationStart))
}

func (sp *StatusPublisher) PublishToolResult(ctx context.Context, toolName string, resultData any, callID string) {
	sp.PublishToolResultWithPartMeta(ctx, toolName, resultData, callID, nil)
}

func (sp *StatusPublisher) PublishToolResultWithPartMeta(ctx context.Context, toolName string, resultData any, callID string,
	partMeta map[string]any) {
	if !sp.Enabled() {
		return
	}
	data := a2a.ToolResultData{
		Type:           string(a2a.SignalToolResult),
		ToolName:       toolName,
		ResultData:     redactValue(resultData),
		FunctionCallID: callID,
	}
	sp.publishSignalWithPartMeta(ctx, data, string(a2a.SignalToolResult), partMeta)
}

func (sp *StatusPublisher) PublishArtifactProgress(ctx context.Context, filename, status string, bytesTransferred int, chunk, mimeType, description *string, version *int, callID *string, tags []string) {
	if !sp.Enabled() {
		return
	}
	data := a2a.ArtifactCreationProgressData{
		Type:             string(a2a.SignalArtifactCreationProgress),
		Filename:         filename,
		Status:           status,
		BytesTransferred: bytesTransferred,
		Description:      description,
		ArtifactChunk:    chunk,
		MIMEType:         mimeType,
		Version:          version,
		FunctionCallID:   callID,
		Tags:             tags,
	}
	sp.publishSignal(ctx, data, string(a2a.SignalArtifactCreationProgress))
}

func (sp *StatusPublisher) PublishArtifactProgressWithError(ctx context.Context, filename, status string, bytesTransferred int, chunk, mimeType, description *string, version *int, callID *string, errorMessage *string, tags []string) {
	if !sp.Enabled() {
		return
	}
	data := a2a.ArtifactCreationProgressData{
		Type:             string(a2a.SignalArtifactCreationProgress),
		Filename:         filename,
		Status:           status,
		BytesTransferred: bytesTransferred,
		Description:      description,
		ArtifactChunk:    chunk,
		MIMEType:         mimeType,
		Version:          version,
		FunctionCallID:   callID,
		ErrorMessage:     errorMessage,
		Tags:             tags,
	}
	sp.publishSignal(ctx, data, string(a2a.SignalArtifactCreationProgress))
}

func (sp *StatusPublisher) PublishArtifactSaved(ctx context.Context, filename string, version int, mimeType string, sizeBytes int64, description *string, callID *string, tags []string) {
	if !sp.Enabled() {
		return
	}
	data := a2a.ArtifactSavedData{
		Type:           string(a2a.SignalArtifactSaved),
		Filename:       filename,
		Version:        version,
		MIMEType:       mimeType,
		SizeBytes:      sizeBytes,
		Description:    description,
		FunctionCallID: callID,
		Tags:           tags,
	}
	sp.publishSignal(ctx, data, string(a2a.SignalArtifactSaved))
}

func publishArtifactSavedFromHook(ctx context.Context, sp *StatusPublisher, info tool.ArtifactSavedInfo) {
	if sp == nil || !sp.Enabled() {
		return
	}
	var desc *string
	if info.Description != "" {
		d := info.Description
		desc = &d
	}
	var callID *string
	if info.ToolCallID != "" {
		c := info.ToolCallID
		callID = &c
	}
	var mime *string
	if info.MIMEType != "" {
		m := info.MIMEType
		mime = &m
	}
	version := info.Version
	sp.PublishArtifactProgress(ctx, info.Filename, "completed", int(info.SizeBytes), nil, mime, desc, &version, nil, info.Tags)
	sp.PublishArtifactSaved(ctx, info.Filename, info.Version, info.MIMEType, info.SizeBytes, desc, callID, info.Tags)
}

type templateBlockSignal struct {
	Type            string  `json:"type"`
	TemplateID      string  `json:"template_id"`
	DataArtifact    string  `json:"data_artifact"`
	JSONPath        *string `json:"jsonpath"`
	Limit           *int    `json:"limit"`
	TemplateContent string  `json:"template_content"`
}

func (sp *StatusPublisher) PublishTemplateBlock(ctx context.Context, block *FencedBlockEvent) {
	if !sp.Enabled() {
		return
	}
	data := templateBlockSignal{
		Type:            "template_block",
		TemplateID:      idgen.New(),
		DataArtifact:    block.DataSource,
		TemplateContent: block.Content,
	}
	if block.JSONPath != "" {
		data.JSONPath = &block.JSONPath
	}
	if block.Limit >= 0 {
		data.Limit = &block.Limit
	}
	sp.publishSignal(ctx, data, string(a2a.SignalTemplateBlock))
}

func (sp *StatusPublisher) PublishBuilderComponentProgress(ctx context.Context, componentID, componentType, componentName, state, statusText string, progressPercent *int) {
	if !sp.Enabled() {
		return
	}
	data := a2a.BuilderComponentProgressData{
		Type:            string(a2a.SignalBuilderComponentProgress),
		ComponentID:     componentID,
		ComponentType:   componentType,
		ComponentName:   componentName,
		State:           state,
		StatusText:      statusText,
		ProgressPercent: progressPercent,
	}
	sp.publishSignal(ctx, data, string(a2a.SignalBuilderComponentProgress))
}

func (sp *StatusPublisher) publishSignal(ctx context.Context, data any, signalType string) {
	sp.publishSignalWithPartMeta(ctx, data, signalType, nil)
}

func (sp *StatusPublisher) publishSignalWithPartMeta(ctx context.Context, data any, signalType string, partMeta map[string]any) {
	var part a2a.Part
	if len(partMeta) > 0 {
		part = a2a.NewDataPartWithMetadata(data, partMeta)
	} else {
		part = a2a.NewDataPart(data)
	}
	parts := []a2a.Part{part}
	metadata := map[string]any{
		"agent_name": sp.agentName,
	}
	sp.publishWithState(ctx, parts, false, metadata, a2a.TaskStateWorking)
}

func (sp *StatusPublisher) PublishGenericSignal(ctx context.Context, data any, signalType string) {
	if !sp.Enabled() {
		return
	}
	sp.publishSignal(ctx, data, signalType)
}

func (sp *StatusPublisher) PublishAuthenticationRequired(ctx context.Context, data a2a.AuthenticationRequiredData) {
	if !sp.Enabled() {
		return
	}
	part := a2a.NewDataPart(data)
	metadata := map[string]any{
		"agent_name": sp.agentName,
	}
	sp.publishWithState(ctx, []a2a.Part{part}, false, metadata, a2a.TaskStateInputNeeded)
}

func (sp *StatusPublisher) PublishAuthenticationResult(ctx context.Context, data a2a.AuthenticationResultData) {
	if !sp.Enabled() {
		return
	}
	part := a2a.NewDataPart(data)
	metadata := map[string]any{
		"agent_name": sp.agentName,
	}
	sp.publishWithState(ctx, []a2a.Part{part}, false, metadata, a2a.TaskStateWorking)
}

func (sp *StatusPublisher) PublishUserInputRequest(ctx context.Context, data a2a.UserInputRequestData) {
	if !sp.Enabled() {
		return
	}
	part := a2a.NewDataPart(data)
	metadata := map[string]any{
		"agent_name": sp.agentName,
	}
	sp.publishWithState(ctx, []a2a.Part{part}, false, metadata, a2a.TaskStateInputNeeded)
}

func (sp *StatusPublisher) ClassifyModel(model usagefacts.Model) {
	if model == (usagefacts.Model{}) {
		return
	}
	sp.modelMu.Lock()
	defer sp.modelMu.Unlock()
	if model == sp.model {
		return
	}
	sp.model = model
	sp.modelStamped = false
}

func (sp *StatusPublisher) stampModelClassification(metadata map[string]any) usagefacts.Model {
	sp.modelMu.Lock()
	defer sp.modelMu.Unlock()
	if sp.modelStamped || metadata == nil || sp.model == (usagefacts.Model{}) {
		return usagefacts.Model{}
	}
	if sp.model.Family != "" {
		metadata[a2a.MetadataModelFamily] = sp.model.Family
	}
	if sp.model.Provider != "" {
		metadata[a2a.MetadataModelProvider] = sp.model.Provider
	}
	if sp.model.ServedFrom != "" {
		metadata[a2a.MetadataModelType] = sp.model.ServedFrom
	}
	sp.modelStamped = true
	return sp.model
}

func (sp *StatusPublisher) withdrawModelStamp(model usagefacts.Model) {
	sp.modelMu.Lock()
	defer sp.modelMu.Unlock()
	if model == sp.model {
		sp.modelStamped = false
	}
}

func (sp *StatusPublisher) publish(ctx context.Context, parts []a2a.Part, final bool, metadata map[string]any) {
	sp.publishWithState(ctx, parts, final, metadata, a2a.TaskStateWorking)
}

func (sp *StatusPublisher) droppedForStoppedTask(ctx context.Context, state a2a.TaskState, final bool) bool {
	if !taskWasCancelled(ctx) {
		return false
	}
	sp.logger.Debug("status update dropped: the task was cancelled",
		"taskID", sp.taskID, "traceID", sp.traceID, "state", state, "final", final)
	return true
}

func (sp *StatusPublisher) logDroppedForCancelledTask(kind string) {
	sp.logger.Debug("tool publish dropped: the user cancelled the task",
		"taskID", sp.taskID, "traceID", sp.traceID, "kind", kind)
}

func (sp *StatusPublisher) publishWithState(ctx context.Context, parts []a2a.Part, final bool, metadata map[string]any, state a2a.TaskState) {
	if sp.droppedForStoppedTask(ctx, state, final) {
		return
	}
	stamped := sp.stampModelClassification(metadata)

	msgID := idgen.New()
	msgIDHex := strings.ReplaceAll(msgID, "-", "")

	event := &a2a.TaskStatusUpdateEvent{
		TaskID:    sp.taskID,
		ContextID: sp.contextID,
		Kind:      "status-update",
		Status: a2a.TaskStatus{
			State:     state,
			Timestamp: time.Now().UTC().Format("2006-01-02T15:04:05.000000+00:00"),
			Message: &a2a.Message{
				Kind:      "message",
				MessageID: msgIDHex,
				Role:      a2a.RoleAgent,
				Parts:     parts,
				TaskID:    sp.taskID,
				ContextID: sp.contextID,
			},
		},
		Final:    final,
		Metadata: metadata,
	}

	rpcResp := a2a.NewResponse(sp.taskID, event)

	payload, err := json.Marshal(rpcResp)
	if err != nil {
		sp.logger.Error("failed to marshal status update", "error", err)
		sp.withdrawModelStamp(stamped)
		return
	}

	userProps := map[string]string{
		"delegating_agent_name": sp.agentName,
		"timestamp":             strconv.FormatInt(time.Now().UnixMilli(), 10),
	}
	if sp.clientID != "" {
		userProps["clientId"] = sp.clientID
	}
	if sp.userID != "" {
		userProps["userId"] = sp.userID
	}
	if sp.userConfig != "" {
		userProps["a2aUserConfig"] = sp.userConfig
	}
	a2a.AddTraceID(userProps, sp.traceID)

	sp.publishMu.Lock()
	defer sp.publishMu.Unlock()

	if sp.droppedForStoppedTask(ctx, state, final) {
		sp.withdrawModelStamp(stamped)
		return
	}
	sp.seqGen.Stamp(userProps, sp.agentName, sp.taskID)

	msg := &broker.Message{
		Payload:        payload,
		UserProperties: userProps,
	}

	if err := sp.broker.PublishGuaranteed(ctx, sp.topic, msg); err != nil {
		sp.logger.Error("failed to publish status update",
			"topic", sp.topic,
			"error", err,
		)
		sp.withdrawModelStamp(stamped)
	}
}

func (sp *StatusPublisher) PublishFinal(ctx context.Context, text string) {
	if !sp.Enabled() {
		return
	}
	parts := []a2a.Part{a2a.NewTextPart(text)}
	metadata := map[string]any{"agent_name": sp.agentName}
	sp.publish(ctx, parts, true, metadata)
}

func NewParallelGroupID() string {
	return fmt.Sprintf("llm_batch_%s", idgen.New())
}

func (sp *StatusPublisher) TaskID() string {
	if sp == nil {
		return ""
	}
	return sp.taskID
}
