// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"encoding/json"
	"strconv"
)

type Request struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	ErrorDataLLMErrorCategory = "llm_error_category"
	ErrorDataLLMProvider      = "llm_provider"
)

const (
	MetadataToolCategories      = "tool_categories"
	MetadataConnectorCategories = "connector_categories"
	MetadataBuiltinCategories   = "builtin_categories"
	MetadataPeerDelegated       = "peer_delegated"
	MetadataModelFamily         = "model_family"
	MetadataModelProvider       = "model_provider"
	MetadataModelType           = "model_type"
	MetadataToolModelFamilies   = "tool_model_families"
	MetadataModelUsages         = "model_usages"
)

const (
	MetadataModelOverride = "model_override"
	MetadataModelID       = "model_id"
)

const (
	ModelUsageFamily     = "family"
	ModelUsageProvider   = "provider"
	ModelUsageServedFrom = "served_from"
)

type Task struct {
	ID        string         `json:"id"`
	ContextID string         `json:"contextId"`
	Kind      string         `json:"kind"`
	Status    TaskStatus     `json:"status"`
	History   []Message      `json:"history,omitempty"`
	Artifacts []Artifact     `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type TaskStatus struct {
	State     TaskState `json:"state"`
	Message   *Message  `json:"message,omitempty"`
	Timestamp string    `json:"timestamp,omitempty"`
}

type TaskState string

const (
	TaskStateSubmitted    TaskState = "submitted"
	TaskStateWorking      TaskState = "working"
	TaskStateCompleted    TaskState = "completed"
	TaskStateFailed       TaskState = "failed"
	TaskStateCancelled    TaskState = "canceled"
	TaskStateInputNeeded  TaskState = "input-required"
	TaskStateRejected     TaskState = "rejected"
	TaskStateAuthRequired TaskState = "auth-required"
	TaskStateUnknown      TaskState = "unknown"
)

func IsFailureStatus(status string) bool {
	return status == string(TaskStateFailed) || status == "error" || status == "timeout"
}

type Role string

const (
	RoleUser  Role = "user"
	RoleAgent Role = "agent"
)

type Message struct {
	Kind      string         `json:"kind"`
	MessageID string         `json:"messageId"`
	Role      Role           `json:"role"`
	Parts     []Part         `json:"parts"`
	TaskID    string         `json:"taskId,omitempty"`
	ContextID string         `json:"contextId,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type Part struct {
	Kind string `json:"kind"`

	Text string `json:"text,omitempty"`

	Data any `json:"data,omitempty"`

	File *FileContent `json:"file,omitempty"`

	Metadata map[string]any `json:"metadata,omitempty"`
}

func (p Part) MarshalJSON() ([]byte, error) {
	m := map[string]any{"kind": p.Kind}
	switch p.Kind {
	case "text":
		m["text"] = p.Text
	case "data":
		if p.Data != nil {
			m["data"] = p.Data
		}
	case "file":
		if p.File != nil {
			m["file"] = p.File
		}
	}
	if p.Metadata != nil {
		m["metadata"] = p.Metadata
	}
	return json.Marshal(m)
}

type FileContent struct {
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Bytes    string `json:"bytes,omitempty"`
}

type Artifact struct {
	ArtifactID  string         `json:"artifactId"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parts       []Part         `json:"parts"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Extensions  []string       `json:"extensions,omitempty"`
}

const (
	ArtifactTagUserUploaded = "__user_uploaded"
	ArtifactTagWorking      = "__working"
)

type TaskStatusUpdateEvent struct {
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Kind      string         `json:"kind"`
	Status    TaskStatus     `json:"status"`
	Final     bool           `json:"final"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type TaskArtifactUpdateEvent struct {
	TaskID    string         `json:"taskId"`
	ContextID string         `json:"contextId"`
	Kind      string         `json:"kind"`
	Artifact  Artifact       `json:"artifact"`
	Append    bool           `json:"append,omitempty"`
	LastChunk bool           `json:"lastChunk,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type AgentCard struct {
	Name               string                    `json:"name"`
	Description        string                    `json:"description"`
	URL                string                    `json:"url"`
	Version            string                    `json:"version"`
	ProtocolVersion    string                    `json:"protocolVersion"`
	Capabilities       AgentCapabilities         `json:"capabilities"`
	DefaultInputModes  []string                  `json:"defaultInputModes"`
	DefaultOutputModes []string                  `json:"defaultOutputModes"`
	Skills             []AgentSkill              `json:"skills"`
	Provider           *AgentProvider            `json:"provider,omitempty"`
	DocumentationURL   string                    `json:"documentationUrl,omitempty"`
	SecuritySchemes    map[string]SecurityScheme `json:"securitySchemes,omitempty"`
	Security           []map[string][]string     `json:"security,omitempty"`
}

type AgentCapabilities struct {
	Streaming              bool             `json:"streaming"`
	PushNotifications      bool             `json:"pushNotifications"`
	StateTransitionHistory bool             `json:"stateTransitionHistory"`
	Extensions             []AgentExtension `json:"extensions,omitempty"`
}

type AgentExtension struct {
	URI         string         `json:"uri"`
	Description string         `json:"description,omitempty"`
	Required    bool           `json:"required,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
}

type AgentSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

type AgentProvider struct {
	Organization string `json:"organization"`
	URL          string `json:"url,omitempty"`
}

type SecurityScheme struct {
	Type   string      `json:"type"`
	Scheme string      `json:"scheme,omitempty"`
	In     string      `json:"in,omitempty"`
	Name   string      `json:"name,omitempty"`
	Flows  *OAuthFlows `json:"flows,omitempty"`
}

type OAuthFlows struct {
	ClientCredentials *OAuthFlow `json:"clientCredentials,omitempty"`
	AuthorizationCode *OAuthFlow `json:"authorizationCode,omitempty"`
}

type OAuthFlow struct {
	AuthorizationURL string            `json:"authorizationUrl,omitempty"`
	TokenURL         string            `json:"tokenUrl"`
	Scopes           map[string]string `json:"scopes,omitempty"`
}

type UserProperties struct {
	Timestamp           int64           `json:"timestamp"`
	ClientID            string          `json:"clientId,omitempty"`
	UserID              string          `json:"userId,omitempty"`
	ReplyTo             string          `json:"replyTo,omitempty"`
	A2AStatusTopic      string          `json:"a2aStatusTopic,omitempty"`
	A2AUserConfig       string          `json:"a2aUserConfig,omitempty"`
	A2AExtensions       string          `json:"a2aExtensions,omitempty"`
	CallDepth           int             `json:"callDepth,omitempty"`
	AuthToken           string          `json:"authToken,omitempty"`
	DelegatingAgentName string          `json:"delegatingAgentName,omitempty"`
	GatewayCapabilities map[string]bool `json:"gatewayCapabilities,omitempty"`
	TraceID             string          `json:"traceId,omitempty"`
	Trigger             string          `json:"trigger,omitempty"`
	RootKind            string          `json:"rootKind,omitempty"`
	RootID              string          `json:"rootId,omitempty"`
	RootNodeID          string          `json:"rootNodeId,omitempty"`
	Origin
	TriggerSource
}

type TriggerSource struct {
	TriggerRoute      string `json:"triggerRoute,omitempty"`
	TriggerEvent      string `json:"triggerEvent,omitempty"`
	TriggerDeliveryID string `json:"triggerDeliveryId,omitempty"`
}

type Origin struct {
	Channel        string `json:"channel,omitempty"`
	UserUUID       string `json:"userUuid,omitempty"`
	EntrypointUUID string `json:"entrypointUuid,omitempty"`
	EntrypointName string `json:"entrypointName,omitempty"`
}

func (up UserProperties) IsPeerCall() bool {
	return up.DelegatingAgentName != ""
}

const (
	TriggerInteractive = "interactive"
	TriggerScheduled   = "scheduled"
	TriggerEval        = "eval"
	TriggerEvent       = "event"
	TriggerUnknown     = "unknown"
)

const (
	RootKindAgent    = "agent"
	RootKindWorkflow = "workflow"
)

const (
	ChannelScheduler = "scheduler"
	ChannelEval      = "eval"
	ChannelSystem    = "system"
)

const SourceScheduler = "scheduler"

const MetadataScheduledTaskID = "scheduledTaskId"

const MetadataQuietEndAllowed = "quietEndAllowed"

const (
	MetadataRunOutcome = "runOutcome"
	RunOutcomeQuiet    = "quiet"
)

const MetadataDisplaysTemplateSignals = "displaysTemplateSignals"

const (
	TaskKindAgent         = "agent"
	TaskKindWorkflowAgent = "workflow_agent"
	TaskKindWorkflow      = "workflow"
	TaskKindUnknown       = "unknown"
)

type ActiveTaskKey struct {
	Kind    string
	Trigger string
}

func NormalizeTrigger(trigger string) string {
	switch trigger {
	case TriggerInteractive, TriggerScheduled, TriggerEval, TriggerEvent:
		return trigger
	default:
		return TriggerUnknown
	}
}

type SignalType string

const (
	SignalToolInvocationStart      SignalType = "tool_invocation_start"
	SignalToolResult               SignalType = "tool_result"
	SignalLLMInvocation            SignalType = "llm_invocation"
	SignalLLMResponse              SignalType = "llm_response"
	SignalAgentProgressUpdate      SignalType = "agent_progress_update"
	SignalArtifactCreationProgress SignalType = "artifact_creation_progress"
	SignalArtifactSaved            SignalType = "artifact_saved"
	SignalTemplateBlock            SignalType = "template_block"
	SignalStructuredInvocationReq  SignalType = "structured_invocation_request"
	SignalStructuredInvocationRes  SignalType = "structured_invocation_result"
	SignalWorkflowExecutionStart   SignalType = "workflow_execution_start"
	SignalWorkflowNodeExecStart    SignalType = "workflow_node_execution_start"
	SignalWorkflowNodeExecResult   SignalType = "workflow_node_execution_result"
	SignalWorkflowMapProgress      SignalType = "workflow_map_progress"
	SignalWorkflowExecutionResult  SignalType = "workflow_execution_result"
	SignalDeepResearchProgress     SignalType = "deep_research_progress"
	SignalRAGInfoUpdate            SignalType = "rag_info_update"
	SignalDeepResearchReport       SignalType = "deep_research_report"
	SignalDeepResearchPlan         SignalType = "deep_research_plan"
	SignalDeepResearchPlanStale    SignalType = "deep_research_plan_stale"
	SignalAuthenticationRequired   SignalType = "authentication_required"
	SignalAuthenticationResult     SignalType = "authentication_result"
	SignalAuthResponse             SignalType = "auth_response"
	SignalUserInputRequest         SignalType = "user_input_request"
	SignalUserInputResponse        SignalType = "user_input_response"
	SignalThinkingContent          SignalType = "thinking_content"
	SignalBuilderComponentProgress SignalType = "builder_component_progress"
)

type SignalTyper interface {
	GetSignalType() string
}

type DeepResearchProgressData struct {
	Type               string              `json:"type"`
	Phase              string              `json:"phase"`
	StatusText         string              `json:"status_text"`
	ProgressPercentage int                 `json:"progress_percentage"`
	CurrentIteration   int                 `json:"current_iteration"`
	TotalIterations    int                 `json:"total_iterations"`
	SourcesFound       int                 `json:"sources_found"`
	CurrentQuery       string              `json:"current_query,omitempty"`
	FetchingURLs       []map[string]string `json:"fetching_urls,omitempty"`
	ElapsedSeconds     int                 `json:"elapsed_seconds"`
	MaxRuntimeSeconds  int                 `json:"max_runtime_seconds"`
}

func (d DeepResearchProgressData) GetSignalType() string {
	return string(SignalDeepResearchProgress)
}

type RAGSource struct {
	CitationID     string         `json:"citationId"`
	FileID         string         `json:"fileId,omitempty"`
	Filename       string         `json:"filename,omitempty"`
	Title          string         `json:"title,omitempty"`
	SourceType     string         `json:"sourceType,omitempty"`
	SourceURL      string         `json:"sourceUrl,omitempty"`
	URL            string         `json:"url,omitempty"`
	ContentPreview string         `json:"contentPreview"`
	RelevanceScore float64        `json:"relevanceScore"`
	RetrievedAt    string         `json:"retrievedAt,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

type RAGInfoUpdateData struct {
	Type       string      `json:"type"`
	Title      string      `json:"title"`
	Query      string      `json:"query"`
	SearchType string      `json:"search_type"`
	Sources    []RAGSource `json:"sources"`
	IsComplete bool        `json:"is_complete"`
	Timestamp  string      `json:"timestamp"`
	SearchTurn int         `json:"search_turn"`
}

func (d RAGInfoUpdateData) GetSignalType() string {
	return string(SignalRAGInfoUpdate)
}

type DeepResearchPlanData struct {
	Type              string   `json:"type"`
	PlanID            string   `json:"plan_id"`
	AgentName         string   `json:"agent_name"`
	Title             string   `json:"title"`
	ResearchQuestion  string   `json:"research_question"`
	Steps             []string `json:"steps"`
	ResearchType      string   `json:"research_type"`
	MaxIterations     int      `json:"max_iterations"`
	MaxRuntimeSeconds int      `json:"max_runtime_seconds"`
	Sources           []string `json:"sources"`
}

func (d DeepResearchPlanData) GetSignalType() string {
	return string(SignalDeepResearchPlan)
}

type DeepResearchPlanStaleData struct {
	Type   string `json:"type"`
	PlanID string `json:"plan_id"`
	Reason string `json:"reason"`
}

func (d DeepResearchPlanStaleData) GetSignalType() string {
	return string(SignalDeepResearchPlanStale)
}

type DeepResearchReportData struct {
	Type         string `json:"type"`
	Filename     string `json:"filename"`
	Version      int    `json:"version"`
	URI          string `json:"uri"`
	Title        string `json:"title"`
	SourcesCount int    `json:"sources_count"`
}

func (d DeepResearchReportData) GetSignalType() string {
	return string(SignalDeepResearchReport)
}

type ToolInvocationStartData struct {
	Type            string         `json:"type"`
	ToolName        string         `json:"tool_name"`
	ToolArgs        map[string]any `json:"tool_args"`
	FunctionCallID  string         `json:"function_call_id"`
	ParallelGroupID *string        `json:"parallel_group_id"`
}

const PartMetaSkillContentHash = "skill_content_hash"

func IsSkillContentHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

type ToolResultData struct {
	Type           string          `json:"type"`
	ToolName       string          `json:"tool_name"`
	ResultData     any             `json:"result_data"`
	FunctionCallID string          `json:"function_call_id"`
	LLMUsage       *map[string]any `json:"llm_usage"`
}

type AgentProgressUpdateData struct {
	Type       string `json:"type"`
	StatusText string `json:"status_text"`
}

type ThinkingContentData struct {
	Type       string `json:"type"`
	Content    string `json:"content"`
	IsComplete bool   `json:"is_complete"`
}

type BuilderComponentProgressData struct {
	Type            string  `json:"type"`
	ComponentID     string  `json:"component_id"`
	ComponentType   string  `json:"component_type"`
	ComponentName   string  `json:"component_name"`
	State           string  `json:"state"`
	StatusText      string  `json:"status_text,omitempty"`
	ProgressPercent *int    `json:"progress_percent,omitempty"`
	ParentID        *string `json:"parent_id,omitempty"`
}

func (d BuilderComponentProgressData) GetSignalType() string {
	return string(SignalBuilderComponentProgress)
}

type LlmInvocationData struct {
	Type    string         `json:"type"`
	Request map[string]any `json:"request"`
	Usage   map[string]any `json:"usage"`
}

const LLMRequestLastAllowedCall = "last_allowed_call"

const LLMRequestTrailing = "trailing"

type LlmResponseData struct {
	Type  string         `json:"type"`
	Data  map[string]any `json:"data"`
	Usage map[string]any `json:"usage"`
}

type ArtifactCreationProgressData struct {
	Type             string   `json:"type"`
	Filename         string   `json:"filename"`
	Status           string   `json:"status"`
	BytesTransferred int      `json:"bytes_transferred"`
	Description      *string  `json:"description"`
	ArtifactChunk    *string  `json:"artifact_chunk"`
	MIMEType         *string  `json:"mime_type"`
	Version          *int     `json:"version"`
	FunctionCallID   *string  `json:"function_call_id"`
	RolledBackText   *string  `json:"rolled_back_text,omitempty"`
	ErrorMessage     *string  `json:"error_message,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

type ArtifactSavedData struct {
	Type           string   `json:"type"`
	Filename       string   `json:"filename"`
	Version        int      `json:"version"`
	MIMEType       string   `json:"mime_type"`
	SizeBytes      int64    `json:"size_bytes"`
	Description    *string  `json:"description"`
	FunctionCallID *string  `json:"function_call_id"`
	Tags           []string `json:"tags,omitempty"`
}

type ArtifactRef struct {
	Name    string `json:"name"`
	Version int    `json:"version,omitempty"`
}

type StructuredInvocationRequest struct {
	Type                    string         `json:"type"`
	WorkflowName            string         `json:"workflow_name"`
	NodeID                  string         `json:"node_id"`
	InputSchema             map[string]any `json:"input_schema,omitempty"`
	OutputSchema            map[string]any `json:"output_schema,omitempty"`
	SuggestedOutputFilename string         `json:"suggested_output_filename,omitempty"`
}

type StructuredInvocationResult struct {
	Type              string       `json:"type"`
	Status            string       `json:"status"`
	OutputArtifactRef *ArtifactRef `json:"output_artifact_ref,omitempty"`
	ErrorMessage      string       `json:"error_message,omitempty"`
	ValidationErrors  []string     `json:"validation_errors,omitempty"`
	RetryCount        int          `json:"retry_count"`
}

func NewRequest(taskID, method string, params any) *Request {
	return &Request{
		JSONRPC: "2.0",
		ID:      taskID,
		Method:  method,
		Params:  params,
	}
}

func NewCancelTaskRequest(taskID string) *Request {
	return NewRequest(taskID, "tasks/cancel", map[string]any{"id": taskID})
}

const CancelErrorCodeKey = "error_code"

func NewCancelTaskRequestWithErrorCode(taskID, errorCode string) *Request {
	return NewRequest(taskID, "tasks/cancel", map[string]any{
		"id":       taskID,
		"metadata": map[string]any{CancelErrorCodeKey: errorCode},
	})
}

func NewResponse(taskID string, result any) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      taskID,
		Result:  result,
	}
}

func NewErrorResponse(taskID string, code int, message string) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      taskID,
		Error:   &Error{Code: code, Message: message},
	}
}

func NewTextPart(text string) Part {
	return Part{Kind: "text", Text: text}
}

func NewDataPart(data any) Part {
	return Part{Kind: "data", Data: data}
}

func NewDataPartWithMetadata(data any, metadata map[string]any) Part {
	return Part{Kind: "data", Data: data, Metadata: metadata}
}

func SetForwardedPeerChain(incomingMeta, newMeta map[string]any, origTaskID, parentTaskID string) {
	leaf := origTaskID
	if incomingMeta != nil {
		if existing, ok := incomingMeta["original_peer_event_taskId"].(string); ok && existing != "" {
			leaf = existing
		}
	}
	if leaf != "" && leaf != parentTaskID {
		newMeta["original_peer_event_taskId"] = leaf
	}

	var chain []any
	if incomingMeta != nil {
		if existing, ok := incomingMeta["peer_task_chain"].([]any); ok {
			chain = append(chain, existing...)
		}
	}
	if origTaskID != "" && origTaskID != parentTaskID {
		chain = append(chain, origTaskID)
	}
	if len(chain) > 0 {
		newMeta["peer_task_chain"] = chain
	}
}

func NewFilePart(uri, name, mimeType string) Part {
	return Part{
		Kind: "file",
		File: &FileContent{URI: uri, Name: name, MIMEType: mimeType},
	}
}

func NewFilePartWithBytes(b64, name, mimeType string) Part {
	return Part{
		Kind: "file",
		File: &FileContent{Bytes: b64, Name: name, MIMEType: mimeType},
	}
}

func NewStatusUpdateEvent(taskID, contextID string, parts []Part, final bool, metadata map[string]any) *TaskStatusUpdateEvent {
	return &TaskStatusUpdateEvent{
		TaskID:    taskID,
		ContextID: contextID,
		Kind:      "status-update",
		Status: TaskStatus{
			State: TaskStateWorking,
			Message: &Message{
				Kind:  "message",
				Role:  RoleAgent,
				Parts: parts,
			},
		},
		Final:    final,
		Metadata: metadata,
	}
}

const (
	PropTimestamp           = "timestamp"
	PropClientID            = "clientId"
	PropUserID              = "userId"
	PropReplyTo             = "replyTo"
	PropA2AStatusTopic      = "a2aStatusTopic"
	PropA2AUserConfig       = "a2aUserConfig"
	PropA2AExtensions       = "a2aExtensions"
	PropCallDepth           = "callDepth"
	PropAuthToken           = "authToken"
	PropDelegatingAgentName = "delegating_agent_name"
	PropGatewayCapabilities = "gatewayCapabilities"
	PropTraceID             = "traceId"
	PropEventSeq            = "eventSeq"
	PropPublisherID         = "publisherId"
	PropTrigger             = "trigger"
	PropRootKind            = "rootKind"
	PropRootID              = "rootId"
	PropRootNodeID          = "rootNodeId"
	PropEntrypointName      = "entrypointName"
	PropChannel             = "channel"
	PropUserUUID            = "userUuid"
	PropEntrypointUUID      = "entrypointUuid"
	PropTriggerRoute        = "triggerRoute"
	PropTriggerEvent        = "triggerEvent"
	PropTriggerDeliveryID   = "triggerDeliveryId"
	PropNotifyCard          = "notifyCard"
)

const MetaAgentName = "agent_name"

func AddTraceID(m map[string]string, traceID string) {
	if traceID != "" {
		m[PropTraceID] = traceID
	}
}

func AddTrigger(m map[string]string, trigger string) {
	if trigger != "" {
		m[PropTrigger] = trigger
	}
}

func (up UserProperties) ResolveEntrypointName() string {
	if up.IsPeerCall() {
		return up.EntrypointName
	}
	return up.ClientID
}

func (up UserProperties) ForwardedOrigin() Origin {
	origin := up.Origin
	origin.EntrypointName = up.ResolveEntrypointName()
	return origin
}

func AddRoot(m map[string]string, kind, id, nodeID string) {
	if kind != "" {
		m[PropRootKind] = kind
	}
	if id != "" {
		m[PropRootID] = id
	}
	if nodeID != "" {
		m[PropRootNodeID] = nodeID
	}
}

func AddOrigin(m map[string]string, origin Origin) {
	if origin.Channel != "" {
		m[PropChannel] = origin.Channel
	}
	if origin.UserUUID != "" {
		m[PropUserUUID] = origin.UserUUID
	}
	if origin.EntrypointUUID != "" {
		m[PropEntrypointUUID] = origin.EntrypointUUID
	}
	if origin.EntrypointName != "" {
		m[PropEntrypointName] = origin.EntrypointName
	}
}

func AddEventSeq(m map[string]string, publisherID string, seq uint64) {
	if publisherID == "" {
		return
	}
	m[PropPublisherID] = publisherID
	m[PropEventSeq] = strconv.FormatUint(seq, 10)
}

func EncodeUserProperties(up *UserProperties) map[string]string {
	m := map[string]string{
		PropTimestamp: strconv.FormatInt(up.Timestamp, 10),
	}
	if up.ClientID != "" {
		m[PropClientID] = up.ClientID
	}
	if up.UserID != "" {
		m[PropUserID] = up.UserID
	}
	if up.ReplyTo != "" {
		m[PropReplyTo] = up.ReplyTo
	}
	if up.A2AStatusTopic != "" {
		m[PropA2AStatusTopic] = up.A2AStatusTopic
	}
	if up.A2AUserConfig != "" {
		m[PropA2AUserConfig] = up.A2AUserConfig
	}
	if up.A2AExtensions != "" {
		m[PropA2AExtensions] = up.A2AExtensions
	}
	if up.CallDepth > 0 {
		m[PropCallDepth] = strconv.Itoa(up.CallDepth)
	}
	if up.AuthToken != "" {
		m[PropAuthToken] = up.AuthToken
	}
	if up.DelegatingAgentName != "" {
		m[PropDelegatingAgentName] = up.DelegatingAgentName
	}
	if len(up.GatewayCapabilities) > 0 {
		if b, err := json.Marshal(up.GatewayCapabilities); err == nil {
			m[PropGatewayCapabilities] = string(b)
		}
	}
	if up.TraceID != "" {
		m[PropTraceID] = up.TraceID
	}
	if up.Trigger != "" {
		m[PropTrigger] = up.Trigger
	}
	AddRoot(m, up.RootKind, up.RootID, up.RootNodeID)
	AddOrigin(m, up.Origin)
	addTriggerSource(m, up.TriggerSource)
	return m
}

func addTriggerSource(m map[string]string, source TriggerSource) {
	if source.TriggerRoute != "" {
		m[PropTriggerRoute] = source.TriggerRoute
	}
	if source.TriggerEvent != "" {
		m[PropTriggerEvent] = source.TriggerEvent
	}
	if source.TriggerDeliveryID != "" {
		m[PropTriggerDeliveryID] = source.TriggerDeliveryID
	}
}

type AuthenticationRequiredData struct {
	Type                      string  `json:"type"`
	TargetAgent               string  `json:"target_agent"`
	TargetAgentTaskID         string  `json:"target_agent_task_id"`
	UserID                    string  `json:"user_id"`
	ClientID                  string  `json:"client_id"`
	AuthURI                   string  `json:"auth_uri"`
	AuthRequestFunctionCallID string  `json:"auth_request_function_call_id"`
	CredentialKey             string  `json:"credential_key"`
	GatewayTaskID             *string `json:"gateway_task_id"`
	CodeVerifier              string  `json:"code_verifier"`
	ToolDisplayName           string  `json:"tool_display_name,omitempty"`
}

type AuthenticationResultData struct {
	Type                      string `json:"type"`
	TargetAgent               string `json:"target_agent"`
	TargetAgentTaskID         string `json:"target_agent_task_id"`
	Success                   bool   `json:"success"`
	CredentialKey             string `json:"credential_key,omitempty"`
	AuthRequestFunctionCallID string `json:"auth_request_function_call_id,omitempty"`
	ToolDisplayName           string `json:"tool_display_name,omitempty"`
}

func AuthenticationRequiredFromStatus(event *TaskStatusUpdateEvent) (*AuthenticationRequiredData, bool) {
	if event == nil || event.Status.Message == nil {
		return nil, false
	}
	for _, part := range event.Status.Message.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		dataMap, ok := part.Data.(map[string]any)
		if !ok {
			continue
		}
		if authData := AuthenticationRequiredFromDataMap(dataMap); authData != nil {
			return authData, true
		}
	}
	return nil, false
}

func AuthenticationRequiredFromDataMap(dataMap map[string]any) *AuthenticationRequiredData {
	if dataMap["type"] != string(SignalAuthenticationRequired) {
		return nil
	}
	raw, err := json.Marshal(dataMap)
	if err != nil {
		return nil
	}
	var authData AuthenticationRequiredData
	if err := json.Unmarshal(raw, &authData); err != nil {
		return nil
	}
	return &authData
}

func AuthenticationResultFromStatus(event *TaskStatusUpdateEvent) (*AuthenticationResultData, bool) {
	if event == nil || event.Status.Message == nil {
		return nil, false
	}
	for _, part := range event.Status.Message.Parts {
		if part.Kind != "data" || part.Data == nil {
			continue
		}
		dataMap, ok := part.Data.(map[string]any)
		if !ok || dataMap["type"] != "authentication_result" {
			continue
		}
		raw, err := json.Marshal(dataMap)
		if err != nil {
			continue
		}
		var resultData AuthenticationResultData
		if err := json.Unmarshal(raw, &resultData); err != nil {
			continue
		}
		return &resultData, true
	}
	return nil, false
}

type AuthResponseData struct {
	Type              string         `json:"type"`
	TargetAgent       string         `json:"target_agent"`
	TargetAgentTaskID string         `json:"target_agent_task_id"`
	UserID            string         `json:"user_id"`
	ClientID          string         `json:"client_id"`
	FunctionCallID    string         `json:"function_call_id"`
	Code              string         `json:"code"`
	CredentialKey     string         `json:"credential_key"`
	UserIdentity      map[string]any `json:"user_identity"`
	AuthResponseURI   string         `json:"auth_response_uri"`
	TaskCallStack     []string       `json:"task_call_stack"`
	CodeVerifier      string         `json:"code_verifier"`
	State             string         `json:"state,omitempty"`
	IsCredentialSave  bool           `json:"is_mcp_credential_save"`
}

type ToolAuthStateRegistration struct {
	State          string `json:"state"`
	TargetAgent    string `json:"target_agent"`
	TaskID         string `json:"task_id"`
	UserID         string `json:"user_id"`
	ClientID       string `json:"client_id,omitempty"`
	FunctionCallID string `json:"function_call_id,omitempty"`
	CredentialKey  string `json:"credential_key"`
	CodeVerifier   string `json:"code_verifier,omitempty"`
	TTLMs          int64  `json:"ttl_ms,omitempty"`
}

type UserInputRequestData struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	ExpiresAt string          `json:"expires_at"`
	Source    string          `json:"source"`
	Surface   json.RawMessage `json:"surface"`
}

type UserInputResponseData struct {
	Type      string         `json:"type"`
	RequestID string         `json:"request_id"`
	Status    string         `json:"status"`
	Data      map[string]any `json:"data"`
}

const (
	NotifyModeNotify = "notify"
	NotifyModeInput  = "input"
)

const (
	CategoryScheduledTask         = "scheduled_task"
	CategoryAgentMessage          = "agent_message"
	CategoryHIL                   = "hil"
	CategoryChatShare             = "chat_share"
	CategoryBudgetSelfWarn        = "budget_self_warn"
	CategoryBudgetSelfLimit       = "budget_self_limit"
	CategoryBudgetMeshWarn        = "budget_mesh_warn"
	CategoryBudgetMeshLimit       = "budget_mesh_limit"
	CategoryBudgetSystemUserWarn  = "budget_system_user_warn"
	CategoryBudgetSystemUserLimit = "budget_system_user_limit"
)

type NotifyArtifact struct {
	AgentName string `json:"agent_name"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type,omitempty"`
	Version   *int   `json:"version,omitempty"`
}

type NotifyRequest struct {
	RequestID               string           `json:"request_id"`
	RecipientUserID         string           `json:"recipient_user_id"`
	RecipientPlatformUserID string           `json:"recipient_platform_user_id,omitempty"`
	Mode                    string           `json:"mode"`
	SenderUserID            string           `json:"sender_user_id,omitempty"`
	SenderAgent             string           `json:"sender_agent,omitempty"`
	RequesterAddr           string           `json:"requester_addr,omitempty"`
	Channels                []string         `json:"channels,omitempty"`
	Category                string           `json:"category,omitempty"`
	Title                   string           `json:"title,omitempty"`
	Message                 string           `json:"message,omitempty"`
	Surface                 json.RawMessage  `json:"surface,omitempty"`
	ExpiresAt               string           `json:"expires_at,omitempty"`
	Dedup                   string           `json:"dedup,omitempty"`
	Metadata                map[string]any   `json:"metadata,omitempty"`
	Artifacts               []NotifyArtifact `json:"artifacts,omitempty"`
	Approvers               *NotifyApprovers `json:"approvers,omitempty"`
}

type NotifyApprovers struct {
	UserIDs []string          `json:"user_ids,omitempty"`
	Groups  []string          `json:"groups,omitempty"`
	Places  []NotifyPlace     `json:"places,omitempty"`
	Names   map[string]string `json:"names,omitempty"`
}

type NotifyPlace struct {
	ChannelType string          `json:"channel_type"`
	Address     json.RawMessage `json:"address"`
}

type ScheduleRequest struct {
	RequestID          string         `json:"request_id"`
	Name               string         `json:"name"`
	Description        string         `json:"description,omitempty"`
	ScheduleType       string         `json:"schedule_type"`
	ScheduleExpression string         `json:"schedule_expression"`
	Timezone           string         `json:"timezone,omitempty"`
	TargetType         string         `json:"target_type,omitempty"`
	TargetName         string         `json:"target_name"`
	TaskMessage        string         `json:"task_message"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	TimeoutSeconds     int            `json:"timeout_seconds,omitempty"`
	MaxRetries         int            `json:"max_retries,omitempty"`
	RetryDelaySeconds  int            `json:"retry_delay_seconds,omitempty"`
	SenderUserID       string         `json:"sender_user_id,omitempty"`
	SenderAgent        string         `json:"sender_agent,omitempty"`
	Source             string         `json:"source,omitempty"`
	ReplyTo            string         `json:"reply_to,omitempty"`
}

type ScheduleResponse struct {
	RequestID string `json:"request_id"`
	OK        bool   `json:"ok"`
	TaskID    string `json:"task_id,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
}

type NotifyPresence struct {
	UserID      string          `json:"user_id"`
	ChannelType string          `json:"channel_type"`
	Address     json.RawMessage `json:"address"`
	Email       string          `json:"email,omitempty"`
	GatewayID   string          `json:"gateway_id,omitempty"`
}

type NotifyInboxCreated struct {
	RecipientUserID         string `json:"recipient_user_id,omitempty"`
	RecipientPlatformUserID string `json:"recipient_platform_user_id,omitempty"`
	NotificationID          string `json:"notification_id"`
}

type UserEvent struct {
	Origin string          `json:"origin"`
	Keys   []string        `json:"keys"`
	Event  string          `json:"event"`
	Data   json.RawMessage `json:"data"`
}

type NotifyDeliver struct {
	RequestID     string               `json:"request_id"`
	Mode          string               `json:"mode"`
	UserID        string               `json:"user_id"`
	ChannelType   string               `json:"channel_type"`
	Address       json.RawMessage      `json:"address"`
	Title         string               `json:"title,omitempty"`
	Message       string               `json:"message,omitempty"`
	Surface       json.RawMessage      `json:"surface,omitempty"`
	ExpiresAt     string               `json:"expires_at,omitempty"`
	Artifacts     []NotifyArtifact     `json:"artifacts,omitempty"`
	ApproverSet   bool                 `json:"approver_set,omitempty"`
	PromptOutcome *NotifyPromptOutcome `json:"prompt_outcome,omitempty"`
}

type NotifyPromptOutcome struct {
	Outcome string `json:"outcome,omitempty"`
	Text    string `json:"text,omitempty"`
	Refusal string `json:"refusal,omitempty"`

	Retryable bool `json:"retryable,omitempty"`
}

const (
	NotifyOutcomeApproved  = "approved"
	NotifyOutcomeDeclined  = "declined"
	NotifyOutcomeAnswered  = "answered"
	NotifyOutcomeCancelled = "cancelled"
	NotifyOutcomeExpired   = "expired"
	NotifyOutcomeWithdrawn = "withdrawn"
	NotifyOutcomeFailed    = "failed"
)

const (
	HILStatusDelivered = "delivered"
	HILStatusWithdrawn = "withdrawn"
)

const (
	NotifyRefusalApproverSetsDisabled = "approver_sets_disabled"
	NotifyRefusalApproverGroups       = "approver_groups_unsupported"
	NotifyRefusalApproverNoUser       = "approver_set_names_no_user"
	NotifyRefusalApproverTooMany      = "approver_set_too_large"
	NotifyRefusalApproverSystem       = "approver_is_system_principal"
	NotifyRefusalApproverInvalid      = "approver_invalid"
	NotifyRefusalApproverPlaceInvalid = "approver_place_invalid"
	NotifyRefusalApproverNotInput     = "approver_set_requires_input_mode"
	NotifyRefusalDeliveryFailed       = "delivery_failed"
	NotifyRefusalApproverStoreDown    = "approver_store_unavailable"
	NotifyRefusalApproverLookupDown   = "approver_lookup_unavailable"
)

const (
	NotifyResolveNoUser          = "no_user"
	NotifyResolveMissingScope    = "missing_scope"
	NotifyResolveNoDirectMessage = "no_dm"
	NotifyResolveUnavailable     = "unavailable"
)

type NotifyResolveRequest struct {
	RequestID string `json:"request_id"`
	UserID    string `json:"user_id"`
	Email     string `json:"email"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type NotifyResolveResponse struct {
	Address json.RawMessage `json:"address,omitempty"`
	Error   string          `json:"error,omitempty"`
}

func DecodeOrigin(m map[string]string) Origin {
	return Origin{
		Channel:        m[PropChannel],
		UserUUID:       m[PropUserUUID],
		EntrypointUUID: m[PropEntrypointUUID],
		EntrypointName: m[PropEntrypointName],
	}
}

func DecodeUserProperties(m map[string]string) UserProperties {
	var up UserProperties
	if v, ok := m[PropTimestamp]; ok {
		up.Timestamp, _ = strconv.ParseInt(v, 10, 64)
	}
	up.ClientID = m[PropClientID]
	up.UserID = m[PropUserID]
	up.ReplyTo = m[PropReplyTo]
	up.A2AStatusTopic = m[PropA2AStatusTopic]
	up.A2AUserConfig = m[PropA2AUserConfig]
	up.A2AExtensions = m[PropA2AExtensions]
	if v, ok := m[PropCallDepth]; ok {
		up.CallDepth, _ = strconv.Atoi(v)
	}
	up.AuthToken = m[PropAuthToken]
	up.DelegatingAgentName = m[PropDelegatingAgentName]
	up.TraceID = m[PropTraceID]
	up.Trigger = m[PropTrigger]
	up.RootKind = m[PropRootKind]
	up.RootID = m[PropRootID]
	up.RootNodeID = m[PropRootNodeID]
	up.Origin = DecodeOrigin(m)
	up.TriggerSource = TriggerSource{
		TriggerRoute:      m[PropTriggerRoute],
		TriggerEvent:      m[PropTriggerEvent],
		TriggerDeliveryID: m[PropTriggerDeliveryID],
	}
	if v, ok := m[PropGatewayCapabilities]; ok && v != "" {
		caps := map[string]bool{}
		if err := json.Unmarshal([]byte(v), &caps); err == nil && len(caps) > 0 {
			up.GatewayCapabilities = caps
		}
	}
	return up
}
