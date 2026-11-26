// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package visualization

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

type RichStreamEvent struct {
	EventType      string          `json:"event_type"`
	Timestamp      string          `json:"timestamp"`
	SolaceTopic    string          `json:"solace_topic"`
	Direction      string          `json:"direction"`
	SourceEntity   string          `json:"source_entity"`
	TargetEntity   string          `json:"target_entity"`
	MessageID      any             `json:"message_id"`
	TaskID         any             `json:"task_id"`
	PayloadSummary *PayloadSummary `json:"payload_summary"`
	FullPayload    map[string]any  `json:"full_payload"`
	DebugType      string          `json:"debug_type"`
}

type PayloadSummary struct {
	Method        string  `json:"method"`
	ParamsPreview *string `json:"params_preview"`
}

func InferEventDetails(topic string, payload map[string]any) *RichStreamEvent {
	evt := &RichStreamEvent{
		EventType:    "a2a_message",
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
		SolaceTopic:  topic,
		Direction:    "unknown",
		SourceEntity: "unknown",
		TargetEntity: "unknown",
		DebugType:    "unknown",
		MessageID:    payload["id"],
		FullPayload:  payload,
	}

	if eventType, ok := payload["event_type"].(string); ok {
		evt.Direction = "system_event"
		evt.DebugType = "sam_event"
		evt.PayloadSummary = &PayloadSummary{Method: eventType}
		if src, ok := payload["source_component"].(string); ok {
			evt.SourceEntity = src
		}
		evt.TargetEntity = "system"
		return evt
	}

	if result, hasResult := payload["result"]; hasResult {
		evt.inferFromResult(result, topic)
	} else if _, hasError := payload["error"]; hasError {
		evt.Direction = "error_response"
		evt.DebugType = "error"
	} else if method, ok := payload["method"].(string); ok {
		evt.inferFromRequest(method, payload)
	} else {
		evt.inferDirectionFromTopic(topic)
	}

	evt.buildPayloadSummary(payload)

	return evt
}

func (evt *RichStreamEvent) inferFromResult(result any, topic string) {
	resultMap, ok := result.(map[string]any)
	if !ok {
		evt.inferDirectionFromTopic(topic)
		return
	}

	kind, _ := resultMap["kind"].(string)
	if kind != "" {
		evt.Direction = kind
	} else {
		evt.inferDirectionFromTopic(topic)
	}

	if taskID, ok := resultMap["taskId"].(string); ok {
		evt.TaskID = taskID
	} else if taskID, ok := resultMap["task_id"].(string); ok {
		evt.TaskID = taskID
	} else if id, ok := resultMap["id"].(string); ok {
		evt.TaskID = id
	}

	if agentName := getNestedString(resultMap, "metadata", "agent_name"); agentName != "" {
		evt.SourceEntity = agentName
	}

	switch kind {
	case "status-update":
		evt.inferStatusDebugType(resultMap)
		if evt.SourceEntity == "unknown" {
			if agentName := getNestedString(resultMap, "status", "message", "metadata", "agent_name"); agentName != "" {
				evt.SourceEntity = agentName
			}
		}
		if evt.SourceEntity == "unknown" {
			parts := getNestedParts(resultMap, "status", "message", "parts")
			if name := firstDataPartEntityName(parts); name != "" {
				evt.SourceEntity = name
			}
		}
		if evt.SourceEntity == "unknown" {
			if name := agentFromTopic(topic); name != "" {
				evt.SourceEntity = name
			}
		}
	case "task":
		if dataType, ok := firstDataPartType(getNestedParts(resultMap, "status", "message", "parts")); ok {
			evt.DebugType = dataType
		} else if evt.DebugType == "unknown" {
			evt.DebugType = "task_result"
		}
		if evt.SourceEntity == "unknown" {
			if name := agentFromTopic(topic); name != "" {
				evt.SourceEntity = name
			}
		}
	case "artifact-update":
		if agentName := getNestedString(resultMap, "artifact", "metadata", "agent_name"); agentName != "" {
			evt.SourceEntity = agentName
		}
	}
}

func (evt *RichStreamEvent) inferStatusDebugType(resultMap map[string]any) {
	parts := getNestedParts(resultMap, "status", "message", "parts")
	if len(parts) == 0 {
		return
	}

	if dataType, ok := firstDataPartType(parts); ok {
		evt.DebugType = dataType
		return
	}

	allText := true
	for _, part := range parts {
		partMap, ok := part.(map[string]any)
		if !ok {
			allText = false
			break
		}
		if partMap["kind"] != "text" {
			allText = false
			break
		}
	}
	if allText {
		evt.DebugType = "streaming_text"
	}
}

func (evt *RichStreamEvent) inferFromRequest(method string, payload map[string]any) {
	evt.Direction = "request"
	evt.DebugType = method

	if id, ok := payload["id"].(string); ok {
		evt.TaskID = id
	}

	if params, ok := payload["params"].(map[string]any); ok {
		if msg, ok := params["message"].(map[string]any); ok {
			if agentName := getNestedString(msg, "metadata", "agent_name"); agentName != "" {
				evt.TargetEntity = agentName
			}
			parts, _ := msg["parts"].([]any)
			if dataType, ok := firstDataPartType(parts); ok {
				evt.DebugType = dataType
			}
		}
	}
}

func (evt *RichStreamEvent) inferDirectionFromTopic(topic string) {
	switch {
	case strings.Contains(topic, "/a2a/v1/discovery/"):
		evt.Direction = "discovery"
	case strings.Contains(topic, "/response/"):
		if evt.Direction == "unknown" {
			evt.Direction = "response"
		}
	case strings.Contains(topic, "/status/"):
		if evt.Direction == "unknown" {
			evt.Direction = "status_update"
		}
	case strings.Contains(topic, "/request/"):
		if evt.Direction == "unknown" {
			evt.Direction = "request"
		}
	}
}

func (evt *RichStreamEvent) buildPayloadSummary(payload map[string]any) {
	method, _ := payload["method"].(string)
	if method == "" {
		method = "N/A"
	}

	summary := &PayloadSummary{Method: method}

	var previewObj any
	if result, ok := payload["result"]; ok {
		previewObj = result
	} else if params, ok := payload["params"]; ok {
		previewObj = params
	} else if errObj, ok := payload["error"]; ok {
		previewObj = errObj
	}

	if previewObj != nil {
		if previewBytes, err := json.Marshal(previewObj); err == nil {
			preview := strutil.Truncate(string(previewBytes), 100, "...")
			summary.ParamsPreview = &preview
		}
	}

	evt.PayloadSummary = summary
}

func ShouldFilterTopic(topic string) bool {
	return strings.Contains(topic, "/discovery/") ||
		strings.Contains(topic, "/trust/") ||
		strings.Contains(topic, "/sam_remote_tool/") ||
		strings.Contains(topic, "/sam_skill/")
}

func ShouldFilterWorkingState(payload map[string]any) bool {
	result, ok := payload["result"].(map[string]any)
	if !ok {
		return false
	}
	status, ok := result["status"].(map[string]any)
	if !ok {
		return false
	}
	state, _ := status["state"].(string)
	if state != "working" {
		return false
	}

	msg, ok := status["message"].(map[string]any)
	if !ok {
		return false
	}
	parts, ok := msg["parts"].([]any)
	if !ok || len(parts) == 0 {
		return false
	}

	allText := true
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			allText = false
			break
		}
		if kind, _ := part["kind"].(string); kind != "text" {
			allText = false
			break
		}
	}
	if allText {
		return true
	}

	allInProgress := true
	for _, p := range parts {
		part, ok := p.(map[string]any)
		if !ok {
			allInProgress = false
			break
		}
		data, ok := part["data"].(map[string]any)
		if !ok {
			allInProgress = false
			break
		}
		if s, _ := data["status"].(string); s != "in-progress" {
			allInProgress = false
			break
		}
	}
	return allInProgress
}

func ShouldFilterWorkingStateEvent(event *a2a.TaskStatusUpdateEvent) bool {
	if event == nil || event.Status.State != a2a.TaskStateWorking {
		return false
	}
	msg := event.Status.Message
	if msg == nil || len(msg.Parts) == 0 {
		return false
	}

	allText := true
	for _, p := range msg.Parts {
		if p.Kind != "text" {
			allText = false
			break
		}
	}
	if allText {
		return true
	}

	for _, p := range msg.Parts {
		data, ok := p.Data.(map[string]any)
		if !ok {
			return false
		}
		if s, _ := data["status"].(string); s != "in-progress" {
			return false
		}
	}
	return true
}

func IsVisualizationExcluded(payload map[string]any) bool {
	if params, ok := payload["params"].(map[string]any); ok {
		if msg, ok := params["message"].(map[string]any); ok {
			if metadata, ok := msg["metadata"].(map[string]any); ok {
				return isVisFalse(metadata["visualization"])
			}
		}
	}
	return false
}

func isVisFalse(v any) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return !val
	case string:
		return strings.EqualFold(val, "false")
	}
	return false
}

func firstDataPartType(parts []any) (string, bool) {
	for _, p := range parts {
		partMap, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if partMap["kind"] != "data" {
			continue
		}
		data, ok := partMap["data"].(map[string]any)
		if !ok {
			continue
		}
		if dataType, ok := data["type"].(string); ok && dataType != "" {
			return dataType, true
		}
	}
	return "", false
}

func firstDataPartEntityName(parts []any) string {
	for _, p := range parts {
		partMap, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if partMap["kind"] != "data" {
			continue
		}
		data, ok := partMap["data"].(map[string]any)
		if !ok {
			continue
		}
		if name, ok := data["agent_name"].(string); ok && name != "" {
			return name
		}
		if name, ok := data["workflow_name"].(string); ok && name != "" {
			return name
		}
	}
	return ""
}

func agentFromTopic(topic string) string {
	parts := strings.Split(topic, "/")
	for i, seg := range parts {
		if seg != "agent" || i+2 >= len(parts) {
			continue
		}
		kind := parts[i+1]
		if kind != "status" && kind != "response" {
			continue
		}
		name := parts[i+2]
		if name == "" {
			continue
		}
		return name
	}
	return ""
}

func getNestedString(m map[string]any, keys ...string) string {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			s, _ := current[key].(string)
			return s
		}
		next, ok := current[key].(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func getNestedParts(m map[string]any, keys ...string) []any {
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			parts, _ := current[key].([]any)
			return parts
		}
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return nil
}

func ExtractEventUserID(userProps map[string]string, payload map[string]any) string {
	if uid := userProps["userId"]; uid != "" {
		return uid
	}

	return userIDFromClientControlledPayload(payload)
}

func userIDFromClientControlledPayload(payload map[string]any) string {
	if params, ok := payload["params"].(map[string]any); ok {
		if msg, ok := params["message"].(map[string]any); ok {
			if metadata, ok := msg["metadata"].(map[string]any); ok {
				if uid, ok := metadata["user_id"].(string); ok && uid != "" {
					return uid
				}
			}
		}
	}
	if result, ok := payload["result"].(map[string]any); ok {
		if metadata, ok := result["metadata"].(map[string]any); ok {
			if uid, ok := metadata["user_id"].(string); ok && uid != "" {
				return uid
			}
		}
		if status, ok := result["status"].(map[string]any); ok {
			if msg, ok := status["message"].(map[string]any); ok {
				if metadata, ok := msg["metadata"].(map[string]any); ok {
					if uid, ok := metadata["user_id"].(string); ok && uid != "" {
						return uid
					}
				}
			}
		}
	}
	return ""
}
