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
	"strconv"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type workflowAgentTool struct {
	tool.SideEffectingTool
	peerName    string
	card        a2a.AgentCard
	agent       *Agent
	namespace   string
	inputSchema map[string]any
}

var _ tool.BuiltinTool = (*workflowAgentTool)(nil)

func (t *workflowAgentTool) Name() string {
	return a2a.WorkflowToolName(t.peerName)
}

func (t *workflowAgentTool) Description() string {
	desc := fmt.Sprintf(a2a.WorkflowToolLeadFormat, t.displayName())
	if t.card.Description != "" {
		desc += " " + t.card.Description
	}
	if declared := declaredWorkflowOutput(t.card); declared != "" {
		desc += " " + declared
	}
	return desc
}

func (t *workflowAgentTool) displayName() string {
	if name := displayNameFromCard(t.card); name != "" {
		return name
	}
	return t.peerName
}

func (t *workflowAgentTool) Parameters() *tool.JSONSchema {
	properties := map[string]any{
		"input_artifact": map[string]any{
			"type":        "string",
			"description": "Filename of an existing artifact containing the input JSON data. Use this OR individual parameters.",
		},
	}

	if schemaProps, ok := t.inputSchema["properties"].(map[string]any); ok {
		for k, v := range schemaProps {
			properties[k] = v
		}
	}

	return &tool.JSONSchema{
		Type:       "object",
		Properties: properties,
		Required:   []string{},
	}
}

func (t *workflowAgentTool) Instructions() string { return "" }

func (t *workflowAgentTool) IsLongRunning() bool { return true }

func (t *workflowAgentTool) RawStringArgs() []string { return nil }

func (t *workflowAgentTool) ArtifactParams() map[string]tool.ArtifactParamInfo { return nil }

func (t *workflowAgentTool) ContextParam() string { return "" }

func (t *workflowAgentTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if authzErr := t.agent.svc.AgentAuthorizer().AuthorizeWorkflow(ctx, toolCtx.Identity, t.peerName); authzErr != nil {
		return tool.Error(fmt.Sprintf("workflow invocation denied: %v", authzErr)), nil
	}

	inputJSON, artifactURI, err := t.prepareInput(ctx, args, toolCtx)
	if err != nil {
		return tool.Error("input preparation failed: " + err.Error()), nil
	}

	reqID := newWorkflowRunTaskID()
	contextID := toolCtx.SessionID
	if contextID == "" {
		contextID = reqID
	}

	msg := t.buildA2AMessage(inputJSON, artifactURI, contextID, toolCtx.ParentTaskID, toolCtx.SessionID)

	a2aReq := a2a.Request{
		JSONRPC: "2.0",
		Method:  "message/send",
		ID:      reqID,
		Params:  map[string]any{"message": msg},
	}

	reqBytes, marshalErr := json.Marshal(a2aReq)
	if marshalErr != nil {
		return tool.Error("failed to marshal workflow request: " + marshalErr.Error()), nil
	}

	if t.agent.cfg.MaxMessageSizeBytes > 0 && len(reqBytes) > t.agent.cfg.MaxMessageSizeBytes {
		return tool.Error(fmt.Sprintf(
			"Message size %d bytes exceeds maximum limit of %d bytes",
			len(reqBytes), t.agent.cfg.MaxMessageSizeBytes,
		)), nil
	}

	peerTopic := a2a.AgentRequestTopic(t.namespace, t.peerName)
	replyTo := a2a.PeerResponseTopic(t.namespace, t.agent.cfg.Name, reqID)

	workflowPeerTimeout := resolveDelegationTimeout(
		t.agent.cfg.WorkflowRequestTimeoutSeconds, config.DefaultWorkflowRequestTimeoutSecs)

	completeCh := t.agent.peerRouter.Register(
		reqID,
		toolCtx.A2AStatusTopic,
		toolCtx.ParentTaskID,
		t.agent.cfg.Name,
		t.peerName,
		toolCtx.ToolCallID,
		toolCtx.ClientID,
		toolCtx.UserID,
		toolCtx.A2AUserConfig,
		workflowPeerTimeout,
		WithTraceID(toolCtx.TraceID),
		WithForwardArtifactProgress(),
	)

	peerStatusTopic := a2a.PeerStatusTopic(t.namespace, t.agent.cfg.Name, reqID)

	userProps := map[string]string{
		"replyTo": replyTo,
		"userId":  toolCtx.UserID,
	}
	if peerStatusTopic != "" {
		userProps["a2aStatusTopic"] = peerStatusTopic
	}
	if toolCtx.A2AUserConfig != "" {
		userProps["a2aUserConfig"] = toolCtx.A2AUserConfig
	}
	if toolCtx.Identity != nil && toolCtx.Identity.AuthToken != "" {
		userProps["authToken"] = toolCtx.Identity.AuthToken
	}
	userProps["delegating_agent_name"] = t.agent.cfg.Name
	userProps["callDepth"] = strconv.Itoa(toolCtx.CallDepth + 1)
	a2a.AddTraceID(userProps, toolCtx.TraceID)
	a2a.AddTrigger(userProps, toolCtx.Trigger)
	a2a.AddRoot(userProps, toolCtx.RootKind, toolCtx.RootID, toolCtx.RootNodeID)
	a2a.AddOrigin(userProps, toolCtx.Origin)

	if pubErr := t.agent.svc.Broker().PublishGuaranteed(ctx, peerTopic, &broker.Message{
		Payload:        reqBytes,
		ReplyTo:        replyTo,
		UserProperties: userProps,
	}); pubErr != nil {
		t.agent.peerRouter.Cancel(reqID)
		return tool.Error(fmt.Sprintf("workflow %q publish failed: %v", t.peerName, pubErr)), nil
	}

	if toolCtx.PauseResume {
		peerName := t.peerName
		result := tool.Pending(fmt.Sprintf("Waiting for workflow %q", peerName), tool.WithData(map[string]any{
			"sub_task_id":   reqID,
			"workflow_name": peerName,
			"pending_kind":  "workflow",
		}))
		result.OnDelivery = func(deliver func(*tool.Result)) {
			go func() {
				respMsg := <-completeCh
				if respMsg == nil {
					deliver(tool.Error(fmt.Sprintf("workflow %q request timed out or was cancelled", peerName)))
					return
				}
				parsed, _ := t.parseResponse(context.WithoutCancel(ctx), respMsg, toolCtx)
				deliver(parsed)
			}()
		}
		return result, nil
	}

	select {
	case respMsg := <-completeCh:
		if respMsg == nil {
			return tool.Error(fmt.Sprintf("workflow %q request timed out", t.peerName)), nil
		}
		return t.parseResponse(ctx, respMsg, toolCtx)
	case <-ctx.Done():
		t.agent.peerRouter.CancelWithPropagation(reqID, t.namespace)
		return tool.Error("workflow request cancelled"), nil
	}
}

func newWorkflowRunTaskID() string {
	return idgen.New()
}

func (t *workflowAgentTool) prepareInput(ctx context.Context, args map[string]any, toolCtx *tool.Context) ([]byte, string, error) {
	if inputArtifact, ok := args["input_artifact"].(string); ok && inputArtifact != "" {
		return nil, fmt.Sprintf("artifact://%s", inputArtifact), nil
	}

	paramArgs := make(map[string]any, len(args))
	for k, v := range args {
		if k != "input_artifact" {
			paramArgs[k] = v
		}
	}

	inputJSON, err := json.Marshal(paramArgs)
	if err != nil {
		return nil, "", fmt.Errorf("marshal input: %w", err)
	}

	if toolCtx.ArtifactService != nil {
		suffix := fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFF)
		filename := fmt.Sprintf("wi_%s_%s.json", sanitizeForFilename(t.peerName), suffix)

		result, saveErr := artifact.SaveWithMetadata(ctx, toolCtx.ArtifactService,
			toolCtx.AgentName, toolCtx.UserID, toolCtx.SessionID, filename,
			inputJSON, mimeJSON,
			artifact.MetadataOpts{
				Description:    fmt.Sprintf("Input for workflow %s", t.displayName()),
				Source:         "workflow_peer_invocation",
				SuppressSchema: true,
				Tags:           []string{a2a.ArtifactTagWorking},
			}, t.agent.logger)
		if saveErr != nil {
			t.agent.logger.Warn("failed to save workflow input artifact",
				"traceID", toolCtx.TraceID, "taskID", toolCtx.ParentTaskID,
				"filename", filename, "error", saveErr)
		} else {
			uri := fmt.Sprintf("artifact://%s:%d", filename, result.DataVersion)
			return nil, uri, nil
		}
	}

	return inputJSON, "", nil
}

func (t *workflowAgentTool) buildA2AMessage(inputJSON []byte, artifactURI string, contextID string, parentTaskID string, sessionID string) a2a.Message {
	var parts []a2a.Part

	sir := a2a.StructuredInvocationRequest{
		Type:                    "structured_invocation_request",
		WorkflowName:            t.agent.cfg.Name,
		NodeID:                  fmt.Sprintf("workflow_tool_%s", t.peerName),
		InputSchema:             t.inputSchema,
		SuggestedOutputFilename: fmt.Sprintf(a2a.WorkflowHandoffArtifactPrefix+"%s_result.json", t.peerName),
	}
	parts = append(parts, a2a.NewDataPart(sir))

	if artifactURI != "" {
		parts = append(parts, a2a.NewFilePart(artifactURI, "input.json", mimeJSON))
	} else if inputJSON != nil {
		b64 := base64.StdEncoding.EncodeToString(inputJSON)
		parts = append(parts, a2a.NewFilePartWithBytes(b64, "input.json", mimeJSON))
	}

	metadata := map[string]any{
		"sessionBehavior": "RUN_BASED",
		"parentTaskId":    parentTaskID,
		"agent_name":      t.peerName,
	}

	if vids := t.agent.resolvedVolumeIDsForSession(sessionID); len(vids) > 0 {
		metadata["volume_context"] = vids
	}

	return a2a.Message{
		Kind:      "message",
		MessageID: fmt.Sprintf("wf-%s-%d", t.peerName, time.Now().UnixNano()),
		ContextID: contextID,
		Role:      a2a.RoleUser,
		Parts:     parts,
		Metadata:  metadata,
	}
}

func (t *workflowAgentTool) parseResponse(ctx context.Context, respMsg *broker.Message, toolCtx *tool.Context) (*tool.Result, error) {
	var a2aResp a2a.Response
	if err := json.Unmarshal(respMsg.Payload, &a2aResp); err != nil {
		return tool.Error("failed to parse workflow response: " + err.Error()), nil
	}

	if a2aResp.Error != nil {
		return tool.Error(fmt.Sprintf("workflow error: %s", a2aResp.Error.Message)), nil
	}

	task, ok := a2aResp.Result.(map[string]any)
	if !ok {
		return tool.OK("Workflow completed (unexpected response format)"), nil
	}

	failed := delegatedTaskUnsuccessful(task)
	placeholder := func(detail string) *tool.Result {
		if failed {
			return delegatedTaskError(fmt.Sprintf("workflow %q did not succeed %s", t.peerName, detail))
		}
		return tool.OK("Workflow completed " + detail)
	}

	status, _ := task["status"].(map[string]any)
	if status == nil {
		return tool.OK("Workflow completed (no status)"), nil
	}

	respMsgMap, _ := status["message"].(map[string]any)
	if respMsgMap == nil {
		return placeholder("(no message)"), nil
	}

	parts, _ := respMsgMap["parts"].([]any)
	if parts == nil {
		return placeholder("(no parts)"), nil
	}

	var fallbackText string
	for _, p := range parts {
		partMap, ok := p.(map[string]any)
		if !ok {
			continue
		}

		switch partMap["kind"] {
		case "data":
			data, ok := partMap["data"].(map[string]any)
			if !ok {
				continue
			}
			if data["type"] == "structured_invocation_result" {
				return t.handleStructuredResult(ctx, data, toolCtx, failed)
			}
		case "text":
			if text, ok := partMap["text"].(string); ok && text != "" {
				fallbackText += text
			}
		}
	}

	if fallbackText == "" {
		return placeholder("(no content in response)"), nil
	}
	if failed {
		return delegatedTaskError(fallbackText), nil
	}
	return tool.OK(fallbackText), nil
}

func (t *workflowAgentTool) handleStructuredResult(ctx context.Context, data map[string]any, toolCtx *tool.Context, taskFailed bool) (*tool.Result, error) {
	resultStatus, _ := data["status"].(string)
	errMsg, _ := data["error_message"].(string)

	if resultStatus == "error" || taskFailed {
		if errMsg == "" {
			switch resultStatus {
			case "", "error":
				errMsg = "workflow execution failed"
			default:
				errMsg = fmt.Sprintf("workflow execution did not succeed (status %q)", resultStatus)
			}
		}
		return delegatedTaskError(errMsg), nil
	}

	resultData := map[string]any{
		"status": resultStatus,
	}

	if ref, ok := data["output_artifact_ref"].(map[string]any); ok {
		name, _ := ref["name"].(string)
		if name != "" {
			resultData["output_artifact"] = name

			if toolCtx.ArtifactService != nil {
				var version *int
				if v, ok := ref["version"].(float64); ok {
					iv := int(v)
					version = &iv
				}
				loc := toolCtx.ArtifactService.Resolve(
					t.peerName, toolCtx.UserID, toolCtx.SessionID, name,
				)
				content, _, loadErr := toolCtx.ArtifactService.Inner.Load(ctx, loc, version)
				if loadErr == nil && content != nil {
					var parsed map[string]any
					if json.Unmarshal(content, &parsed) == nil {
						resultData["output"] = parsed
					} else {
						resultData["output"] = string(content)
					}
				} else if loadErr != nil {
					t.agent.logger.Warn("failed to load workflow output artifact",
						"name", name, "error", loadErr)
				}
			}
		}
	}

	if errMsg != "" {
		resultData["partial_failure_message"] = errMsg
		return tool.OK("Workflow completed, but some nodes failed: "+errMsg, tool.WithData(resultData)), nil
	}
	return tool.OK("Workflow completed successfully", tool.WithData(resultData)), nil
}

func sanitizeForFilename(name string) string {
	var b strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			b.WriteRune(c)
		}
	}
	return b.String()
}
