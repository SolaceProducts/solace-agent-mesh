// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package structured

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type Handler struct {
	inputSchema  map[string]any
	outputSchema map[string]any
	maxRetries   int
	logger       *slog.Logger
}

func NewHandler(cfg HandlerConfig) (*Handler, error) {
	if cfg.InputSchema != nil {
		if err := CompileSchema(cfg.InputSchema); err != nil {
			return nil, fmt.Errorf("invalid input_schema: %w", err)
		}
	}
	if cfg.OutputSchema != nil {
		if err := CompileSchema(cfg.OutputSchema); err != nil {
			return nil, fmt.Errorf("invalid output_schema: %w", err)
		}
	}

	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 2
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Handler{
		inputSchema:  cfg.InputSchema,
		outputSchema: cfg.OutputSchema,
		maxRetries:   maxRetries,
		logger:       logger,
	}, nil
}

func (h *Handler) ExtractRequest(msg a2a.Message) *a2a.StructuredInvocationRequest {
	for _, p := range msg.Parts {
		if p.Kind != "data" || p.Data == nil {
			continue
		}

		data, ok := toMapStringAny(p.Data)
		if !ok {
			continue
		}

		if data["type"] != "structured_invocation_request" {
			continue
		}

		jsonBytes, err := json.Marshal(data)
		if err != nil {
			h.logger.Warn("failed to marshal structured invocation data", "error", err)
			return nil
		}

		var req a2a.StructuredInvocationRequest
		if err := json.Unmarshal(jsonBytes, &req); err != nil {
			h.logger.Warn("invalid structured invocation request", "error", err)
			return nil
		}

		return &req
	}

	return nil
}

func (h *Handler) BuildContext(req *a2a.StructuredInvocationRequest) *Context {
	inputSchema := req.InputSchema
	if inputSchema == nil {
		inputSchema = h.inputSchema
	}
	if inputSchema == nil {
		inputSchema = defaultInputSchema
	}

	outputSchema := req.OutputSchema
	if outputSchema == nil {
		outputSchema = h.outputSchema
	}

	return &Context{
		Request:      req,
		InputSchema:  inputSchema,
		OutputSchema: outputSchema,
		MaxRetries:   h.maxRetries,
	}
}

func (h *Handler) ValidateInput(msg a2a.Message, sc *Context) []string {
	data, err := ExtractInputData(msg, sc.InputSchema)
	if err != nil {
		return []string{fmt.Sprintf("input extraction error: %v", err)}
	}

	h.logger.Debug("structured invocation input data",
		"nodeID", sc.Request.NodeID,
		"fieldCount", len(data),
	)

	return Validate(data, sc.InputSchema)
}

func (h *Handler) ParseAndValidateOutput(
	ctx context.Context,
	llmOutput string,
	sc *Context,
	loader ArtifactLoader,
	agentName, userID, sessionID string,
) (*a2a.StructuredInvocationResult, *RetryFeedback, error) {

	logID := fmt.Sprintf("[Node:%s]", sc.Request.NodeID)

	embed := ParseResultEmbed(llmOutput)
	if embed == nil {
		if sc.RetryCount < sc.MaxRetries {
			h.logger.Debug("no result embed found in agent output", "nodeID", sc.Request.NodeID)

			return nil, &RetryFeedback{
				Message: `ERROR: You failed to provide the mandatory result embed in your response.
You MUST end your response with:
«result:artifact=<your_artifact_name>:<version> status=success»

Please retry and ensure you include this embed.`,
			}, nil
		}

		h.logger.Warn("no result embed found in agent output after max retries", "nodeID", sc.Request.NodeID)

		return &a2a.StructuredInvocationResult{
			Type:         "structured_invocation_result",
			Status:       "error",
			ErrorMessage: taxonomy.StructuredResultMissingMessage,
			RetryCount:   sc.RetryCount,
		}, nil, nil
	}

	if embed.Status == "error" || embed.Status == "failed" {
		errMsg := taxonomy.AgentReportedErrorMessage
		if embed.Message != "" {
			errMsg += ": " + embed.Message
		}
		return &a2a.StructuredInvocationResult{
			Type:         "structured_invocation_result",
			Status:       "error",
			ErrorMessage: errMsg,
			RetryCount:   sc.RetryCount,
		}, nil, nil
	}

	if sc.OutputSchema == nil {
		var ref *a2a.ArtifactRef
		if embed.ArtifactName != "" {
			ref = &a2a.ArtifactRef{Name: embed.ArtifactName}
			if embed.Version != nil {
				ref.Version = *embed.Version
			}
			if loader != nil {
				_, loadedVersion, err := loader.LoadContent(agentName, userID, sessionID, embed.ArtifactName, nil)
				if err != nil {
					return artifactLoadFailure(sc, embed.ArtifactName, err)
				}
				ref.Version = loadedVersion
			}
		}
		return &a2a.StructuredInvocationResult{
			Type:              "structured_invocation_result",
			Status:            "success",
			OutputArtifactRef: ref,
			RetryCount:        sc.RetryCount,
		}, nil, nil
	}

	h.logger.Debug("loading artifact for validation",
		"logID", logID,
		"artifact", embed.ArtifactName,
		"version", embed.Version,
	)

	content, loadedVersion, err := loader.LoadContent(agentName, userID, sessionID, embed.ArtifactName, nil)
	if err != nil {
		return artifactLoadFailure(sc, embed.ArtifactName, err)
	}

	var artifactData any
	if parseErr := json.Unmarshal(content, &artifactData); parseErr != nil {
		if sc.RetryCount < sc.MaxRetries {
			return nil, &RetryFeedback{
				Message: fmt.Sprintf("ERROR: Artifact %q is not valid JSON: %v\nPlease fix the artifact content to be valid JSON conforming to the output schema.", embed.ArtifactName, parseErr),
			}, nil
		}
		return &a2a.StructuredInvocationResult{
			Type:         "structured_invocation_result",
			Status:       "error",
			ErrorMessage: fmt.Sprintf("Artifact %q is not valid JSON: %v", embed.ArtifactName, parseErr),
			RetryCount:   sc.RetryCount,
		}, nil, nil
	}

	validationErrors := Validate(artifactData, sc.OutputSchema)
	if validationErrors != nil {
		if sc.RetryCount < sc.MaxRetries {
			h.logger.Debug("output schema validation failed",
				"nodeID", sc.Request.NodeID,
				"errors", validationErrors,
			)

			return nil, &RetryFeedback{
				Message: fmt.Sprintf("ERROR: Your output artifact %q failed schema validation.\n\nValidation errors:\n- %s\n\nPlease fix the artifact to conform to the required JSON Schema and include the result embed.",
					embed.ArtifactName, strings.Join(validationErrors, "\n- ")),
			}, nil
		}

		h.logger.Warn("output schema validation failed after max retries",
			"nodeID", sc.Request.NodeID,
			"errors", validationErrors,
		)

		return &a2a.StructuredInvocationResult{
			Type:             "structured_invocation_result",
			Status:           "error",
			ErrorMessage:     taxonomy.StructuredOutputInvalidMessage,
			ValidationErrors: validationErrors,
			RetryCount:       sc.RetryCount,
		}, nil, nil
	}

	return &a2a.StructuredInvocationResult{
		Type:   "structured_invocation_result",
		Status: "success",
		OutputArtifactRef: &a2a.ArtifactRef{
			Name:    embed.ArtifactName,
			Version: loadedVersion,
		},
		RetryCount: sc.RetryCount,
	}, nil, nil
}

func artifactLoadFailure(sc *Context, name string, err error) (*a2a.StructuredInvocationResult, *RetryFeedback, error) {
	if sc.RetryCount < sc.MaxRetries {
		return nil, &RetryFeedback{
			Message: fmt.Sprintf("ERROR: Failed to load output artifact %q: %v\nPlease create the artifact and include the result embed.", name, err),
		}, nil
	}
	return &a2a.StructuredInvocationResult{
		Type:         "structured_invocation_result",
		Status:       "error",
		ErrorMessage: fmt.Sprintf("Failed to load artifact %q: %v", name, err),
		RetryCount:   sc.RetryCount,
	}, nil, nil
}

func (h *Handler) BuildErrorResult(msg string, valErrors []string, retryCount int) *a2a.StructuredInvocationResult {
	return &a2a.StructuredInvocationResult{
		Type:             "structured_invocation_result",
		Status:           "error",
		ErrorMessage:     msg,
		ValidationErrors: valErrors,
		RetryCount:       retryCount,
	}
}

func toMapStringAny(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}
