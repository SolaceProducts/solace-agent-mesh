// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"slices"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type EventName = string

const (
	EventAppLaunched        EventName = "app:launched"
	EventAgentCreationStart EventName = "agent:creation_started"
	EventChatFirstResponse  EventName = "chat:first_response"

	EventBuilderOpened    EventName = "builder:opened"
	EventBuilderCompleted EventName = "builder:completed"

	EventFormValidationError EventName = "form:validation_error"
	EventErrorDisplayed      EventName = "error:displayed"
	EventPageView            EventName = "page_view"
)

const (
	EventActivationSignInClicked    EventName = "activation:sign_in_clicked"
	EventActivationSignInFailed     EventName = "activation:sign_in_failed"
	EventActivationSignedIn         EventName = "activation:signed_in"
	EventActivationTermsShown       EventName = "activation:terms_shown"
	EventActivationTermsError       EventName = "activation:terms_error"
	EventActivationReplaceConfirmed EventName = "activation:replace_confirmed"
	EventActivationSetupChosen      EventName = "activation:setup_chosen"
	EventActivationAccepted         EventName = "activation:accepted"
	EventActivationAcceptFailed     EventName = "activation:accept_failed"

	EventFreeModelCreditsLow  EventName = "free_model:credits_low"
	EventFreeModelCreditsOut  EventName = "free_model:credits_out"
	EventFreeModelSignInAgain EventName = "free_model:sign_in_again_clicked"
)

const PropSetupPath = "setupPath"

const (
	PropAlreadyAgreed     = "alreadyAgreed"
	traitReturningAccount = "returningAccount"
)

var SetupPaths = []string{"free_model", "own_model"}

const PropSignInFailure = "signInFailure"

var SignInFailures = []string{
	"canceled",
	"denied",
	"different_account",
	"failed",
	"grant_in_use",
	"no_callback_port",
	"no_refresh_token",
	"not_saved",
	"request_failed",
	"timed_out",
	"unavailable",
	"other",
}

const (
	EventCLICommandRun EventName = "cli:command_run"
)

const (
	EventAgentRunCompleted  EventName = "agent:run_completed"
	EventToolUsed           EventName = "tool:used"
	EventAgentPeerDelegated EventName = "agent:peer_delegated"

	EventModelUsed EventName = "model:used"

	EventTaskSubmitRejected EventName = "task:submit_rejected"
)

const (
	EventModelConfigured  EventName = "model:configured"
	EventAgentCreated     EventName = "agent:created"
	EventAgentDeployed    EventName = "agent:deployed"
	EventConnectorCreated EventName = "connector:created"
	EventGatewayCreated   EventName = "gateway:created"
	EventToolsetCreated   EventName = "toolset:created"

	EventWorkflowCreated  EventName = "workflow:created"
	EventWorkflowDeployed EventName = "workflow:deployed"

	EventAgentDeleted     EventName = "agent:deleted"
	EventConnectorDeleted EventName = "connector:deleted"
	EventGatewayDeleted   EventName = "gateway:deleted"
)

const EventEntrypointUsed EventName = "entrypoint:used"

const EventChatSuggestionUsed EventName = "chat:suggestion_used"

const EventEvalCreated EventName = "eval:created"

const EventEvalRun EventName = "eval:run"

const EventEvaluatorCreated EventName = "evaluator:created"

const EventConsentChanged EventName = "consent:changed"

const EventInstallInventory EventName = "install:inventory"

const (
	PropInterface     = "interface"
	PropCommand       = "command"
	PropOutcome       = "outcome"
	PropAgentType     = "agentType"
	PropErrorCategory = "errorCategory"
	PropErrorSource   = "errorSource"
	PropProvider      = "provider"

	PropSubmittedFrom = "submittedFrom"

	PropRejectReason      = "rejectReason"
	PropModel             = "model"
	PropToolModels        = "toolModels"
	PropToolCategory      = "category"
	PropConnectorCategory = "connectorCategory"
	PropBuiltinCategory   = "builtinCategory"
	PropEntrypointType    = "entrypointType"
	PropRunOrigin         = "runOrigin"
	PropModelType         = "modelType"
	PropCategory          = "category"
	PropGatewayType       = "gatewayType"
	PropTraceID           = "traceId"
	propPath              = "path"
	PropConsentPrevious   = "consentPrevious"
	PropConsentNew        = "consentNew"
	PropSuggestionLabel   = "suggestionLabel"
	PropModelKind         = "modelKind"
)

const (
	PropTargetType    = "targetType"
	PropEvaluatorType = "evaluatorType"
	PropQuestionType  = "questionType"
	PropEscalation    = "escalation"
)

const (
	PropEvaluatorTypes            = "evaluatorTypes"
	PropTrialsPerExample          = "trialsPerExample"
	PropWorkflowTrialFailureKinds = "workflowTrialFailureKinds"
)

const (
	PropDecisionEvaluatorCount = "decisionEvaluatorCount"
	PropDecisionCasesDecided   = "decisionCasesDecided"
	PropDecisionCasesEscalated = "decisionCasesEscalated"
	PropDecisionCasesErrored   = "decisionCasesErrored"

	PropDecisionEscalatedLowConfidence = "decisionEscalatedLowConfidence"
	PropDecisionEscalatedNoConfidence  = "decisionEscalatedNoConfidence"
	PropDecisionEscalatedProviderError = "decisionEscalatedProviderError"
	PropDecisionEscalatedModelLookup   = "decisionEscalatedModelLookupFailed"
)

var DecisionEscalationReasonFields = map[string]string{
	taxonomy.EscalationReasonLowConfidence: PropDecisionEscalatedLowConfidence,
	taxonomy.EscalationReasonNoConfidence:  PropDecisionEscalatedNoConfidence,
	taxonomy.EscalationReasonProviderError: PropDecisionEscalatedProviderError,
	taxonomy.EscalationReasonModelLookup:   PropDecisionEscalatedModelLookup,
}

var ToolCategories = taxonomy.ToolCategories

const (
	ToolCategoryBuiltin   = taxonomy.ToolCategoryBuiltin
	ToolCategoryConnector = taxonomy.ToolCategoryConnector
	ToolCategoryCustom    = taxonomy.ToolCategoryCustom
	ToolCategorySkill     = taxonomy.ToolCategorySkill
)

var RunOrigins = []string{
	RunOriginInteractive,
	RunOriginMachineIngress,
	RunOriginScheduled,
}

const (
	RunOriginInteractive    = "interactive"
	RunOriginMachineIngress = "machine_ingress"
	RunOriginScheduled      = "scheduled"
)

var ConsentStates = []string{
	ConsentStateGranted,
	ConsentStateWithdrawn,
	ConsentStateUnset,
}

const (
	ConsentStateGranted   = "granted"
	ConsentStateWithdrawn = "withdrawn"
	ConsentStateUnset     = "unset"
)

var SuggestionLabels = []string{
	SuggestionLabelWrite,
	SuggestionLabelLearn,
	SuggestionLabelCode,
	SuggestionLabelBrainstorm,
	SuggestionLabelSurpriseMe,
	SuggestionLabelAgentCustom,
}

const (
	SuggestionLabelWrite       = "write"
	SuggestionLabelLearn       = "learn"
	SuggestionLabelCode        = "code"
	SuggestionLabelBrainstorm  = "brainstorm"
	SuggestionLabelSurpriseMe  = "surprise_me"
	SuggestionLabelAgentCustom = "agent_custom"
)

var BuiltinToolCategories = taxonomy.BuiltinToolCategories

const BuiltinCategoryUngrouped = taxonomy.BuiltinCategoryUngrouped

const (
	PropAgentCount                  = "agentCount"
	PropWorkflowCount               = "workflowCount"
	PropConnectorCount              = "connectorCount"
	PropEntrypointCount             = "entrypointCount"
	PropToolsetCount                = "toolsetCount"
	PropConfiguredModelCount        = "configuredModelCount"
	PropAgentsWithCustomToolsets    = "agentsWithCustomToolsets"
	PropWorkflowsWithCustomToolsets = "workflowsWithCustomToolsets"
	PropConnectorCategories         = "connectorCategories"
	PropEntrypointTypes             = "entrypointTypes"
)

const (
	PropDeployedAgentCount                  = "deployedAgentCount"
	PropDeployedWorkflowCount               = "deployedWorkflowCount"
	PropDeployedAgentsWithCustomToolsets    = "deployedAgentsWithCustomToolsets"
	PropDeployedWorkflowsWithCustomToolsets = "deployedWorkflowsWithCustomToolsets"
)

const (
	PropBuiltinAgentCount         = "builtinAgentCount"
	PropDeployedBuiltinAgentCount = "deployedBuiltinAgentCount"
)

const (
	ModelTypeHosted = taxonomy.ModelTypeHosted
	ModelTypeLocal  = taxonomy.ModelTypeLocal
)

var ModelTypes = taxonomy.ModelTypes

const (
	OutcomeSuccess   = taxonomy.OutcomeSuccess
	OutcomeError     = taxonomy.OutcomeError
	OutcomeCancelled = taxonomy.OutcomeCancelled
	OutcomeTimeout   = taxonomy.OutcomeTimeout
)

var EvalRunOutcomes = taxonomy.EvalRunOutcomes

var (
	EvaluatorTypes        = taxonomy.EvaluatorTypes
	EvalTargetTypes       = taxonomy.EvalTargetTypes
	DecisionQuestionTypes = taxonomy.DecisionQuestionTypes
	EscalationModes       = taxonomy.EscalationModes
	ModelKinds            = taxonomy.ModelKinds

	EvalRunEvaluatorTypes         = taxonomy.EvalRunEvaluatorTypes
	EvalTrialsPerExample          = taxonomy.EvalTrialsPerExample
	EvalWorkflowTrialFailureKinds = taxonomy.EvalWorkflowTrialFailureKinds
)

const maxEvalRunSetElements = 8

const (
	AgentTypeBuiltin     = "builtin"
	AgentTypeUserCreated = "user_created"
	AgentTypeWorkflow    = "workflow"

	AgentTypeRemote = "remote"
)

var AgentCreationTypes = []string{AgentTypeUserCreated, AgentTypeRemote}

var ErrorCategories = taxonomy.ErrorCategories

const ErrorCategoryUnknown = taxonomy.ErrorCategoryUnknown

var ErrorSources = []string{
	ErrorSourceAPI,
	ErrorSourceAgentTask,
	ErrorSourceClient,
}

const (
	ErrorSourceAPI       = "api"
	ErrorSourceAgentTask = "agent_task"
	ErrorSourceClient    = "client"
)

var ProviderLabels = taxonomy.ProviderLabels

var GatewayTypes = taxonomy.GatewayTypes

var ConnectorCategories = taxonomy.ConnectorCategories

var FormNames = []string{
	"agent",
	"connector",
	"entrypoint",
	"model",
}

var FormFields = []string{
	"alias",
	"apiBase",
	"apiKey",
	"apiVersion",
	"authType",
	"awsAccessKeyId",
	"awsRegionName",
	"awsSecretAccessKey",
	"awsSessionToken",
	"cache_strategy",
	"clientId",
	"clientSecret",
	"customHeaders",
	"customParams",
	"description",
	"maxInputTokens",
	"max_tokens",
	"modelName",
	"modelPrefix",
	"name",
	"oauthAudience",
	"oauthCaCert",
	"oauthMaxRetries",
	"oauthScope",
	"oauthTokenRefreshBufferSeconds",
	"provider",
	"resources",
	"selectedConnectors",
	"selectedModel",
	"selectedToolsets",
	"slug",
	"systemPrompt",
	"tenantId",
	"tokenUrl",
	"values",
	"values.access_token",
	"values.addon_service_account_email",
	"values.allow_list",
	"values.allowedRedirectUris",
	"values.allowed_email_domains",
	"values.allowed_senders",
	"values.api_base",
	"values.api_key",
	"values.app_secret",
	"values.app_token",
	"values.authMode",
	"values.authToken",
	"values.authTokenLocation",
	"values.authTokenName",
	"values.auth_apikey_location",
	"values.auth_apikey_name",
	"values.auth_apikey_value",
	"values.auth_audience_type",
	"values.auth_http_basic_password",
	"values.auth_http_basic_username",
	"values.auth_http_bearer_token",
	"values.auth_http_scheme",
	"values.auth_mode",
	"values.auth_oauth_authorization_url",
	"values.auth_oauth_client_id",
	"values.auth_oauth_client_secret",
	"values.auth_oauth_mode",
	"values.auth_oauth_pkce",
	"values.auth_oauth_refresh_url",
	"values.auth_oauth_scopes",
	"values.auth_oauth_token_endpoint_auth_method",
	"values.auth_oauth_token_url",
	"values.auth_type",
	"values.aws_access_key_id",
	"values.aws_account_id",
	"values.aws_secret_access_key",
	"values.base_url",
	"values.bot_token",
	"values.broker_password",
	"values.broker_url",
	"values.broker_username",
	"values.broker_vpn",
	"values.cloud_id",
	"values.collection",
	"values.connection_type",
	"values.contact_request_template",
	"values.contact_request_template_language",
	"values.corsAllowedOrigins",
	"values.custom_headers",
	"values.database",
	"values.defaultUserIdentity",
	"values.default_agent_name",
	"values.delivery",
	"values.delivery_mode",
	"values.deny_list",
	"values.disable_file_uploads",
	"values.enableAuth",
	"values.enableFileInput",
	"values.enable_typing_indicator",
	"values.encrypt",
	"values.endpoint_url",
	"values.envelope_from",
	"values.envelope_from_login",
	"values.envelope_from_none",
	"values.event_rules",
	"values.excludeTools",
	"values.external_id",
	"values.feedback_enabled",
	"values.thread_history_limit",
	"values.artifact_drive_folder_id",
	"values.artifact_drive_access_days",
	"values.file_s3_key",
	"values.hil",
	"values.hmacAlgorithm",
	"values.hmacEncoding",
	"values.hmacHeader",
	"values.hmacPrefix",
	"values.hmacSecret",
	"values.hostname",
	"values.identity_claim_key",
	"values.identity_lookup_key",
	"values.imap_host",
	"values.imap_password",
	"values.imap_port",
	"values.includeTools",
	"values.initial_status_message",
	"values.inputExpression",
	"values.kb_id",
	"values.mailbox",
	"values.manifest",
	"values.maxBodyBytes",
	"values.max_attachment_size_mb",
	"values.max_download_file_size_mb",
	"values.max_message_size_mb",
	"values.max_results",
	"values.max_total_attachment_size_mb",
	"values.method",
	"values.microsoft_app_id",
	"values.microsoft_app_password",
	"values.microsoft_app_tenant_id",
	"values.mime_allowlist",
	"values.mime_denylist",
	"values.oauth2_client_id",
	"values.oauth2_client_secret",
	"values.oauth2_tenant_id",
	"values.oauthDispatcherUrl",
	"values.oauth_google_client_id",
	"values.oauth_google_client_secret",
	"values.oauth_google_email",
	"values.oauth_google_refresh_token",
	"values.oauth_microsoft_client_id",
	"values.oauth_microsoft_client_secret",
	"values.oauth_microsoft_email",
	"values.oauth_microsoft_tenant_id",
	"values.operations",
	"values.options",
	"values.password",
	"values.phone_number_id",
	"values.policy_mode",
	"values.poll_interval_seconds",
	"values.port",
	"values.pubsub_subscription",
	"values.rate_limit_per_hour",
	"values.rate_limit_per_minute",
	"values.recipient_allowlist",
	"values.region",
	"values.require_dmarc_for_identity",
	"values.role_arn",
	"values.role_name",
	"values.routePath",
	"values.run_as",
	"values.safe_search",
	"values.scheme",
	"values.sender_identity_mode",
	"values.serverDescription",
	"values.serverName",
	"values.server_url",
	"values.service_account_key",
	"values.service_name",
	"values.session_name",
	"values.slack_bot_token",
	"values.smtp_auth_type",
	"values.smtp_host",
	"values.smtp_password",
	"values.smtp_password_login",
	"values.smtp_port",
	"values.smtp_tls",
	"values.smtp_username",
	"values.smtp_username_login",
	"values.specification_file",
	"values.specification_url",
	"values.stateRedirectionEnabled",
	"values.table_name",
	"values.targetAgentName",
	"values.targetType",
	"values.targetWorkflowName",
	"values.tls_skip_verify",
	"values.tool_name",
	"values.tool_name_prefix",
	"values.trust_server_certificate",
	"values.trusted_authserv_ids",
	"values.trusted_internal_tenant_id",
	"values.use_user_identity_in_channels",
	"values.username",
	"values.verify_token",
	"vertexCredentials",
	"vertexLocation",
	"vertexProject",
}

var FormErrorTypes = []string{
	"custom",
	"invalid_element",
	"invalid_format",
	"invalid_key",
	"invalid_type",
	"invalid_union",
	"invalid_value",
	"max",
	"min",
	"not_multiple_of",
	"required",
	"too_big",
	"too_small",
	"unknown",
	"unrecognized_keys",
	"validate",
}

const maxInventorySetElements = 32

const maxRunModelSetElements = 16

var universalFields = map[string]fieldSpec{
	propPath: routePatternShape(),
}

var webUICatalog = map[EventName]map[string]fieldSpec{
	EventAppLaunched:        {},
	EventAgentCreationStart: {},
	EventChatFirstResponse: {
		PropTraceID: uuidShape(),
	},
	EventBuilderOpened:    {},
	EventBuilderCompleted: {},
	EventFormValidationError: {
		"form":      enumShape(FormNames...),
		"field":     enumShape(FormFields...),
		"errorType": enumShape(FormErrorTypes...),
	},
	EventErrorDisplayed: {
		PropErrorCategory: enumShape(ErrorCategories...),
		PropErrorSource:   enumShape(ErrorSources...),
		"page":            routePatternShape(),
		PropTraceID:       uuidShape(),
	},
	EventPageView: {},
	EventChatSuggestionUsed: {
		PropSuggestionLabel: enumShape(SuggestionLabels...),
	},
	EventActivationSignInClicked:    {},
	EventActivationSignInFailed:     {PropSignInFailure: enumShape(SignInFailures...)},
	EventActivationSignedIn:         {},
	EventActivationTermsShown:       {PropAlreadyAgreed: boolShape()},
	EventActivationTermsError:       {},
	EventActivationReplaceConfirmed: {},
	EventActivationSetupChosen:      {PropSetupPath: enumShape(SetupPaths...)},
	EventActivationAccepted:         {PropSetupPath: enumShape(SetupPaths...)},
	EventActivationAcceptFailed:     {PropSetupPath: enumShape(SetupPaths...)},
	EventFreeModelCreditsLow:        {},
	EventFreeModelCreditsOut:        {},
	EventFreeModelSignInAgain:       {},
}

var cliCatalog = map[EventName]map[string]fieldSpec{
	EventCLICommandRun: {
		PropCommand: textShape(64),
		PropOutcome: enumShape(OutcomeSuccess, OutcomeError),
	},
}

var cliUniversalFields = map[string]fieldSpec{}

var RejectReasons = taxonomy.RejectReasons

const (
	RejectMalformed     = taxonomy.RejectMalformed
	RejectAgentNotFound = taxonomy.RejectAgentNotFound
	RejectForbidden     = taxonomy.RejectForbidden
	RejectInternal      = taxonomy.RejectInternal
)

var serverCatalog = map[EventName]map[string]fieldSpec{
	EventAgentRunCompleted: {
		PropOutcome:       enumShape(OutcomeSuccess, OutcomeError, OutcomeCancelled, OutcomeTimeout),
		PropAgentType:     enumShape(AgentTypeBuiltin, AgentTypeUserCreated, AgentTypeWorkflow),
		PropErrorCategory: enumShape(ErrorCategories...),
		PropProvider:      enumShape(ProviderLabels...),
		PropModel:         enumShape(modelfamily.Families...),
		PropSubmittedFrom: enumShape(httputil.SubmitterSurfaces...),
		PropToolModels:    enumSetShape(maxRunModelSetElements, modelfamily.Families...),
		PropModelType:     enumShape(ModelTypes...),
	},
	EventModelUsed: {
		PropProvider:  enumShape(ProviderLabels...),
		PropModel:     enumShape(modelfamily.Families...),
		PropModelType: enumShape(ModelTypes...),
	},
	EventToolUsed: {
		PropToolCategory:      enumShape(ToolCategories...),
		PropConnectorCategory: enumShape(ConnectorCategories...),
		PropBuiltinCategory:   enumShape(BuiltinToolCategories...),
		PropRunOrigin:         enumShape(RunOrigins...),
	},
	EventTaskSubmitRejected: {
		PropRejectReason:   enumShape(RejectReasons...),
		PropSubmittedFrom:  enumShape(httputil.SubmitterSurfaces...),
		PropEntrypointType: enumShape(GatewayTypes...),
	},
	EventAgentPeerDelegated: {PropRunOrigin: enumShape(RunOrigins...)},
	EventModelConfigured: {
		PropProvider:      enumShape(ProviderLabels...),
		PropModel:         enumShape(modelfamily.Families...),
		PropModelType:     enumShape(ModelTypes...),
		PropSubmittedFrom: enumShape(httputil.WriterSurfaces...),
		PropModelKind:     enumShape(ModelKinds...),
	},
	EventAgentCreated: {
		PropAgentType:     enumShape(AgentCreationTypes...),
		PropToolsetCount:  countShape(),
		PropSubmittedFrom: enumShape(httputil.SubmitterSurfaces...),
	},
	EventAgentDeployed: {},
	EventAgentDeleted: {
		PropAgentType: enumShape(AgentCreationTypes...),
	},
	EventWorkflowCreated:  {},
	EventWorkflowDeployed: {},
	EventConnectorCreated: {
		PropCategory: enumShape(ConnectorCategories...),
	},
	EventConnectorDeleted: {},
	EventGatewayCreated: {
		PropGatewayType: enumShape(GatewayTypes...),
	},
	EventGatewayDeleted: {},
	EventToolsetCreated: {},
	EventEntrypointUsed: {
		PropEntrypointType: enumShape(GatewayTypes...),
		PropOutcome:        enumShape(OutcomeSuccess, OutcomeError, OutcomeCancelled, OutcomeTimeout),
	},
	EventInstallInventory: {
		PropAgentCount:                  countShape(),
		PropWorkflowCount:               countShape(),
		PropConnectorCount:              countShape(),
		PropEntrypointCount:             countShape(),
		PropToolsetCount:                countShape(),
		PropConfiguredModelCount:        countShape(),
		PropAgentsWithCustomToolsets:    countShape(),
		PropWorkflowsWithCustomToolsets: countShape(),
		PropConnectorCategories:         enumSetShape(maxInventorySetElements, ConnectorCategories...),
		PropEntrypointTypes:             enumSetShape(maxInventorySetElements, GatewayTypes...),

		PropDeployedAgentCount:                  countShape(),
		PropDeployedWorkflowCount:               countShape(),
		PropDeployedAgentsWithCustomToolsets:    countShape(),
		PropDeployedWorkflowsWithCustomToolsets: countShape(),

		PropBuiltinAgentCount:         countShape(),
		PropDeployedBuiltinAgentCount: countShape(),
	},
	EventEvalCreated: {
		PropTargetType: enumShape(EvalTargetTypes...),
	},
	EventEvalRun: {
		PropOutcome:                        enumShape(EvalRunOutcomes...),
		PropTargetType:                     enumShape(EvalTargetTypes...),
		PropErrorCategory:                  enumShape(ErrorCategories...),
		PropEvaluatorTypes:                 enumSetShape(maxEvalRunSetElements, EvalRunEvaluatorTypes...),
		PropTrialsPerExample:               enumShape(EvalTrialsPerExample...),
		PropWorkflowTrialFailureKinds:      enumSetShape(maxEvalRunSetElements, EvalWorkflowTrialFailureKinds...),
		PropDecisionEvaluatorCount:         countShape(),
		PropDecisionCasesDecided:           countShape(),
		PropDecisionCasesEscalated:         countShape(),
		PropDecisionCasesErrored:           countShape(),
		PropDecisionEscalatedLowConfidence: countShape(),
		PropDecisionEscalatedNoConfidence:  countShape(),
		PropDecisionEscalatedProviderError: countShape(),
		PropDecisionEscalatedModelLookup:   countShape(),
	},
	EventEvaluatorCreated: {
		PropEvaluatorType: enumShape(EvaluatorTypes...),
		PropQuestionType:  enumShape(DecisionQuestionTypes...),
		PropEscalation:    enumShape(EscalationModes...),
		PropSubmittedFrom: enumShape(httputil.SubmitterSurfaces...),
	},
	EventConsentChanged: {
		PropConsentPrevious: enumShape(ConsentStates...),
		PropConsentNew:      enumShape(ConsentStates...),
	},
}

var serverUniversalFields = map[string]fieldSpec{
	PropTraceID: uuidShape(),
}

var catalog = mergeCatalogs(webUICatalog, cliCatalog, serverCatalog)

func catalogFor(surface string) (events map[EventName]map[string]fieldSpec, universal map[string]fieldSpec) {
	switch surface {
	case SurfaceCLI:
		return cliCatalog, cliUniversalFields
	case SurfaceUI:
		return webUICatalog, universalFields
	case SurfaceServer:
		return serverCatalog, serverUniversalFields
	default:
		return nil, nil
	}
}

func ApprovedFields(surface string, name EventName) []string {
	events, _ := catalogFor(surface)
	fields := make([]string, 0, len(events[name]))
	for field := range events[name] {
		fields = append(fields, field)
	}
	slices.Sort(fields)
	return fields
}

func mergeCatalogs(sets ...map[EventName]map[string]fieldSpec) map[EventName]map[string]fieldSpec {
	merged := make(map[EventName]map[string]fieldSpec)
	for _, set := range sets {
		for name, fields := range set {
			merged[name] = fields
		}
	}
	return merged
}

func IsCatalogEvent(name string) bool {
	_, ok := catalog[name]
	return ok
}
