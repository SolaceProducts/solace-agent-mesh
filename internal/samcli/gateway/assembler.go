// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gateway

type MessageAssembler struct {
	msg AssembledMessage
}

func NewMessageAssembler() *MessageAssembler {
	return &MessageAssembler{}
}

func (a *MessageAssembler) ProcessEvent(eventType string, data map[string]any) (AssembledMessage, string) {
	var newText string

	if errObj, ok := data["error"]; ok && errObj != nil {
		errMap, _ := errObj.(map[string]any)
		msg := "Unknown error"
		if errMap != nil {
			if m, ok := errMap["message"].(string); ok {
				msg = m
			}
		}
		a.msg.IsError = true
		a.msg.ErrorMessage = msg
		a.msg.IsComplete = true
		return a.msg, "\nError: " + msg
	}

	result, _ := data["result"].(map[string]any)
	if result == nil {
		return a.msg, ""
	}

	kind, _ := result["kind"].(string)

	switch kind {
	case "status-update":
		newText = a.processStatusUpdate(result)
	case "artifact-update":
		a.processArtifactUpdate(result)
	case "task":
		newText = a.processFinalTask(result)
	}

	return a.msg, newText
}

func (a *MessageAssembler) Message() AssembledMessage {
	return a.msg
}

func (a *MessageAssembler) processStatusUpdate(result map[string]any) string {
	if id, ok := result["taskId"].(string); ok {
		a.msg.TaskID = id
	}
	if id, ok := result["contextId"].(string); ok {
		a.msg.ContextID = id
	}

	status, _ := result["status"].(map[string]any)
	if status == nil {
		return ""
	}

	message, _ := status["message"].(map[string]any)
	if message == nil {
		return ""
	}

	parts, _ := message["parts"].([]any)

	var newText string
	for _, partRaw := range parts {
		part, _ := partRaw.(map[string]any)
		if part == nil {
			continue
		}

		partKind, _ := part["kind"].(string)
		switch partKind {
		case "text":
			text, _ := part["text"].(string)
			if text != "" {
				a.msg.TextParts = append(a.msg.TextParts, text)
				newText += text
			}
		case "data":
			dataObj, _ := part["data"].(map[string]any)
			if dataObj == nil {
				continue
			}
			dataType, _ := dataObj["type"].(string)
			switch dataType {
			case "agent_progress_update":
				if st, ok := dataObj["status_text"].(string); ok {
					a.msg.StatusText = st
				}
			case "user_input_request":
				a.msg.HILRequest = &HILRequest{
					RequestID: getStringField(dataObj, "request_id"),
					Source:    getStringField(dataObj, "source"),
					ExpiresAt: getStringField(dataObj, "expires_at"),
					Surface:   dataObj["surface"],
					TaskID:    a.msg.TaskID,
					AgentName: getAgentName(result),
				}
			case "authentication_required":
				a.msg.AuthRequest = &AuthRequest{
					AuthURI:           getStringField(dataObj, "auth_uri"),
					FunctionCallID:    getStringField(dataObj, "auth_request_function_call_id"),
					CredentialKey:     getStringField(dataObj, "credential_key"),
					TargetAgent:       getStringField(dataObj, "target_agent"),
					TargetAgentTaskID: getStringField(dataObj, "target_agent_task_id"),
				}
			}
		}
	}

	if final, ok := result["final"].(bool); ok && final {
		a.msg.IsComplete = true
	}

	return newText
}

func (a *MessageAssembler) processArtifactUpdate(result map[string]any) {
	artifact, _ := result["artifact"].(map[string]any)
	if artifact == nil {
		return
	}
	name, _ := artifact["name"].(string)
	if name == "" {
		return
	}

	for i, existing := range a.msg.Artifacts {
		if fn, _ := existing["filename"].(string); fn == name {
			for k, v := range artifact {
				a.msg.Artifacts[i][k] = v
			}
			return
		}
	}
	entry := map[string]any{"filename": name}
	for k, v := range artifact {
		entry[k] = v
	}
	a.msg.Artifacts = append(a.msg.Artifacts, entry)
}

func (a *MessageAssembler) SetLastSequence(seq int64) {
	a.msg.LastSequence = seq
}

func (a *MessageAssembler) ClearHILRequest() {
	a.msg.HILRequest = nil
}

func (a *MessageAssembler) ClearAuthRequest() {
	a.msg.AuthRequest = nil
}

func (a *MessageAssembler) processFinalTask(result map[string]any) string {
	if id, ok := result["id"].(string); ok {
		a.msg.TaskID = id
	}
	if id, ok := result["contextId"].(string); ok {
		a.msg.ContextID = id
	}
	a.msg.IsComplete = true

	status, _ := result["status"].(map[string]any)
	if status == nil {
		return ""
	}

	state, _ := status["state"].(string)
	if state == "failed" {
		a.msg.IsError = true
		reason := failureReason(result, status)
		if reason == "" {
			reason = "Unknown error"
		}
		a.msg.ErrorMessage = reason
		return "\nTask failed: " + reason
	}

	a.msg.DataParts = finalDataParts(status)
	return ""
}

func finalDataParts(status map[string]any) []any {
	message, _ := status["message"].(map[string]any)
	parts, _ := message["parts"].([]any)
	var dataParts []any
	for _, partRaw := range parts {
		part, _ := partRaw.(map[string]any)
		if part["kind"] != "data" || part["data"] == nil || isArtifactNotice(part) {
			continue
		}
		dataParts = append(dataParts, part["data"])
	}
	return dataParts
}

const artifactNoticeToolName = "_notify_artifact_save"

func isArtifactNotice(part map[string]any) bool {
	metadata, _ := part["metadata"].(map[string]any)
	return metadata["tool_name"] == artifactNoticeToolName
}

func failureReason(result, status map[string]any) string {
	if reason := reasonFromParts(status); reason != "" {
		return reason
	}
	metadata, _ := result["metadata"].(map[string]any)
	reason, _ := metadata["error"].(string)
	return reason
}

func reasonFromParts(status map[string]any) string {
	message, _ := status["message"].(map[string]any)
	parts, _ := message["parts"].([]any)
	var structuredReason string
	for _, partRaw := range parts {
		part, _ := partRaw.(map[string]any)
		switch part["kind"] {
		case "text":
			if text, _ := part["text"].(string); text != "" {
				return text
			}
		case "data":
			data, _ := part["data"].(map[string]any)
			reason, _ := data["error_message"].(string)
			if data["type"] == "structured_invocation_result" && structuredReason == "" {
				structuredReason = reason
			}
		}
	}
	return structuredReason
}

func getStringField(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getAgentName(result map[string]any) string {
	if meta, ok := result["metadata"].(map[string]any); ok {
		if name := getStringField(meta, "agent_name"); name != "" {
			return name
		}
	}
	if status, ok := result["status"].(map[string]any); ok {
		if msg, ok := status["message"].(map[string]any); ok {
			if meta, ok := msg["metadata"].(map[string]any); ok {
				return getStringField(meta, "agent_name")
			}
		}
	}
	return ""
}
