// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remotetool

const (
	MethodInvoke          = "sam_remote_tool/invoke"
	MethodStatus          = "sam_remote_tool/status"
	MethodInit            = "sam_remote_tool/init"
	MethodInitRequest     = "sam_remote_tool/init_request"
	MethodRemoved         = "sam_remote_tool/removed"
	MethodFailedDiscovery = "sam_remote_tool/failed_discovery"
)

type RemovedParams struct {
	ToolName string `json:"tool_name"`
}

type FailedDiscoveryParams struct {
	ToolName             string `json:"tool_name"`
	ErrorMessage         string `json:"error_message"`
	ErrorCode            string `json:"error_code,omitempty"`
	RuntimeOS            string `json:"runtime_os,omitempty"`
	RuntimeArch          string `json:"runtime_arch,omitempty"`
	RuntimePythonVersion string `json:"runtime_python_version,omitempty"`
}

type InvokeParams struct {
	TaskID             string                       `json:"task_id"`
	ToolName           string                       `json:"tool_name"`
	Args               map[string]any               `json:"args"`
	ToolConfig         map[string]any               `json:"tool_config"`
	AppName            string                       `json:"app_name"`
	UserID             string                       `json:"user_id"`
	SessionID          string                       `json:"session_id"`
	PreloadedArtifacts map[string]any               `json:"preloaded_artifacts"`
	ArtifactReferences map[string]ArtifactReference `json:"artifact_references"`
	TimeoutSeconds     int                          `json:"timeout_seconds"`
	SandboxProfile     string                       `json:"sandbox_profile"`
	ToolOAuthToken     string                       `json:"tool_oauth_token,omitempty"`
	ToolAuthHeaders    map[string]string            `json:"tool_auth_headers,omitempty"`
	VolumeBindings     []VolumeBinding              `json:"volume_bindings,omitempty"`
	NetworkBindings    []NetworkBinding             `json:"network_bindings,omitempty"`
	ParentTaskID       string                       `json:"parent_task_id,omitempty"`
	TraceID            string                       `json:"trace_id,omitempty"`
	A2AUserConfig      string                       `json:"a2a_user_config,omitempty"`
	Spend              *SpendAttribution            `json:"spend,omitempty"`
	SearchTurn         *int                         `json:"search_turn,omitempty"`
}

type SpendAttribution struct {
	UserID         string `json:"user_id,omitempty"`
	UserSub        string `json:"user_sub,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentName      string `json:"agent_name,omitempty"`
	WorkflowID     string `json:"workflow_id,omitempty"`
	WorkflowName   string `json:"workflow_name,omitempty"`
	WorkflowNodeID string `json:"workflow_node_id,omitempty"`
	RootKind       string `json:"root_kind,omitempty"`
	RootID         string `json:"root_id,omitempty"`
	RootName       string `json:"root_name,omitempty"`
	RootNodeID     string `json:"root_node_id,omitempty"`
	EntrypointID   string `json:"entrypoint_id,omitempty"`
	EntrypointName string `json:"entrypoint_name,omitempty"`
	Channel        string `json:"channel,omitempty"`
	TriggerKind    string `json:"trigger_kind,omitempty"`
	TriggerID      string `json:"trigger_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	ParentTaskID   string `json:"parent_task_id,omitempty"`
	TraceID        string `json:"trace_id,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
}

type VolumeBinding struct {
	VolumeID  string `json:"volume_id"`
	MountPath string `json:"mount_path"`
	ReadOnly  bool   `json:"read_only"`
	AllowExec bool   `json:"allow_exec,omitempty"`
}

type NetworkBinding struct {
	ParamName  string   `json:"param_name"`
	PolicyName string   `json:"policy_name"`
	Allowlist  []string `json:"allowlist"`
}

type ArtifactReference struct {
	Filename string `json:"filename"`
	Version  int    `json:"version"`
}

type PreloadedArtifact struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
	MIMEType string `json:"mime_type,omitempty"`
}

type InvokeResult struct {
	ToolResult       map[string]any    `json:"tool_result"`
	ExecutionTimeMs  int64             `json:"execution_time_ms"`
	TimedOut         bool              `json:"timed_out"`
	CreatedArtifacts []CreatedArtifact `json:"created_artifacts,omitempty"`
}

type CreatedArtifact struct {
	Filename  string `json:"filename"`
	Version   int    `json:"version"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
}

type InvokeError struct {
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	StderrTail string    `json:"stderr_tail,omitempty"`
	StackTrace string    `json:"stack_trace,omitempty"`
	Site       string    `json:"-"`
}

type StatusParams struct {
	TaskID     string `json:"task_id"`
	StatusText string `json:"status_text"`
	Timestamp  string `json:"timestamp"`
}

type ErrorCode string

const (
	ErrSandboxTimeout       ErrorCode = "SANDBOX_TIMEOUT"
	ErrSandboxFailed        ErrorCode = "SANDBOX_FAILED"
	ErrToolNotAvailable     ErrorCode = "TOOL_NOT_AVAILABLE"
	ErrToolNotFound         ErrorCode = "TOOL_NOT_FOUND"
	ErrImportError          ErrorCode = "IMPORT_ERROR"
	ErrExecutionError       ErrorCode = "EXECUTION_ERROR"
	ErrToolError            ErrorCode = "TOOL_ERROR"
	ErrArtifactError        ErrorCode = "ARTIFACT_ERROR"
	ErrArtifactNotFound     ErrorCode = "ARTIFACT_NOT_FOUND"
	ErrInvalidRequest       ErrorCode = "INVALID_REQUEST"
	ErrInternalError        ErrorCode = "INTERNAL_ERROR"
	ErrAuthenticationFailed ErrorCode = "AUTHENTICATION_FAILED"
	ErrVolumeLocked         ErrorCode = "VOLUME_LOCKED"
	ErrVolumeError          ErrorCode = "VOLUME_ERROR"

	ErrToolExitNonzero ErrorCode = "TOOL_EXIT_NONZERO"
	ErrToolTimeout     ErrorCode = "TOOL_TIMEOUT"
	ErrToolSignaled    ErrorCode = "TOOL_SIGNALED"
	ErrToolExecFailed  ErrorCode = "TOOL_EXEC_FAILED"

	ErrToolMemoryExceeded ErrorCode = "TOOL_MEMORY_EXCEEDED"

	ErrSchemaFailed ErrorCode = "SCHEMA_FAILED"
)

const ArtifactVersionNotFoundFormat = "artifact %q version %d not found"

const ArtifactLatestNotFoundFormat = "latest version of %q not found in this session"

func AllErrorCodes() []ErrorCode {
	return []ErrorCode{
		ErrSandboxTimeout, ErrSandboxFailed, ErrToolNotAvailable, ErrToolNotFound, ErrImportError,
		ErrExecutionError, ErrToolError, ErrArtifactError, ErrArtifactNotFound, ErrInvalidRequest, ErrInternalError,
		ErrAuthenticationFailed, ErrVolumeLocked, ErrVolumeError,
		ErrToolExitNonzero, ErrToolTimeout, ErrToolSignaled, ErrToolExecFailed,
		ErrToolMemoryExceeded, ErrSchemaFailed,
	}
}

type InitParams struct {
	ToolName             string                       `json:"tool_name"`
	Description          string                       `json:"description"`
	Parameters           map[string]any               `json:"parameters"`
	ArtifactParams       map[string]ArtifactParamInfo `json:"artifact_params,omitempty"`
	Instructions         string                       `json:"instructions,omitempty"`
	ConfigSchema         []map[string]any             `json:"config_schema,omitempty"`
	Capabilities         map[string]any               `json:"capabilities"`
	Auth                 *AuthDeclaration             `json:"auth,omitempty"`
	RuntimeOS            string                       `json:"runtime_os,omitempty"`
	RuntimeArch          string                       `json:"runtime_arch,omitempty"`
	RuntimePythonVersion string                       `json:"runtime_python_version,omitempty"`

	PackageToolNames []string `json:"package_tool_names,omitempty"`

	VolumeParams []VolumeParam `json:"volume_params,omitempty"`

	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	Effect string `json:"effect,omitempty"`

	InstanceID string `json:"instance_id,omitempty"`
}

const (
	EffectReadOnly      = "read_only"
	EffectSideEffecting = "side_effecting"
)

func ValidEffect(s string) bool {
	return s == EffectReadOnly || s == EffectSideEffecting
}

type VolumeParam struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Mode        string `json:"mode"`
	MountPath   string `json:"mount_path"`
}

type AuthDeclaration struct {
	Type   string                 `json:"type"`
	Scheme *AuthDeclarationScheme `json:"scheme,omitempty"`
}

type AuthDeclarationScheme struct {
	AuthorizationURL        string   `json:"authorization_url,omitempty"`
	TokenURL                string   `json:"token_url,omitempty"`
	Scopes                  []string `json:"scopes,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

type InitRequestParams struct {
	ToolName   string         `json:"tool_name"`
	ToolConfig map[string]any `json:"tool_config"`
}

type ArtifactParamInfo struct {
	IsList      bool   `json:"is_list"`
	IsOptional  bool   `json:"is_optional"`
	FilenameKey string `json:"filename_key,omitempty"`
}

func (c ErrorCode) Retryable() bool {
	return c == ErrToolNotAvailable
}

const (
	PropReplyTo        = "replyTo"
	PropStatusTo       = "statusTo"
	PropClientID       = "clientId"
	PropUserID         = "userId"
	PropAuthToken      = "authToken"
	PropA2AUserConfig  = "a2aUserConfig"
	PropA2AStatusTopic = "a2aStatusTopic"
	PropContextID      = "contextId"
	PropTraceID        = "traceId"
)

type UserProperties struct {
	ReplyTo        string `json:"replyTo"`
	StatusTo       string `json:"statusTo"`
	ClientID       string `json:"clientId"`
	UserID         string `json:"userId"`
	AuthToken      string `json:"authToken,omitempty"`
	A2AUserConfig  string `json:"a2aUserConfig,omitempty"`
	A2AStatusTopic string `json:"a2aStatusTopic,omitempty"`
	ContextID      string `json:"contextId,omitempty"`
	TraceID        string `json:"traceId,omitempty"`
}

func (up *UserProperties) Encode() map[string]string {
	m := map[string]string{
		PropReplyTo:  up.ReplyTo,
		PropStatusTo: up.StatusTo,
		PropClientID: up.ClientID,
		PropUserID:   up.UserID,
	}
	if up.AuthToken != "" {
		m[PropAuthToken] = up.AuthToken
	}
	if up.A2AUserConfig != "" {
		m[PropA2AUserConfig] = up.A2AUserConfig
	}
	if up.A2AStatusTopic != "" {
		m[PropA2AStatusTopic] = up.A2AStatusTopic
	}
	if up.ContextID != "" {
		m[PropContextID] = up.ContextID
	}
	if up.TraceID != "" {
		m[PropTraceID] = up.TraceID
	}
	return m
}

func DecodeUserProperties(m map[string]string) UserProperties {
	return UserProperties{
		ReplyTo:        m[PropReplyTo],
		StatusTo:       m[PropStatusTo],
		ClientID:       m[PropClientID],
		UserID:         m[PropUserID],
		AuthToken:      m[PropAuthToken],
		A2AUserConfig:  m[PropA2AUserConfig],
		A2AStatusTopic: m[PropA2AStatusTopic],
		ContextID:      m[PropContextID],
		TraceID:        m[PropTraceID],
	}
}
