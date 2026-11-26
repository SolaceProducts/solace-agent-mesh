// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package taxonomy

var ToolCategories = []string{
	ToolCategoryBuiltin,
	ToolCategoryConnector,
	ToolCategoryCustom,
	ToolCategorySkill,
}

const (
	ToolCategoryBuiltin   = "builtin"
	ToolCategoryConnector = "connector"
	ToolCategoryCustom    = "custom"
	ToolCategorySkill     = "skill"
)

var BuiltinToolCategories = []string{
	"activity_reporting",
	"builder_tools",
	"builtin_artifact_tools",
	"builtin_code_execution",
	"builtin_diagram_tools",
	"builtin_document_tools",
	"builtin_file_tools",
	"builtin_image_tools",
	"builtin_media_tools",
	"builtin_research_tools",
	"builtin_time_tools",
	"builtin_volume_tools",
	"builtin_web_request_tools",
	"data_analysis",
	"general_agent_tools",
	"hil_tools",
	"notification_tools",
	"observability_analytics",
	"observability_datadog",
	"observability_platform_read",
	"scheduling_tools",
	BuiltinCategoryUngrouped,
}

const BuiltinCategoryUngrouped = "ungrouped"

var ConnectorCategories = []string{
	"api",
	"document_db",
	"email",
	"event_mesh",
	"graph_db",
	"knowledge_base",
	"mcp",
	"search",
	"slack",
	"sql",
	"web_search",
}

const (
	ModelTypeHosted    = "hosted"
	ModelTypeLocal     = "local"
	ModelTypeFreeModel = "free_model"
)

var ModelTypes = []string{ModelTypeHosted, ModelTypeLocal, ModelTypeFreeModel}

var ProviderLabels = []string{
	"anthropic",
	"azure",
	"bedrock",
	"bedrock_mantle",
	"byteplus",
	"cerebras",
	"cloudflare",
	"cohere",
	"databricks",
	"deepseek",
	"elevenlabs",
	"fireworks",
	"gemini",
	"github-copilot",
	"groq",
	"huggingface",
	"minimax",
	"mistral",
	"nebius",
	"ollama",
	"openai",
	"opencode-go",
	"opencode-zen",
	"openrouter",
	"other",
	"parasail",
	"perplexity",
	"replicate",
	"runware",
	"runway",
	"sagemaker",
	"sarvam",
	"sgl",
	"typesafe",
	"vertex",
	"vllm",
	"wafer",
	"xai",
}

const (
	OutcomeSuccess   = "success"
	OutcomeError     = "error"
	OutcomeCancelled = "cancelled"
	OutcomeTimeout   = "timeout"
)

var EvalRunOutcomes = []string{OutcomeSuccess, OutcomeError, OutcomeCancelled, OutcomeTimeout}

const (
	EvalTargetTypeAgent    = "agent"
	EvalTargetTypeWorkflow = "workflow"
)

var EvalTargetTypes = []string{EvalTargetTypeAgent, EvalTargetTypeWorkflow}

const (
	EvaluatorTypeLLMJudge = "llm_judge"
	EvaluatorTypeDecision = "decision"
)

var EvaluatorTypes = []string{EvaluatorTypeLLMJudge, EvaluatorTypeDecision}

const EvaluatorTypeHeuristic = "heuristic"

var EvalRunEvaluatorTypes = []string{EvaluatorTypeLLMJudge, EvaluatorTypeDecision, EvaluatorTypeHeuristic}

const (
	EvalTrialsSingle   = "single"
	EvalTrialsMultiple = "multiple"
)

var EvalTrialsPerExample = []string{EvalTrialsSingle, EvalTrialsMultiple}

const (
	EvalTrialNodeFailed     = "node_failed"
	EvalTrialPausedForInput = "paused_for_input"
	EvalTrialTimeout        = "timeout"
	EvalTrialCancelled      = "cancelled"
	EvalTrialAuthRequired   = "auth_required"
	EvalTrialInvalidInput   = "invalid_input"
	EvalTrialRejected       = "rejected"
)

var EvalWorkflowTrialFailureKinds = []string{
	EvalTrialNodeFailed,
	EvalTrialPausedForInput,
	EvalTrialTimeout,
	EvalTrialCancelled,
	EvalTrialAuthRequired,
	EvalTrialInvalidInput,
	EvalTrialRejected,
}

var DecisionQuestionTypes = []string{"boolean", "choice", "score"}

const (
	EscalationNone            = "none"
	EscalationOnError         = "on_error"
	EscalationOnLowConfidence = "on_low_confidence"
)

var EscalationModes = []string{EscalationNone, EscalationOnError, EscalationOnLowConfidence}

const (
	EscalationReasonLowConfidence = "low_confidence"
	EscalationReasonNoConfidence  = "no_confidence"
	EscalationReasonProviderError = "provider_error"
	EscalationReasonModelLookup   = "model_lookup_failed"
)

var EscalationReasons = []string{
	EscalationReasonLowConfidence,
	EscalationReasonNoConfidence,
	EscalationReasonProviderError,
	EscalationReasonModelLookup,
}

const (
	ModelKindChat     = "chat"
	ModelKindDecision = "decision"
)

var ModelKinds = []string{ModelKindChat, ModelKindDecision}

const ErrorCategoryUnknown = "unknown"

const ErrorCategoryBudgetExhausted = "budget_exhausted"

var ErrorCategories = []string{
	"auth_failure",
	"rate_limit",
	"timeout",
	"context_overflow",
	"content_filtered",
	"free_llm_terms_required",
	"free_llm_paused",
	"allowance_exhausted",
	ErrorCategoryBudgetExhausted,
	ErrorCategoryUnknown,
}

const (
	DispatchClassTimeout            = "timeout"
	DispatchClassCanceled           = "canceled"
	DispatchClassValidation         = "validation"
	DispatchClassBackend4xx         = "backend_4xx"
	DispatchClassBackend5xx         = "backend_5xx"
	DispatchClassAuthRequired       = "auth_required"
	DispatchClassInternal           = "internal"
	DispatchClassNetworkUnreachable = "network_unreachable"
	DispatchClassError              = "error"

	DispatchClassUserInputUnanswered    = "user_input_unanswered"
	DispatchClassUserInputUnavailable   = "user_input_unavailable"
	DispatchClassUserInputUndeliverable = "user_input_undeliverable"
)

var UserInputClasses = []string{
	DispatchClassUserInputUnanswered,
	DispatchClassUserInputUnavailable,
	DispatchClassUserInputUndeliverable,
}

var ToolReportedClasses = []string{
	DispatchClassNetworkUnreachable,
}

var DispatchErrorClasses = []string{
	DispatchClassAuthRequired,
	DispatchClassBackend4xx,
	DispatchClassBackend5xx,
	DispatchClassCanceled,
	DispatchClassError,
	DispatchClassInternal,
	DispatchClassNetworkUnreachable,
	DispatchClassTimeout,
	DispatchClassUserInputUnanswered,
	DispatchClassUserInputUnavailable,
	DispatchClassUserInputUndeliverable,
	DispatchClassValidation,
}

var GatewayTypes = []string{
	"rest",
	"webhook",
	"slack",
	"teams",
	"event_mesh",
	"email",
	"whatsapp",
	"google_chat",
	"mcp",
	"agent_link",
}

const (
	RejectMalformed         = "malformed"
	RejectAgentNotFound     = "agent_not_found"
	RejectForbidden         = "forbidden"
	RejectInternal          = "internal"
	RejectBudgetExhausted   = "budget_exhausted"
	RejectBudgetUnconfirmed = "budget_unconfirmed"
)

var RejectReasons = []string{
	RejectMalformed,
	RejectAgentNotFound,
	RejectForbidden,
	RejectInternal,
	RejectBudgetExhausted,
	RejectBudgetUnconfirmed,
}

func ParseArrivingStringSet(v any, limit int) ([]string, bool) {
	switch vals := v.(type) {
	case []string:
		if limit >= 0 && len(vals) > limit {
			vals = vals[:limit]
		}
		return vals, true
	case []any:
		if limit >= 0 && len(vals) > limit {
			vals = vals[:limit]
		}
		out := make([]string, 0, len(vals))
		for _, item := range vals {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}
