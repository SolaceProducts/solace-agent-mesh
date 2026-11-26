// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	samtoolsdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const inProcessSchemaLabel = "in-process:"

var registeredInProcessTools []*samtoolsdk.ToolDef

func RegisterInProcessTools(tools ...*samtoolsdk.ToolDef) {
	registeredInProcessTools = append(registeredInProcessTools, tools...)
}

func InProcessToolNames() []string {
	names := make([]string, len(registeredInProcessTools))
	for i, td := range registeredInProcessTools {
		names[i] = td.Name()
	}
	slices.Sort(names)
	return slices.Compact(names)
}

func (w *Worker) inProcessTools() []*samtoolsdk.ToolDef {
	if len(w.cfg.InProcessTools) == 0 && len(registeredInProcessTools) == 0 {
		return nil
	}
	out := make([]*samtoolsdk.ToolDef, 0, len(w.cfg.InProcessTools)+len(registeredInProcessTools))
	out = append(out, w.cfg.InProcessTools...)
	out = append(out, registeredInProcessTools...)
	return out
}

func lookupGlobalInProcessTool(name string) *samtoolsdk.ToolDef {
	for _, td := range registeredInProcessTools {
		if td.Name() == name {
			return td
		}
	}
	return nil
}

func (w *Worker) lookupInProcessTool(name string) *samtoolsdk.ToolDef {
	for _, td := range w.inProcessTools() {
		if td.Name() == name {
			return td
		}
	}
	return nil
}

func inProcessDiscovered(td *samtoolsdk.ToolDef) (DiscoveredTool, error) {
	data, err := samtoolsdk.SchemaJSON([]*samtoolsdk.ToolDef{td}, nil)
	if err != nil {
		return DiscoveredTool{}, fmt.Errorf("schema for %q: %w", td.Name(), err)
	}
	discovered, err := parseSchemaOutput(data, inProcessSchemaLabel+td.Name())
	if err != nil {
		return DiscoveredTool{}, err
	}
	if s, ok := discovered[td.Name()]; ok {
		return s, nil
	}
	return DiscoveredTool{}, fmt.Errorf("tool %q absent from its own schema output", td.Name())
}

func (w *Worker) registerInProcessTools() {
	for _, td := range w.inProcessTools() {
		entry, err := w.inProcessManifestEntry(td)
		if err != nil {
			w.logger.Error("register in-process tool failed", "tool", td.Name(), "error", err)
			continue
		}
		w.manifest.RegisterBuiltin(entry)
		w.logger.Info("registered in-process tool", "tool", td.Name())
	}
}

func (w *Worker) inProcessManifestEntry(td *samtoolsdk.ToolDef) (*ManifestEntry, error) {
	data, err := samtoolsdk.SchemaJSON([]*samtoolsdk.ToolDef{td}, nil)
	if err != nil {
		return nil, fmt.Errorf("schema for %q: %w", td.Name(), err)
	}
	discovered, err := parseSchemaOutput(data, inProcessSchemaLabel+td.Name())
	if err != nil {
		return nil, err
	}
	info, ok := discovered[td.Name()]
	if !ok {
		return nil, fmt.Errorf("tool %q absent from its own schema output", td.Name())
	}
	return &ManifestEntry{
		ToolName:       td.Name(),
		Description:    info.Description,
		Parameters:     info.Parameters,
		ArtifactParams: info.ArtifactParams,
		Instructions:   info.Instructions,
		ConfigSchema:   info.ConfigSchema,
		Auth:           info.Auth,
		TimeoutSeconds: info.TimeoutSeconds,
		Effect:         info.Effect,
		IsBuiltin:      true,
		inProcess:      td,
	}, nil
}

func (w *Worker) resolveInProcessToolSchema(entry *ManifestEntry, toolConfig map[string]any) (*remotetool.InitParams, error) {
	var cfg map[string]any
	if len(toolConfig) > 0 {
		cfg = toolConfig
	}
	data, err := samtoolsdk.SchemaJSON([]*samtoolsdk.ToolDef{entry.inProcess}, cfg)
	if err != nil {
		return nil, fmt.Errorf("in-process schema for %q: %w", entry.ToolName, err)
	}
	discovered, err := parseSchemaOutput(data, inProcessSchemaLabel+entry.ToolName)
	if err != nil {
		return nil, err
	}
	return w.buildInitParamsFromDiscovered(entry, discovered)
}

func (w *Worker) executeInProcessTool(
	ctx context.Context,
	params *remotetool.InvokeParams,
	entry *ManifestEntry,
	artifactSvc *artifact.ScopedService,
	statusCb func(string),
) (*remotetool.InvokeResult, *remotetool.InvokeError) {
	start := time.Now()

	workDir, err := w.runner.setupWorkDir()
	if err != nil {
		return nil, &remotetool.InvokeError{Code: remotetool.ErrInternalError, Message: fmt.Sprintf("setup work dir: %v", err)}
	}
	defer func() { _ = os.RemoveAll(workDir) }()
	inputDir := filepath.Join(workDir, "input")
	outputDir := filepath.Join(workDir, "output")

	artifactPaths, artifactMeta, preErr := w.runner.preloadArtifacts(ctx, params, artifactSvc, inputDir)
	if preErr != nil {
		return nil, preErr
	}

	timeout, _ := resolveExecTimeout(w.runner.cfg.DefaultTimeoutSeconds, w.runner.cfg.MaxTimeoutSeconds, entry, params)
	execCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	bindings := samtoolsdk.InProcessBindings{
		UserID:        params.UserID,
		SessionID:     params.SessionID,
		AppName:       params.AppName,
		TaskID:        params.TaskID,
		OAuthToken:    params.ToolOAuthToken,
		AuthHeaders:   params.ToolAuthHeaders,
		A2AUserConfig: params.A2AUserConfig,
		SearchTurn:    params.SearchTurn,
		OutputDir:     outputDir,
		ArtifactPaths: artifactPaths,
		ArtifactMeta:  artifactMeta,
		Status:        func(s string) error { statusCb(s); return nil },
	}
	if w.runner.llmCall != nil {
		toolName := entry.ToolName
		modelAlias, aliasErr := toolConfigModelAlias(params.ToolConfig)
		if aliasErr != nil {
			return nil, &remotetool.InvokeError{
				Code:    remotetool.ErrInternalError,
				Message: aliasErr.Error(),
			}
		}
		bindings.LLM = func(ctx context.Context, systemPrompt, userPrompt string, temperature float64) (string, error) {
			return w.runner.llmCall(ctx, modelAlias, toolName, params, systemPrompt, userPrompt, temperature)
		}
	}

	if w.runner.llmChat != nil {
		chatModels, modelsErr := toolConfigChatModels(params.ToolConfig, w.runner.chatModelChoice, w.runner.toolModelAlias)
		if modelsErr != nil {
			return nil, &remotetool.InvokeError{Code: remotetool.ErrInternalError, Message: modelsErr.Error()}
		}
		bindings.Chat = func(ctx context.Context, messages []samtoolsdk.ChatMessage, opts samtoolsdk.ChatOptions) (*samtoolsdk.ChatResult, error) {
			return w.inProcessChat(ctx, chatModels, entry.ToolName, params, messages, opts)
		}
	}

	wire, err := samtoolsdk.InvokeInProcess(execCtx, []*samtoolsdk.ToolDef{entry.inProcess}, entry.inProcess.Name(), params.Args, params.ToolConfig, bindings)
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return nil, &remotetool.InvokeError{
				Code:    remotetool.ErrToolTimeout,
				Message: fmt.Sprintf("tool %q timed out after %ds", entry.ToolName, timeout),
			}
		}
		if ctx.Err() == context.Canceled {
			return nil, &remotetool.InvokeError{Code: remotetool.ErrInternalError, Message: "tool execution cancelled"}
		}
		return nil, &remotetool.InvokeError{Code: remotetool.ErrToolError, Message: err.Error()}
	}

	toolResult := w.runner.normalizeToolResult(wire)
	declaredMeta := extractDeclaredMeta(toolResult)
	spillOversizedResult(ctx, toolResult, artifactSvc,
		params.AppName, params.UserID, params.SessionID,
		entry.ToolName, w.runner.cfg.ToolOutputLLMReturnMaxBytes, w.logger)

	createdArtifacts, failedArtifacts, collectErr := w.runner.collectOutputArtifacts(ctx, params, artifactSvc, outputDir, declaredMeta, w.logger)
	if collectErr != nil {
		w.logger.Error("failed to collect in-process output artifacts", "tool", entry.ToolName, "error", collectErr)
	}
	if len(failedArtifacts) > 0 {
		markEnvelopeArtifactFailure(toolResult, failedArtifacts)
	}

	return &remotetool.InvokeResult{
		ToolResult:       toolResult,
		ExecutionTimeMs:  time.Since(start).Milliseconds(),
		CreatedArtifacts: createdArtifacts,
	}, nil
}

func (w *Worker) inProcessChat(ctx context.Context, chatModels toolChatModels, toolName string, params *remotetool.InvokeParams, messages []samtoolsdk.ChatMessage, opts samtoolsdk.ChatOptions) (*samtoolsdk.ChatResult, error) {
	p := chatParams{ToolChoice: opts.ToolChoice, MaxTokens: opts.MaxTokens, Temperature: opts.Temperature, ReasoningEffort: opts.ReasoningEffort}
	for _, m := range messages {
		msg := chatMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID, Name: m.Name}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, chatToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		p.Messages = append(p.Messages, msg)
	}
	for _, t := range opts.Tools {
		p.Tools = append(p.Tools, chatTool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	req, err := p.toLLM()
	if err != nil {
		return nil, &samtoolsdk.LLMError{Code: ipcCodeInvalidParams, Category: chatErrorInvalidRequest, Message: err.Error()}
	}
	choice, err := chatModels.pick(opts.Model)
	if err != nil {
		return nil, &samtoolsdk.LLMError{Code: chatErrorCode(err), Category: chatErrorCategory(ctx, err), Message: err.Error()}
	}
	resp, err := w.runner.llmChat(ctx, choice, toolName, params, req)
	if err != nil {
		return nil, &samtoolsdk.LLMError{Code: chatErrorCode(err), Category: chatErrorCategory(ctx, err), Message: err.Error()}
	}
	r := chatResultFromLLM(resp)
	out := &samtoolsdk.ChatResult{Content: r.Content, FinishReason: r.FinishReason, Model: r.Model,
		Usage: samtoolsdk.ChatUsage{PromptTokens: r.Usage.PromptTokens, CompletionTokens: r.Usage.CompletionTokens,
			TotalTokens: r.Usage.TotalTokens, CachedTokens: r.Usage.CachedTokens}}
	for _, tc := range r.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, samtoolsdk.ChatToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	return out, nil
}
