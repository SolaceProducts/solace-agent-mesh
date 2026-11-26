// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agentlink

import (
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

var uiActedTypes = map[string]bool{
	string(a2a.SignalToolInvocationStart): true, string(a2a.SignalToolResult): true,
	string(a2a.SignalLLMInvocation): true, string(a2a.SignalLLMResponse): true,
	string(a2a.SignalAgentProgressUpdate): true, string(a2a.SignalArtifactCreationProgress): true,
	string(a2a.SignalArtifactSaved): true, string(a2a.SignalTemplateBlock): true,
	string(a2a.SignalStructuredInvocationReq): true, string(a2a.SignalStructuredInvocationRes): true,
	string(a2a.SignalWorkflowExecutionStart): true, string(a2a.SignalWorkflowNodeExecStart): true,
	string(a2a.SignalWorkflowNodeExecResult): true, string(a2a.SignalWorkflowMapProgress): true,
	string(a2a.SignalWorkflowExecutionResult): true, string(a2a.SignalDeepResearchProgress): true,
	string(a2a.SignalRAGInfoUpdate): true, string(a2a.SignalDeepResearchReport): true,
	string(a2a.SignalDeepResearchPlan): true, string(a2a.SignalDeepResearchPlanStale): true,
	string(a2a.SignalAuthenticationRequired): true, string(a2a.SignalAuthenticationResult): true,
	string(a2a.SignalAuthResponse): true, string(a2a.SignalUserInputRequest): true,
	string(a2a.SignalUserInputResponse): true, string(a2a.SignalThinkingContent): true,
	string(a2a.SignalBuilderComponentProgress): true,
	entrypoint.TaskTimeoutWarningType:          true, store.ChatTaskKindNotification: true,
	"redirect": true, "request_build_activate": true,
}

var errUIShape = errors.New("holds an object with the type of a SAM signal")

var taskRecordKeys = []string{"node_id", "node_type", "workflow_name", "trace_id", "retry_group_id", "retry_attempt"}

func carriesUIShape(v any) bool {
	return anyObject(v, func(object map[string]any) bool {
		kind, _ := object["type"].(string)
		return uiActedTypes[kind]
	})
}

func taskRecordKeyIn(v any) string {
	var found string
	anyObject(v, func(object map[string]any) bool {
		metadata, ok := object["metadata"].(map[string]any)
		if !ok {
			return false
		}
		for _, key := range taskRecordKeys {
			if _, present := metadata[key]; present {
				found = key
				return true
			}
		}
		return false
	})
	return found
}

func anyObject(v any, match func(map[string]any) bool) bool {
	switch v := v.(type) {
	case map[string]any:
		if match(v) {
			return true
		}
		for _, child := range v {
			if anyObject(child, match) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if anyObject(child, match) {
				return true
			}
		}
	}
	return false
}
