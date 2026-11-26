// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llmtest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelclient"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const probeSystemPrompt = "You are an AI assistant with access to tools. " +
	"When the user asks a question that requires a tool, call the appropriate tool. " +
	"You may call multiple tools in parallel when they are independent. " +
	"Always provide a brief status update before calling tools."

const probeUserMessage = "What time is it right now?"

const probeMaxTokens = 256

const maxProbeErrorLen = 500

const probeRequestTimeoutSeconds = 60

const (
	probeSuccessMessage       = "Connection test successful"
	probeNoResponseMessage    = "Test connection failed. No response from LLM"
	probeFailurePrefix        = "Test connection failed. "
	probeInvalidConfigMessage = probeFailurePrefix + "Invalid model configuration."
	probeBudgetSpentFormat    = "Connection not tested: %s."
)

type SpendRecording struct {
	Publisher   *spend.Publisher
	Attribution *llm.SpendAttribution
	Gate        *budget.Gate
}

func Probe(ctx context.Context, raw map[string]any, logger *slog.Logger, rec *SpendRecording) (bool, string) {
	err := ProbeError(ctx, raw, logger, rec)
	switch {
	case err == nil:
		return true, probeSuccessMessage
	case errors.Is(err, ErrNoResponse):
		return false, probeNoResponseMessage
	case errors.Is(err, ErrInvalidConfig):
		return false, probeInvalidConfigMessage
	default:
		return false, formatProbeError(err)
	}
}

var ErrNoResponse = errors.New("no response from the model")

var ErrNoConfig = errors.New("no model configuration provided")

var ErrInvalidConfig = errors.New("the model configuration could not be read")

var ErrClientInit = errors.New("the model configuration could not be used to build a provider client")

type clientInitError struct{ cause error }

func (e *clientInitError) Error() string   { return e.cause.Error() }
func (e *clientInitError) Unwrap() []error { return []error{ErrClientInit, e.cause} }

func ProbeError(ctx context.Context, raw map[string]any, logger *slog.Logger, rec *SpendRecording) error {
	if logger == nil {
		logger = slog.Default()
	}
	if raw == nil {
		return ErrNoConfig
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeRequestTimeoutSeconds*time.Second)
	defer cancel()

	client, err := modelclient.Build(probeCtx, raw, modelclient.Options{
		Logger:         logger,
		ConnectionTest: &modelclient.ConnectionTest{TimeoutSeconds: probeRequestTimeoutSeconds},
	})
	if err != nil {
		return probeBuildError(logger, err)
	}
	defer client.Shutdown()

	req := probeRequest(client, raw)

	logger.Debug("test connection probe outgoing",
		"model", req.Model,
		"provider", client.Provider,
		"maxTokens", req.MaxTokens,
		"temperature", client.RequestDefaults.Temperature,
		"stopSequences", client.RequestDefaults.StopSequences,
		"reasoning", req.Reasoning,
		"extraParamsKeys", slices.Sorted(maps.Keys(client.RequestDefaults.ExtraParams)),
	)

	var probeClient llm.Client = client
	if rec != nil {
		req.Spend = rec.Attribution
		probeClient = spend.NewMeteredClient(client, client.Model, rec.Publisher, rec.Gate)
	}

	stream, err := probeClient.ChatCompletionStream(probeCtx, req)
	if err == nil {
		err = awaitAnswer(probeCtx, stream)
	}
	if err != nil {
		logProbeFailure(logger, client.Provider, err)
	}
	return err
}

func awaitAnswer(ctx context.Context, stream <-chan llm.StreamChunk) error {
	var failure error
	answered := false
	for chunk := range stream {
		if chunk.Err != nil && failure == nil {
			failure = chunk.Err
		}
		if chunk.ContentDelta != "" || len(chunk.ToolCallDeltas) > 0 {
			answered = true
		}
	}
	switch {
	case ctx.Err() != nil && failure != nil:
		return fmt.Errorf("%w: %w", ctx.Err(), failure)
	case ctx.Err() != nil:
		return ctx.Err()
	case failure != nil:
		return failure
	case !answered:
		return ErrNoResponse
	}
	return nil
}

func probeBuildError(logger *slog.Logger, err error) error {
	if buildErr, ok := errors.AsType[*modelclient.BuildError](err); ok {
		logProbeFailure(logger, buildErr.Provider, buildErr.Cause)
		return &clientInitError{cause: buildErr.Cause}
	}
	return errors.Join(ErrInvalidConfig, err)
}

func probeRequest(client *modelclient.Client, raw map[string]any) *llm.ChatRequest {
	req := &llm.ChatRequest{
		Model: client.Model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: probeSystemPrompt},
			{Role: "user", Content: probeUserMessage},
		},
		Tools:            probeTools(),
		ToolChoice:       "auto",
		WithStoredParams: true,
	}
	storedParams, _ := raw[samapp.ModelParamsKey].(map[string]any)
	req.Reasoning = samapp.ModelReasoning(storedParams)
	if client.RequestDefaults.MaxTokens > 0 {
		return req
	}
	req.MaxTokens = probeMaxTokens
	if req.Reasoning != nil && req.Reasoning.BudgetTokens >= req.MaxTokens {
		req.MaxTokens = req.Reasoning.BudgetTokens + probeMaxTokens
	}
	return req
}

func probeTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "get_current_time",
			Description: "Gets the current date and time in the user's local timezone.",
			Parameters: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "web_search",
			Description: "Search the web for up-to-date information using a search query.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "The search query",
					},
				},
				"required": []any{"query"},
			},
		},
	}
}

func formatProbeError(err error) string {
	if exhausted, ok := errors.AsType[*budget.ExhaustedError](err); ok {
		return fmt.Sprintf(probeBudgetSpentFormat, budget.SpentText(exhausted))
	}
	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		if cause, fix, ok := llmErr.AzureSetupProblem(); ok {
			return probeFailurePrefix + cause + ". " + fix + ` Details: "` + sanitizeProbeText(llmErr.Message) + `"`
		}
	}
	return probeFailurePrefix + sanitizeProbeText(err.Error())
}

func sanitizeProbeText(text string) string {
	text = strings.ReplaceAll(text, redactedSentinelForStrip, "")
	if len(text) > maxProbeErrorLen {
		text = strings.ToValidUTF8(text[:maxProbeErrorLen], "") + "..."
	}
	return text
}

func logProbeFailure(logger *slog.Logger, provider string, err error) {
	args := []any{"provider", provider}
	if llmErr, ok := errors.AsType[*llm.Error](err); ok {
		args = append(args,
			"upstreamStatus", llmErr.StatusCode,
			"errorType", llmErr.Type,
			"errorCode", llmErr.Code,
		)
	}
	logger.Warn("test connection failed", args...)
}

const redactedSentinelForStrip = "<REDACTED>"
