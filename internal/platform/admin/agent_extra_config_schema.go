// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

type agentExtraConfigType int

const (
	extraTypeAny agentExtraConfigType = iota
	extraTypeBool
	extraTypeInt
	extraTypeNumber
	extraTypeString
	extraTypeStringList
	extraTypeObject
	extraTypeList
	extraTypeStringOrObject
)

type agentExtraConfigKey struct {
	Type       agentExtraConfigType
	Nested     map[string]agentExtraConfigKey
	IntMin     *int
	RuntimeKey string
}

var agentExtraConfigSchema = map[string]agentExtraConfigKey{
	"agentType":    {Type: extraTypeString, RuntimeKey: "agent_type"},
	"internal":     {Type: extraTypeBool},
	"showSessions": {Type: extraTypeBool, RuntimeKey: "show_sessions"},
	"description":  {Type: extraTypeString},
	"version":      {Type: extraTypeString},

	"supportsStreaming":  {Type: extraTypeBool, RuntimeKey: "supports_streaming"},
	"maxTokens":          {Type: extraTypeInt, RuntimeKey: "max_tokens"},
	"maxLlmCallsPerTask": {Type: extraTypeInt, RuntimeKey: "max_llm_calls_per_task", IntMin: new(1)},
	"model":              {Type: extraTypeStringOrObject},

	"globalInstruction":    {Type: extraTypeString, RuntimeKey: "global_instruction"},
	"injectSystemPurpose":  {Type: extraTypeBool, RuntimeKey: "inject_system_purpose"},
	"injectResponseFormat": {Type: extraTypeBool, RuntimeKey: "inject_response_format"},
	"injectUserProfile":    {Type: extraTypeBool, RuntimeKey: "inject_user_profile"},
	"injectCurrentTime":    {Type: extraTypeBool, RuntimeKey: "inject_current_time"},

	"streamBatchingThresholdBytes":  {Type: extraTypeInt, RuntimeKey: "stream_batching_threshold_bytes"},
	"streamBatchingFlushIntervalMs": {Type: extraTypeInt, RuntimeKey: "stream_batching_flush_interval_ms"},
	"maxMessageSizeBytes":           {Type: extraTypeInt, RuntimeKey: "max_message_size_bytes"},

	"artifactHandlingMode":         {Type: extraTypeString, RuntimeKey: "artifact_handling_mode"},
	"textArtifactContentMaxLength": {Type: extraTypeInt, RuntimeKey: "text_artifact_content_max_length"},

	"toolResultAutoArtifactThresholdBytes": {Type: extraTypeInt, RuntimeKey: "tool_result_auto_artifact_threshold_bytes"},
	"toolOutputSaveThresholdBytes":         {Type: extraTypeInt, RuntimeKey: "tool_output_save_threshold_bytes"},
	"toolResultInlineTruncationBytes":      {Type: extraTypeInt, RuntimeKey: "tool_result_inline_truncation_bytes"},
	"toolOutputLlmReturnMaxBytes":          {Type: extraTypeInt, RuntimeKey: "tool_output_llm_return_max_bytes"},
	"toolResultPreviewLengthChars":         {Type: extraTypeInt, RuntimeKey: "tool_result_preview_length_chars"},
	"toolResultProcessingEnabled":          {Type: extraTypeBool, RuntimeKey: "tool_result_processing_enabled"},
	"schemaMaxKeys":                        {Type: extraTypeInt, RuntimeKey: "schema_max_keys"},
	"schemaInferenceDepth":                 {Type: extraTypeInt, RuntimeKey: "schema_inference_depth"},

	"enableInlineVision":     {Type: extraTypeBool, RuntimeKey: "enable_inline_vision"},
	"maxInlineVisionImages":  {Type: extraTypeInt, RuntimeKey: "max_inline_vision_images"},
	"maxInlineVisionBytes":   {Type: extraTypeInt, RuntimeKey: "max_inline_vision_bytes"},
	"maxInlineArtifactBytes": {Type: extraTypeInt, RuntimeKey: "max_inline_artifact_bytes"},

	"enableInlinePdf":   {Type: extraTypeBool, RuntimeKey: "enable_inline_pdf"},
	"maxInlinePdfBytes": {Type: extraTypeInt, RuntimeKey: "max_inline_pdf_bytes"},
	"maxInlinePdfPages": {Type: extraTypeInt, RuntimeKey: "max_inline_pdf_pages"},
	"maxInlinePdfCount": {Type: extraTypeInt, RuntimeKey: "max_inline_pdf_count"},

	"mcpToolResponseSaveThresholdBytes": {Type: extraTypeInt, RuntimeKey: "mcp_tool_response_save_threshold_bytes"},
	"mcpToolLlmReturnMaxBytes":          {Type: extraTypeInt, RuntimeKey: "mcp_tool_llm_return_max_bytes"},
	"mcpIntelligentProcessing":          {Type: extraTypeObject, RuntimeKey: "mcp_intelligent_processing"},

	"enableEmbedResolution":            {Type: extraTypeBool, RuntimeKey: "enable_embed_resolution"},
	"enableArtifactContentInstruction": {Type: extraTypeBool, RuntimeKey: "enable_artifact_content_instruction"},
	"enableTemplateInstructions":       {Type: extraTypeBool, RuntimeKey: "enable_template_instructions"},
	"enableExamples":                   {Type: extraTypeBool, RuntimeKey: "enable_examples"},
	"headless":                         {Type: extraTypeBool},
	"enableAutoContinuation":           {Type: extraTypeBool, RuntimeKey: "enable_auto_continuation"},
	"autoInjectArtifactTools":          {Type: extraTypeBool, RuntimeKey: "auto_inject_artifact_tools"},
	"autoInjectDataAnalysisTools":      {Type: extraTypeBool, RuntimeKey: "auto_inject_data_analysis_tools"},

	"maxCallDepth": {Type: extraTypeInt, RuntimeKey: "max_call_depth"},

	"maxSubtaskFanout": {Type: extraTypeInt, RuntimeKey: "max_subtask_fanout"},

	"maxMessageRedelivery": {Type: extraTypeInt, RuntimeKey: "max_message_redelivery"},

	"respectMessageTtl": {Type: extraTypeBool, RuntimeKey: "respect_message_ttl"},

	"autoSummarization": {
		Type:       extraTypeObject,
		RuntimeKey: "auto_summarization",
		Nested: map[string]agentExtraConfigKey{
			"enabled":              {Type: extraTypeBool},
			"compactionPercentage": {Type: extraTypeNumber, RuntimeKey: "compaction_percentage"},
			"summaryMaxTokens":     {Type: extraTypeInt, RuntimeKey: "summary_max_tokens"},
		},
	},

	"inputSchema":          {Type: extraTypeObject, RuntimeKey: "input_schema"},
	"outputSchema":         {Type: extraTypeObject, RuntimeKey: "output_schema"},
	"validationMaxRetries": {Type: extraTypeInt, RuntimeKey: "validation_max_retries"},

	"agentCard": {
		Type:       extraTypeObject,
		RuntimeKey: "agent_card",
		Nested: map[string]agentExtraConfigKey{
			"description":        {Type: extraTypeString},
			"version":            {Type: extraTypeString},
			"documentationUrl":   {Type: extraTypeString},
			"defaultInputModes":  {Type: extraTypeStringList},
			"defaultOutputModes": {Type: extraTypeStringList},
			"provider":           {Type: extraTypeObject},
			"skills":             {Type: extraTypeList},
			"priority":           {Type: extraTypeInt},
			"welcome": {
				Type: extraTypeObject,
				Nested: map[string]agentExtraConfigKey{
					"message": {Type: extraTypeString},
					"suggestions": {
						Type: extraTypeList,
						Nested: map[string]agentExtraConfigKey{
							"label":    {Type: extraTypeString},
							"prompt":   {Type: extraTypeString},
							"autoSend": {Type: extraTypeBool, RuntimeKey: "auto_send"},
						},
					},
				},
			},
		},
	},

	"agentCardPublishing": {
		Type:       extraTypeObject,
		RuntimeKey: "agent_card_publishing",
		Nested: map[string]agentExtraConfigKey{
			"intervalSeconds": {Type: extraTypeInt, RuntimeKey: "interval_seconds", IntMin: new(int)},
		},
	},

	"interAgentCommunication": {
		Type:       extraTypeObject,
		RuntimeKey: "inter_agent_communication",
		Nested: map[string]agentExtraConfigKey{
			"allowList":                     {Type: extraTypeStringList, RuntimeKey: "allow_list"},
			"denyList":                      {Type: extraTypeStringList, RuntimeKey: "deny_list"},
			"requestTimeoutSeconds":         {Type: extraTypeInt, RuntimeKey: "request_timeout_seconds", IntMin: new(int)},
			"workflowRequestTimeoutSeconds": {Type: extraTypeInt, RuntimeKey: "workflow_request_timeout_seconds", IntMin: new(int)},
			"subTaskTimeoutSeconds":         {Type: extraTypeInt, RuntimeKey: "sub_task_timeout_seconds", IntMin: new(int)},
		},
	},

	"agentDiscovery": {
		Type:       extraTypeObject,
		RuntimeKey: "agent_discovery",
		Nested: map[string]agentExtraConfigKey{
			"enabled":                    {Type: extraTypeBool},
			"healthCheckTtlSeconds":      {Type: extraTypeInt, RuntimeKey: "health_check_ttl_seconds", IntMin: new(int)},
			"healthCheckIntervalSeconds": {Type: extraTypeInt, RuntimeKey: "health_check_interval_seconds", IntMin: new(int)},
		},
	},

	"skillsBasePath":  {Type: extraTypeString, RuntimeKey: "skills_base_path"},
	"skillsCachePath": {Type: extraTypeString, RuntimeKey: "skills_cache_path"},

	"volumes": {Type: extraTypeList},

	"networks": {Type: extraTypeList},

	"requiredScopes": {Type: extraTypeStringList, RuntimeKey: "required_scopes"},

	"agent_init_function":    {Type: extraTypeString},
	"agent_cleanup_function": {Type: extraTypeString},

	"maxConcurrentTasks": {Type: extraTypeInt, RuntimeKey: "max_concurrent_tasks"},
}

func runtimeKeyOf(canonical string, entry agentExtraConfigKey) string {
	if entry.RuntimeKey != "" {
		return entry.RuntimeKey
	}
	return canonical
}
