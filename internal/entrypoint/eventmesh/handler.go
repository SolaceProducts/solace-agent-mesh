// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/structured"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/sacexpr"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type pendingTask struct {
	handlerCfg            *EventHandlerConfig
	forwardContext        map[string]any
	deferredACK           bool
	userID                string
	sentStructuredRequest bool
	inboundID             string
}

func (a *EventMeshAdapter) processIncomingMessage(ctx context.Context, handler *EventHandlerConfig, msg *broker.Message) {
	a.core.ReportSubmitRefusal(a.transformMessage(ctx, handler, msg))
}

const msgAuthenticationFailed = "authentication failed; discarding message"

func (a *EventMeshAdapter) transformMessage(ctx context.Context, handler *EventHandlerConfig, msg *broker.Message) string {
	logger := a.logger.With("handler", handler.Name, "topic", msg.Topic)

	var payload any
	if len(msg.Payload) > 0 {
		if err := json.Unmarshal(msg.Payload, &payload); err != nil {
			payload = string(msg.Payload)
		}
	}

	exprCtx := sacexpr.NewContext(payload, msg.Topic, msg.UserProperties)

	if !a.admitsEvent(handler, exprCtx, msg, logger) {
		msg.Ack()
		return ""
	}

	userID, _ := resolveUserID(
		exprCtx,
		handler,
		a.core.Config().DefaultUserIdentity,
		a.core.Config().ForceUserIdentity,
		logger,
	)

	if userID != "" && rbac.IsSystemPrincipal(userID) {
		logger.Warn(msgAuthenticationFailed,
			"reason", "reserved_system_prefix_from_message")
		if !a.settlePreSubmitFailure(handler, msg) {
			return ""
		}
		return taxonomy.RejectForbidden
	}

	var ext *gwauth.ExternalID
	if userID == "" {
		if handler.UserIdentityExpression != "" {
			logger.Warn(msgAuthenticationFailed,
				"reason", "identity_expression_unresolved")
			a.publishIdentityFailure(ctx, logger, handler, evaluateForwardContext(exprCtx, handler, logger), "user_identity_expression resolved no identity for this event")
			if !a.settlePreSubmitFailure(handler, msg) {
				return ""
			}
			return taxonomy.RejectForbidden
		}
	} else {
		ext = &gwauth.ExternalID{Type: gwauth.IDSub, Value: userID}
	}
	caller, callerErr := gwauth.ForEventMeshCaller(ext, handler.RunAs)
	if callerErr != nil {
		logger.Warn(msgAuthenticationFailed,
			"reason", "caller_resolution_failed", "error", callerErr)
		if !a.settlePreSubmitFailure(handler, msg) {
			return ""
		}
		return taxonomy.RejectForbidden
	}
	userID = caller.UserID()

	targetName, targetType, err := resolveTarget(exprCtx, handler)
	if err != nil {
		logger.Error("failed to resolve target", "error", err)
		if !a.settlePreSubmitFailure(handler, msg) {
			return ""
		}
		return taxonomy.RejectInternal
	}

	var fileParts []a2a.Part
	if handler.ArtifactProcessing != nil {
		parts, artifactURIs, artErr := a.processArtifacts(ctx, exprCtx, handler, userID, logger)
		if artErr != nil {
			logger.Error("artifact processing failed", "error", artErr)
			if !a.settlePreSubmitFailure(handler, msg) {
				return ""
			}
			return taxonomy.RejectInternal
		}
		fileParts = parts
		if len(artifactURIs) > 0 {
			_ = exprCtx.SetData("user_data.created_artifacts:uris", artifactURIs)
		}
	}

	structuredRequest := structuredInvocationFor(handler)

	var inputText string
	var inputValue any
	if handler.InputExpression != "" {
		resolved, err := exprCtx.GetData(handler.InputExpression)
		if err != nil {
			logger.Error("failed to evaluate input_expression", "error", err)
			if !a.settlePreSubmitFailure(handler, msg) {
				return ""
			}
			return taxonomy.RejectInternal
		}
		inputValue = resolved
		inputText = inputAsText(resolved)
	}
	if structuredRequest != nil && !inputExpressionSelectsData(handler.InputExpression) {
		inputValue = payload
		if handler.InputExpression == "" {
			inputText = inputAsText(payload)
		}
	}

	forwardCtx := evaluateForwardContext(exprCtx, handler, logger)

	taskID := idgen.New()
	ackPolicy := a.getAckPolicy(handler)
	message := a2a.Message{
		Role:  "user",
		Parts: append([]a2a.Part{a2a.NewTextPart(inputText)}, fileParts...),
	}
	if structuredRequest != nil {
		message = structuredInvocationMessage(structuredRequest, a.core.GatewayID(), handler.Name, taskID, inputText, inputValue, fileParts)
	}
	var externalUser string
	if ext != nil {
		externalUser = ext.Value
	}
	return a.storeEvent(ctx, logger, msg, storedEvent{
		Handler:        handler.Name,
		ExternalUser:   externalUser,
		Target:         targetName,
		TargetType:     targetType,
		Message:        message,
		ForwardContext: forwardCtx,
		UserID:         userID,
		Structured:     structuredRequest != nil,
		Deferred:       ackPolicy.Mode == "on_completion",
	}, taskID, ackPolicy)
}

func resolveTarget(ctx *sacexpr.Context, h *EventHandlerConfig) (name, targetType string, err error) {
	if h.TargetWorkflowName != "" {
		return h.TargetWorkflowName, "workflow", nil
	}
	if h.TargetWorkflowNameExpr != "" {
		resolved, err := ctx.GetData(h.TargetWorkflowNameExpr)
		if err != nil {
			return "", "", fmt.Errorf("target_workflow_name_expression: %w", err)
		}
		s, ok := resolved.(string)
		if !ok || s == "" {
			return "", "", fmt.Errorf("target_workflow_name_expression resolved to empty/non-string")
		}
		return s, "workflow", nil
	}

	if h.TargetAgentName != "" {
		return h.TargetAgentName, "agent", nil
	}
	if h.TargetAgentNameExpression != "" {
		resolved, err := ctx.GetData(h.TargetAgentNameExpression)
		if err != nil {
			return "", "", fmt.Errorf("target_agent_name_expression: %w", err)
		}
		s, ok := resolved.(string)
		if !ok || s == "" {
			return "", "", fmt.Errorf("target_agent_name_expression resolved to empty/non-string")
		}
		return s, "agent", nil
	}

	return "", "", fmt.Errorf("no target agent or workflow configured")
}

func (a *EventMeshAdapter) processArtifacts(
	ctx context.Context,
	exprCtx *sacexpr.Context,
	handler *EventHandlerConfig,
	userID string,
	logger *slog.Logger,
) ([]a2a.Part, []any, error) {
	artSvc := a.core.Services().Artifacts()
	if artSvc == nil {
		return nil, nil, fmt.Errorf("artifact service not configured")
	}
	return processArtifactItems(ctx, exprCtx, handler.ArtifactProcessing, userID, artSvc, logger)
}

func processArtifactItems(
	ctx context.Context,
	exprCtx *sacexpr.Context,
	ap *ArtifactProcessingConfig,
	userID string,
	artSvc *artifact.ScopedService,
	logger *slog.Logger,
) ([]a2a.Part, []any, error) {
	if ap == nil || ap.ExtractExpression == "" {
		return nil, nil, nil
	}

	extracted, err := exprCtx.GetData(ap.ExtractExpression)
	if err != nil {
		return nil, nil, fmt.Errorf("extract_artifacts_expression: %w", err)
	}
	if extracted == nil {
		return nil, nil, nil
	}

	var items []any
	switch v := extracted.(type) {
	case []any:
		items = v
	default:
		items = []any{v}
	}

	var fileParts []a2a.Part
	var artifactURIs []any

	defer exprCtx.SetIterationItem(nil)
	for i, item := range items {
		exprCtx.SetIterationItem(item)

		def := ap.Definition
		filename := resolveDefinitionField(exprCtx, def.Filename, fmt.Sprintf("artifact_%d", i))
		mimeType := resolveDefinitionField(exprCtx, def.MimeType, "application/octet-stream")
		contentEncoding := resolveDefinitionField(exprCtx, def.ContentEncoding, "utf-8")
		encodingLow := strings.ToLower(contentEncoding)

		var content string
		switch encodingLow {
		case "base64", "base64url", "base64raw", "base64rawurl":
			var err error
			content, err = resolveStringField(exprCtx, def.Content, "")
			if err != nil {
				logger.Warn("artifact content expression must return a string for base64 encoding", "artifactIndex", i, "error", err)
				continue
			}
		default:
			content = resolveDefinitionField(exprCtx, def.Content, "")
		}

		var contentBytes []byte
		switch encodingLow {
		case "binary":
			return nil, nil, fmt.Errorf("artifact %d: content_encoding %q is not supported; use %q for standard base64", i, contentEncoding, "base64")
		case "base64":
			decoded, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				logger.Warn("failed to decode base64 artifact content", "artifactIndex", i)
				continue
			}
			contentBytes = decoded
		case "base64url":
			decoded, err := base64.URLEncoding.DecodeString(content)
			if err != nil {
				logger.Warn("failed to decode base64url artifact content", "artifactIndex", i)
				continue
			}
			contentBytes = decoded
		case "base64raw":
			decoded, err := base64.RawStdEncoding.DecodeString(content)
			if err != nil {
				logger.Warn("failed to decode base64raw artifact content", "artifactIndex", i)
				continue
			}
			contentBytes = decoded
		case "base64rawurl":
			decoded, err := base64.RawURLEncoding.DecodeString(content)
			if err != nil {
				logger.Warn("failed to decode base64rawurl artifact content", "artifactIndex", i)
				continue
			}
			contentBytes = decoded
		case "", "utf-8":
			contentBytes = []byte(content)
		default:
			return nil, nil, fmt.Errorf("artifact %d: content_encoding %q is unrecognised; use utf-8, base64, base64url, base64raw, or base64rawurl", i, contentEncoding)
		}

		if len(contentBytes) == 0 {
			logger.Warn("artifact content resolved to empty; skipping artifact", "artifactIndex", i)
			continue
		}

		sessionID := idgen.New()
		loc := artSvc.Resolve("", userID, sessionID, filename)
		if _, err := artSvc.Inner.Save(ctx, loc, contentBytes, mimeType); err != nil {
			logger.Warn("failed to save artifact", "artifactIndex", i, "error", err)
			continue
		}

		uri := fmt.Sprintf("artifact://%s/%s/%s/%s",
			url.PathEscape(loc.AppName), url.PathEscape(userID),
			url.PathEscape(sessionID), url.PathEscape(filename))
		artifactURIs = append(artifactURIs, uri)

		fileParts = append(fileParts, a2a.NewFilePart(uri, filename, mimeType))
	}

	return fileParts, artifactURIs, nil
}

func resolveStringField(ctx *sacexpr.Context, expr, defaultVal string) (string, error) {
	if expr == "" {
		return defaultVal, nil
	}
	val, err := ctx.GetData(expr)
	if err != nil || val == nil {
		return defaultVal, nil
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("evaluate %q: expected a string value, got %T", expr, val)
	}
	return s, nil
}

func resolveDefinitionField(ctx *sacexpr.Context, expr, defaultVal string) string {
	if expr == "" {
		return defaultVal
	}
	val, err := ctx.GetData(expr)
	if err != nil || val == nil {
		return defaultVal
	}
	if s, ok := val.(string); ok {
		return s
	}
	b, err := json.Marshal(val)
	if err != nil {
		return defaultVal
	}
	return string(b)
}

func (a *EventMeshAdapter) getAckPolicy(handler *EventHandlerConfig) AckPolicy {
	if handler.AckPolicy != nil {
		return *handler.AckPolicy
	}
	return a.cfg.AckPolicy
}

func (a *EventMeshAdapter) settlePreSubmitFailure(handler *EventHandlerConfig, msg *broker.Message) bool {
	policy := a.getAckPolicy(handler)
	if policy.Mode != "on_completion" {
		msg.Ack()
		return true
	}
	settleFailure(msg, &policy.OnFailure)
	return policy.OnFailure.Action == "ack"
}

func structuredInvocationFor(handler *EventHandlerConfig) *StructuredInvocationConfig {
	if !handler.SendsStructuredRequest() {
		return nil
	}
	return handler.StructuredInvocation
}

func structuredInvocationMessage(
	si *StructuredInvocationConfig,
	gatewayID, handlerName, taskID, inputText string,
	inputValue any,
	fileParts []a2a.Part,
) a2a.Message {
	parts := []a2a.Part{
		buildStructuredInvocationDataPart(si, gatewayID, handlerName, taskID),
		a2a.NewTextPart(inputText),
	}
	if !structured.IsSingleTextSchema(si.InputSchema) {
		parts = append(parts, structuredInputFilePart(handlerName, inputValue))
	}
	return a2a.Message{
		Role:  "user",
		Parts: append(parts, fileParts...),
	}
}

func buildStructuredInvocationDataPart(si *StructuredInvocationConfig, gatewayID, handlerName, taskID string) a2a.Part {
	return a2a.NewDataPart(map[string]any{
		"type":                      string(a2a.SignalStructuredInvocationReq),
		"workflow_name":             gatewayID,
		"node_id":                   handlerName,
		"input_schema":              si.InputSchema,
		"output_schema":             si.OutputSchema,
		"suggested_output_filename": fmt.Sprintf("%s_%s_%s.json", gatewayID, handlerName, taskID),
	})
}

func inputExpressionSelectsData(expr string) bool {
	return expr != "" && !strings.HasPrefix(expr, promptTemplatePrefix)
}

const promptTemplatePrefix = "template:"

func inputAsText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func structuredInputFilePart(handlerName string, inputValue any) a2a.Part {
	var content []byte
	if s, ok := inputValue.(string); ok && json.Valid([]byte(s)) {
		content = []byte(s)
	} else {
		content, _ = json.Marshal(inputValue)
	}
	return a2a.NewFilePartWithBytes(
		base64.StdEncoding.EncodeToString(content),
		fmt.Sprintf("input_%s.json", handlerName),
		"application/json",
	)
}

func evaluateForwardContext(exprCtx *sacexpr.Context, handler *EventHandlerConfig, logger *slog.Logger) map[string]any {
	forwardCtx := make(map[string]any, len(handler.ForwardContext))
	for key, expr := range handler.ForwardContext {
		val, err := exprCtx.GetData(expr)
		if err != nil {
			logger.Warn("forward_context evaluation failed", "key", key, "error", err)
		}
		forwardCtx[key] = val
	}
	return forwardCtx
}
