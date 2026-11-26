// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type TestScenario struct {
	TestCaseID             string           `yaml:"test_case_id"`
	Description            string           `yaml:"description"`
	Tags                   []string         `yaml:"tags"`
	SkipIntermediateEvents bool             `yaml:"skip_intermediate_events"`
	GatewayInput           *GatewayInput    `yaml:"gateway_input"`
	GatewayInputs          []GatewayInput   `yaml:"gateway_inputs"`
	LLMInteractions        []LLMInteraction `yaml:"llm_interactions"`
	ExpectedGatewayOutput  []ExpectedEvent  `yaml:"expected_gateway_output"`
	SetupArtifacts         []SetupArtifact  `yaml:"setup_artifacts"`
	ConfigOverrides        *ConfigOverrides `yaml:"test_runner_config_overrides"`

	ExpectedCompletionTimeoutSeconds int                    `yaml:"expected_completion_timeout_seconds"`
	ExpectedArtifacts                []ArtifactAssertion    `yaml:"expected_artifacts"`
	ExpectedVolumeState              []VolumeStateAssertion `yaml:"expected_volume_state"`

	PrimedImageResponses []PrimedImageResponse `yaml:"primed_image_generation_responses"`

	MockGeminiMode string `yaml:"mock_gemini_mode"`

	PrimedSearchResponses      []PrimedSearchResponse `yaml:"primed_search_responses"`
	PrimedTavilyResponses      []PrimedSearchResponse `yaml:"primed_tavily_responses"`
	PrimedExaResponses         []PrimedSearchResponse `yaml:"primed_exa_responses"`
	PrimedPageResponses        []PrimedPageResponse   `yaml:"primed_page_responses"`
	ExpectedSearchRequestCount *int                   `yaml:"expected_search_request_count"`
	WebSearchProvider          string                 `yaml:"web_search_provider"`

	PrimedDecisionResponses []PrimedDecisionResponse `yaml:"primed_decision_responses"`

	FeatureFlags []string `yaml:"feature_flags"`

	SlackConfig        *SlackTestConfig    `yaml:"slack_config"`
	ExpectedSlackCalls []ExpectedSlackCall `yaml:"expected_slack_calls"`

	TeamsConfig        *TeamsTestConfig    `yaml:"teams_config"`
	ExpectedTeamsCalls []ExpectedTeamsCall `yaml:"expected_teams_calls"`

	WhatsAppConfig        *WhatsAppTestConfig    `yaml:"whatsapp_config"`
	ExpectedWhatsAppCalls []ExpectedWhatsAppCall `yaml:"expected_whatsapp_calls"`

	GoogleChatConfig                 *GoogleChatTestConfig         `yaml:"googlechat_config"`
	ExpectedGoogleChatCalls          []ExpectedGoogleChatCall      `yaml:"expected_googlechat_calls"`
	ExpectedGoogleChatMessages       []ExpectedGoogleChatMessage   `yaml:"expected_googlechat_messages"`
	ExpectedGoogleChatDriveFiles     []ExpectedGoogleChatDriveFile `yaml:"expected_googlechat_drive_files"`
	ExpectNoGoogleChatDriveFile      bool                          `yaml:"expect_no_googlechat_drive_file"`
	ExpectedGoogleChatDriveFileCount *int                          `yaml:"expected_googlechat_drive_file_count"`
	NoGoogleChatMessageContains      StringOrList                  `yaml:"no_googlechat_message_contains"`

	EventMeshConfig         *EventMeshTestConfig      `yaml:"event_mesh_config"`
	EventMeshInput          *EventMeshInput           `yaml:"event_mesh_input"`
	ExpectedEventMeshOutput []ExpectedEventMeshOutput `yaml:"expected_event_mesh_output"`

	CancelTrigger *CancelTrigger `yaml:"cancel_trigger"`

	FollowUpPrompts []FollowUpPrompt `yaml:"follow_up_prompts"`

	HILTrigger *HILTrigger `yaml:"hil_trigger"`

	OAuthTrigger *OAuthTrigger `yaml:"oauth_trigger"`

	Timeouts *ScenarioTimeouts `yaml:"timeouts"`

	SetupProject *SetupProject `yaml:"setup_project"`

	SetupScheduledTask *SetupScheduledTask `yaml:"setup_scheduled_task"`

	ExpectedScheduledTaskOutput *ExpectedScheduledTaskOutput `yaml:"expected_scheduled_task_output"`

	ExpectedBackgroundTaskStatus *ExpectedBackgroundTaskStatus `yaml:"expected_background_task_status"`

	SetupBackgroundTasks map[string]any `yaml:"setup_background_tasks"`

	ExpectedBackgroundTaskTimeout *ExpectedBackgroundTaskTimeout `yaml:"expected_background_task_timeout"`

	ExpectedTitleGeneration *ExpectedTitleGeneration `yaml:"expected_title_generation"`

	BuilderChatFlow *BuilderChatFlow `yaml:"builder_chat_flow"`

	SetupDataRetention map[string]any `yaml:"setup_data_retention"`

	ExpectedSSEEventBufferState *ExpectedSSEEventBufferState `yaml:"expected_sse_event_buffer_state"`

	ExpectedRendering *ExpectedRendering `yaml:"expected_rendering"`

	AssertNoCodeVerifierOnStream bool `yaml:"assert_no_code_verifier_on_stream"`

	SetupPlatform *SetupPlatform `yaml:"setup_platform"`

	RequiresGatewayMode string `yaml:"requires_gateway_mode"`

	GatewayAuth *GatewayAuth `yaml:"gateway_auth,omitempty"`

	HTTPFlow []HTTPFlowStep `yaml:"http_flow,omitempty"`

	AssertOIDCCalls []OIDCCallAssert `yaml:"assert_oidc_calls,omitempty"`

	AssertLogsOmit []LogOmissionAssert `yaml:"assert_logs_omit,omitempty"`

	HTTPRequestInput yaml.Node `yaml:"http_request_input"`
	GatewayActions   yaml.Node `yaml:"gateway_actions"`
	MonkeypatchSpec  yaml.Node `yaml:"monkeypatch_spec"`

	Extra map[string]yaml.Node `yaml:",inline"`

	SourcePath string `yaml:"-"`
}

func (s *TestScenario) IsGoSupported() (bool, string) {
	if s.HTTPRequestInput.Kind != 0 {
		return false, "http_request_input not supported in Go"
	}
	if s.MonkeypatchSpec.Kind != 0 {
		return false, "monkeypatch_spec not supported in Go"
	}
	if s.GatewayInput == nil && len(s.GatewayInputs) == 0 && s.EventMeshInput == nil && s.SetupScheduledTask == nil && s.BuilderChatFlow == nil && s.GatewayAuth == nil {
		return false, "no gateway_input, gateway_inputs, event_mesh_input, setup_scheduled_task, builder_chat_flow, or gateway_auth defined"
	}
	return true, ""
}

type GatewayInput struct {
	TargetAgentName      string                    `yaml:"target_agent_name"`
	UserIdentity         string                    `yaml:"user_identity"`
	UserUUID             string                    `yaml:"user_uuid"`
	EntrypointUUID       string                    `yaml:"entrypoint_uuid"`
	Parts                []InputPart               `yaml:"parts"`
	Prompt               *PromptInput              `yaml:"prompt"`
	UserPrompt           string                    `yaml:"user_prompt"`
	A2AParts             []InputPart               `yaml:"a2a_parts"`
	ExternalContext      *ExtContext               `yaml:"external_context"`
	InvokedWithArtifacts []ArtifactRef             `yaml:"invoked_with_artifacts"`
	ToolNames            []string                  `yaml:"tool_names"`
	ToolConfigs          map[string]map[string]any `yaml:"tool_configs"`
	Enterprise           *EnterpriseTestConfig     `yaml:"enterprise"`
	MessageMetadata      map[string]any            `yaml:"message_metadata"`
	GatewayCapabilities  map[string]bool           `yaml:"gateway_capabilities"`
	Trigger              string                    `yaml:"trigger"`
	Channel              string                    `yaml:"channel"`
	Streaming            bool                      `yaml:"streaming"`
	OmitStatusTopic      bool                      `yaml:"omit_status_topic"`
}

type EnterpriseTestConfig struct {
	UserScopes     []string `yaml:"user_scopes"`
	UserRoles      []string `yaml:"user_roles"`
	PlatformUserID string   `yaml:"platform_user_id"`
}

func (g *GatewayInput) GetParts() []InputPart {
	if len(g.Parts) > 0 {
		return g.Parts
	}
	if len(g.A2AParts) > 0 {
		return g.A2AParts
	}
	if g.Prompt != nil && len(g.Prompt.Parts) > 0 {
		return g.Prompt.Parts
	}
	if g.Prompt != nil && g.Prompt.Text != "" {
		return []InputPart{{Type: "text", Text: g.Prompt.Text}}
	}
	return nil
}

type PromptInput struct {
	Text  string
	Parts []InputPart `yaml:"parts"`
}

func (p *PromptInput) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		p.Text = value.Value
		return nil
	}
	type promptInputRaw struct {
		Parts []InputPart `yaml:"parts"`
	}
	var raw promptInputRaw
	if err := value.Decode(&raw); err != nil {
		return err
	}
	p.Parts = raw.Parts
	return nil
}

type InputPart struct {
	Type          string         `yaml:"type"`
	Text          string         `yaml:"text"`
	Kind          string         `yaml:"kind"`
	ContentBase64 string         `yaml:"content_base64"`
	BytesBase64   string         `yaml:"bytes_base64"`
	URI           string         `yaml:"uri"`
	Data          map[string]any `yaml:"data"`
	Filename      string         `yaml:"filename"`
	Name          string         `yaml:"name"`
	MimeType      string         `yaml:"mime_type"`
}

func (p *InputPart) GetText() string {
	return p.Text
}

func resolveFilePartFields(p InputPart) (name, bytesBase64 string) {
	name = p.Filename
	if name == "" {
		name = p.Name
	}
	bytesBase64 = p.ContentBase64
	if bytesBase64 == "" {
		bytesBase64 = p.BytesBase64
	}
	return name, bytesBase64
}

func scenarioHasInlineFileBytes(s *TestScenario) bool {
	check := func(gi *GatewayInput) bool {
		if gi == nil {
			return false
		}
		for _, p := range gi.GetParts() {
			if p.Type != "file" {
				continue
			}
			_, b64 := resolveFilePartFields(p)
			if b64 != "" && p.URI == "" {
				return true
			}
		}
		return false
	}
	if check(s.GatewayInput) {
		return true
	}
	for i := range s.GatewayInputs {
		if check(&s.GatewayInputs[i]) {
			return true
		}
	}
	return false
}

type ExtContext struct {
	A2ASessionID   string `yaml:"a2a_session_id"`
	SourceTestFile string `yaml:"source_test_file"`

	Extra map[string]yaml.Node `yaml:",inline"`
}

type ArtifactRef struct {
	Filename string `yaml:"filename"`
	Version  int    `yaml:"version"`
}

type LLMInteraction struct {
	StepID          string              `yaml:"step_id"`
	StaticResponse  yaml.Node           `yaml:"static_response"`
	ErrorResponse   *LLMErrorResponse   `yaml:"error_response"`
	ExpectedRequest *ExpectedLLMRequest `yaml:"expected_request"`
	LatencyMs       int                 `yaml:"latency_ms"`
}

type LLMErrorResponse struct {
	StatusCode int    `yaml:"status_code"`
	Message    string `yaml:"message"`
}

func (i *LLMInteraction) IsError() bool {
	return i.ErrorResponse != nil
}

func (i *LLMInteraction) StaticResponseJSON() (json.RawMessage, error) {
	var generic any
	if err := i.StaticResponse.Decode(&generic); err != nil {
		return nil, fmt.Errorf("decode static_response: %w", err)
	}
	data, err := json.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("marshal static_response to JSON: %w", err)
	}
	return data, nil
}

type ExpectedLLMRequest struct {
	ToolsPresent                       []string            `yaml:"tools_present"`
	ToolsAbsent                        []string            `yaml:"tools_absent"`
	ToolsDeferred                      []string            `yaml:"tools_deferred"`
	ToolsNotDeferred                   []string            `yaml:"tools_not_deferred"`
	PromptContains                     []string            `yaml:"prompt_contains"`
	PromptNotContains                  []string            `yaml:"prompt_not_contains"`
	SystemPromptContains               []string            `yaml:"system_prompt_contains"`
	SystemPromptNotContains            []string            `yaml:"system_prompt_not_contains"`
	PromptContainsArtifactSummaryFor   []ArtifactRef       `yaml:"prompt_contains_artifact_summary_for"`
	ExpectedToolResponsesInLLMMessages []ToolResponseMatch `yaml:"expected_tool_responses_in_llm_messages"`

	Messages []ExpectedMessage `yaml:"messages"`

	SystemMessageHasCacheControl *bool   `yaml:"system_message_has_cache_control"`
	LastToolHasCacheControl      *bool   `yaml:"last_tool_has_cache_control"`
	CacheControlTTL              *string `yaml:"cache_control_ttl"`
}

type ExpectedMessage struct {
	Role            string `yaml:"role"`
	ContentContains string `yaml:"content_contains"`
	ToolCallID      string `yaml:"tool_call_id"`

	ImageURLCount *int `yaml:"image_url_count"`
}

type ToolResponseMatch struct {
	ToolName                         string         `yaml:"tool_name"`
	ResponseContains                 StringOrList   `yaml:"response_contains"`
	ResponseNotContains              StringOrList   `yaml:"response_not_contains"`
	ResponseJSONMatches              map[string]any `yaml:"response_json_matches"`
	ResponseJSONContains             map[string]any `yaml:"response_json_contains"`
	ToolCallIDMatchesPriorRequestIdx *int           `yaml:"tool_call_id_matches_prior_request_index"`
}

type ExpectedEvent struct {
	Type      string       `yaml:"type"`
	Kind      string       `yaml:"kind"`
	ID        string       `yaml:"id"`
	ContextID string       `yaml:"contextId"`
	Status    *StatusMatch `yaml:"status"`
	Error     *ErrorMatch  `yaml:"error"`

	ErrorCode            int    `yaml:"error_code"`
	ErrorMessageContains string `yaml:"error_message_contains"`

	ContentParts            []PartMatch    `yaml:"content_parts"`
	TaskState               string         `yaml:"task_state"`
	EventPurpose            string         `yaml:"event_purpose"`
	ExpectedToolName        string         `yaml:"expected_tool_name"`
	ExpectedToolArgsContain map[string]any `yaml:"expected_tool_args_contain"`
	FinalFlag               *bool          `yaml:"final_flag"`

	SignalType         string         `yaml:"signal_type"`
	SignalDataContains map[string]any `yaml:"signal_data_contains"`

	RequestIndex int `yaml:"request_index"`

	AssertArtifactState []ArtifactAssertion `yaml:"assert_artifact_state"`

	Extra map[string]yaml.Node `yaml:",inline"`
}

type StatusMatch struct {
	State   string        `yaml:"state"`
	Message *MessageMatch `yaml:"message"`
}

type MessageMatch struct {
	Kind      string      `yaml:"kind"`
	MessageID string      `yaml:"messageId"`
	Role      string      `yaml:"role"`
	Parts     []PartMatch `yaml:"parts"`
}

type PartMatch struct {
	Kind         string       `yaml:"kind"`
	Type         string       `yaml:"type"`
	TextContains StringOrList `yaml:"text_contains"`
	TextExact    string       `yaml:"text_exact"`
	TextRegex    StringOrList `yaml:"text_matches_regex"`

	TextNotContains StringOrList `yaml:"text_not_contains"`

	Filename string `yaml:"filename"`
	MimeType string `yaml:"mime_type"`

	DataContains MapOrList `yaml:"data_contains"`

	ResponseJSONMatches map[string]any `yaml:"response_json_matches"`

	Extra map[string]yaml.Node `yaml:",inline"`
}

type StringOrList []string

func (s *StringOrList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*s = []string{value.Value}
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*s = list
	return nil
}

type MapOrList struct {
	Map  map[string]any
	List []string
}

func (m *MapOrList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.MappingNode {
		m.Map = make(map[string]any)
		return value.Decode(&m.Map)
	}
	if value.Kind == yaml.SequenceNode {
		return value.Decode(&m.List)
	}
	if value.Kind == yaml.ScalarNode {
		m.List = []string{value.Value}
		return nil
	}
	return fmt.Errorf("data_contains: unexpected YAML kind %d", value.Kind)
}

type ErrorMatch struct {
	MessageContains string `yaml:"message_contains"`
	Code            int    `yaml:"code"`
}

type SetupProject struct {
	Name         string                 `yaml:"name"`
	Description  string                 `yaml:"description,omitempty"`
	SessionID    string                 `yaml:"session_id"`
	Artifacts    []SetupProjectArtifact `yaml:"artifacts"`
	WaitForIndex bool                   `yaml:"wait_for_index"`
}

type SetupProjectArtifact struct {
	Filename      string `yaml:"filename"`
	Content       string `yaml:"content"`
	ContentBase64 string `yaml:"content_base64"`
	MimeType      string `yaml:"mime_type"`
}

type SetupScheduledTask struct {
	Name               string `yaml:"name"`
	ScheduleType       string `yaml:"schedule_type"`
	ScheduleExpression string `yaml:"schedule_expression"`
	TargetAgentName    string `yaml:"target_agent_name"`
	TargetType         string `yaml:"target_type"`
	TaskMessage        []any  `yaml:"task_message"`
	Trigger            bool   `yaml:"trigger"`
}

type ExpectedScheduledTaskOutput struct {
	PollTimeoutSeconds      int     `yaml:"poll_timeout_seconds"`
	ExecutionStatus         string  `yaml:"execution_status"`
	ResultSummaryContains   string  `yaml:"result_summary_contains"`
	ErrorMessage            *string `yaml:"error_message"`
	ConsecutiveFailureCount *int    `yaml:"consecutive_failure_count"`
}

type ExpectedBackgroundTaskStatus struct {
	PollTimeoutSeconds int    `yaml:"poll_timeout_seconds"`
	IsBackground       *bool  `yaml:"is_background"`
	IsRunning          *bool  `yaml:"is_running"`
	Status             string `yaml:"status"`
}

type ExpectedBackgroundTaskTimeout struct {
	AgeTaskByMs              int64                `yaml:"age_task_by_ms"`
	MaxExecutionTimeMs       int64                `yaml:"max_execution_time_ms"`
	UseGatewayDefaultTimeout bool                 `yaml:"use_gateway_default_timeout"`
	PollTimeoutSeconds       int                  `yaml:"poll_timeout_seconds"`
	ExpectedStatus           string               `yaml:"expected_status"`
	ExpectNoChange           bool                 `yaml:"expect_no_change"`
	FreshTask                *FreshBackgroundTask `yaml:"fresh_task"`
}

type FreshBackgroundTask struct {
	ID                 string `yaml:"id"`
	UserID             string `yaml:"user_id"`
	MaxExecutionTimeMs int64  `yaml:"max_execution_time_ms"`
	AgeByMs            int64  `yaml:"age_by_ms"`
}

type ExpectedSSEEventBufferState struct {
	DelaySeconds       int `yaml:"delay_seconds"`
	PollTimeoutSeconds int `yaml:"poll_timeout_seconds"`
	ExpectedCount      int `yaml:"expected_count"`
}

type ExpectedTitleGeneration struct {
	UserMessage   string `yaml:"user_message"`
	AgentResponse string `yaml:"agent_response"`
	Force         bool   `yaml:"force"`

	TitleNotEmpty bool   `yaml:"title_not_empty"`
	TitleContains string `yaml:"title_contains"`
	TitleEquals   string `yaml:"title_equals"`
}

type SetupArtifact struct {
	Filename      string         `yaml:"filename"`
	Content       string         `yaml:"content"`
	ContentBase64 string         `yaml:"content_base64"`
	MimeType      string         `yaml:"mime_type"`
	Metadata      map[string]any `yaml:"metadata"`
	Description   string         `yaml:"description"`
	AppName       string         `yaml:"app_name"`
	SessionID     string         `yaml:"session_id"`
}

type SetupPlatform struct {
	Agents     []SetupPlatformAgent     `yaml:"agents"`
	Workflows  []SetupPlatformWorkflow  `yaml:"workflows"`
	Gateways   []SetupPlatformGateway   `yaml:"gateways"`
	Connectors []SetupPlatformConnector `yaml:"connectors"`
	Skills     []SetupPlatformSkill     `yaml:"skills"`
	Tools      []SetupPlatformTool      `yaml:"tools"`
}

type SetupPlatformAgent struct {
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	SystemPrompt string   `yaml:"system_prompt"`
	Toolsets     []string `yaml:"toolsets"`
	InputModes   []string `yaml:"input_modes"`
	OutputModes  []string `yaml:"output_modes"`
}

type SetupPlatformWorkflow struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	AppConfig   map[string]any `yaml:"app_config"`
}

type SetupPlatformGateway struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
}

type SetupPlatformConnector struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
	Subtype     string `yaml:"subtype"`
}

type SetupPlatformSkill struct {
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Tags          []string `yaml:"tags"`
	HasReferences bool     `yaml:"has_references"`
	HasTools      bool     `yaml:"has_tools"`
	ResourceCount int      `yaml:"resource_count"`
}

type SetupPlatformTool struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	Parameters   any    `yaml:"parameters"`
	Instructions string `yaml:"instructions"`
}

type ArtifactAssertion struct {
	Filename                 string         `yaml:"filename"`
	FilenameMatchesRegex     string         `yaml:"filename_matches_regex"`
	Version                  int            `yaml:"version"`
	MIMEType                 string         `yaml:"mime_type"`
	ExpectedContentText      string         `yaml:"expected_content_text"`
	ExpectedContentBytesB64  string         `yaml:"expected_content_bytes_base64"`
	ContentBase64Exact       string         `yaml:"content_base64_exact"`
	ContentContains          StringOrList   `yaml:"content_contains"`
	JSONContains             map[string]any `yaml:"json_contains"`
	ExpectedMetadataContains map[string]any `yaml:"expected_metadata_contains"`
	MetadataContains         map[string]any `yaml:"metadata_contains"`
	UserID                   string         `yaml:"user_id"`
	SessionID                string         `yaml:"session_id"`
	ExpectedTags             []string       `yaml:"expected_tags"`
}

type ConfigOverrides struct {
	AgentConfig *AgentConfigOverride `yaml:"agent_config"`
}

type AgentConfigOverride struct {
	ArtifactScope                     string         `yaml:"artifact_scope"`
	MaxLLMCallsPerTask                int            `yaml:"max_llm_calls_per_task"`
	MCPToolResponseSaveThresholdBytes *int           `yaml:"mcp_tool_response_save_threshold_bytes"`
	MCPToolLLMReturnMaxBytes          *int           `yaml:"mcp_tool_llm_return_max_bytes"`
	MCPIntelligentProcessing          map[string]any `yaml:"mcp_intelligent_processing"`
	ToolOutputLLMReturnMaxBytes       *int           `yaml:"tool_output_llm_return_max_bytes"`
	SchemaInferenceDepth              *int           `yaml:"schema_inference_depth"`
	CacheStrategy                     string         `yaml:"cache_strategy"`
	ReasoningBudgetTokens             int            `yaml:"reasoning_budget_tokens"`
	PeerRequestTimeoutSeconds         int            `yaml:"peer_request_timeout_seconds"`

	InjectResponseFormat *bool `yaml:"inject_response_format"`

	EnableInlineVision    *bool `yaml:"enable_inline_vision"`
	MaxInlineVisionImages int   `yaml:"max_inline_vision_images"`
	MaxInlineVisionBytes  int   `yaml:"max_inline_vision_bytes"`

	MaxInlineArtifactBytes *int `yaml:"max_inline_artifact_bytes"`

	AutoSummarization *AutoSummarizationOverride `yaml:"auto_summarization"`

	Skills []TestSkillConfig `yaml:"skills"`

	UseRealSkills []string `yaml:"use_real_skills"`

	VolumeSlots []VolumeSlotConfig `yaml:"volume_slots"`

	ToolVolumeBindings map[string]map[string]string `yaml:"tool_volume_bindings"`

	SetupVolumes []SetupVolume `yaml:"setup_volumes"`
}

type AutoSummarizationOverride struct {
	Enabled              bool     `yaml:"enabled"`
	CompactionPercentage *float64 `yaml:"compaction_percentage,omitempty"`
	SummaryMaxTokens     *int     `yaml:"summary_max_tokens,omitempty"`
}

type TestSkillConfig struct {
	Name string `yaml:"name"`

	Description string `yaml:"description,omitempty"`

	Tags []string `yaml:"tags,omitempty"`

	InstructionContent string `yaml:"instruction_content"`

	References map[string]string `yaml:"references,omitempty"`

	Assets map[string]string `yaml:"assets,omitempty"`

	Templates map[string]TestSkillTemplate `yaml:"templates,omitempty"`

	Tools []map[string]any `yaml:"tools,omitempty"`

	Toolsets []string `yaml:"toolsets,omitempty"`

	BundledGoTool bool `yaml:"bundled_go_tool,omitempty"`

	NotConfigured bool `yaml:"not_configured,omitempty"`

	BundledPythonTool bool `yaml:"bundled_python_tool,omitempty"`

	BundledPythonWheelTool bool `yaml:"bundled_python_wheel_tool,omitempty"`
}

type TestSkillTemplate struct {
	Body    string `yaml:"body"`
	Sidecar string `yaml:"sidecar"`
}

type HILTrigger struct {
	Response *HILResponse `yaml:"response"`

	TimeoutOverrideMs int `yaml:"timeout_override_ms"`
}

type HILResponse struct {
	Status string         `yaml:"status"`
	Data   map[string]any `yaml:"data"`
}

type OAuthTrigger struct {
	ClientID               string `yaml:"client_id"`
	ClientSecret           string `yaml:"client_secret"`
	CredentialKey          string `yaml:"credential_key"`
	UserID                 string `yaml:"user_id"`
	TokenExpirySeconds     int64  `yaml:"token_expiry_seconds"`
	NoRefreshToken         bool   `yaml:"no_refresh_token"`
	RotateRefreshTokens    bool   `yaml:"rotate_refresh_tokens"`
	InjectWrongState       bool   `yaml:"inject_wrong_state"`
	CancelOnAuthRequired   bool   `yaml:"cancel_on_auth_required"`
	RejectTokenExchange    bool   `yaml:"reject_token_exchange"`
	NeverComplete          bool   `yaml:"never_complete"`
	ExpectedAuthCodeGrants *int   `yaml:"expected_auth_code_grants"`
	ExpectedRefreshGrants  *int   `yaml:"expected_refresh_grants"`
	ExpectedErrors         *int   `yaml:"expected_errors"`
}

type ScenarioTimeouts struct {
	AuthPendingMs int `yaml:"auth_pending_ms"`
}

type FollowUpPrompt struct {
	Parts              []InputPart `yaml:"parts"`
	UserPrompt         string      `yaml:"user_prompt"`
	AssertCleanHistory bool        `yaml:"assert_clean_history"`
	ExpectedTaskState  string      `yaml:"expected_task_state"`
}

type CancelTrigger struct {
	AfterResponse int `yaml:"after_response"`

	AfterToolStart string `yaml:"after_tool_start"`
}

func LoadScenario(path string) (*TestScenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}

	var sc TestScenario
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	if err := rejectMisspelledLogOmissionKey(data); err != nil {
		return nil, fmt.Errorf("parse scenario %s: %w", path, err)
	}
	if err := sc.validateGatewayAuth(); err != nil {
		return nil, fmt.Errorf("validate scenario %s: %w", path, err)
	}
	if err := sc.validateLogOmissions(); err != nil {
		return nil, fmt.Errorf("validate scenario %s: %w", path, err)
	}
	if sc.GoogleChatConfig != nil {
		if err := sc.GoogleChatConfig.validate(); err != nil {
			return nil, fmt.Errorf("validate scenario %s: %w", path, err)
		}
	}
	if err := sc.validateGoogleChatDriveExpectations(); err != nil {
		return nil, fmt.Errorf("validate scenario %s: %w", path, err)
	}
	sc.SourcePath = path
	return &sc, nil
}

func (s *TestScenario) validateLogOmissions() error {
	if len(s.AssertLogsOmit) > 0 && slices.Contains(s.Tags, "proxy") && len(s.LLMInteractions) == 0 {
		return fmt.Errorf("assert_logs_omit is not supported on a pure-proxy scenario (tagged \"proxy\" with no llm_interactions): it runs on ProxyTestHarness, which has no log capture to assert against, so the canary would never run and the scenario would pass having checked nothing")
	}

	for i, la := range s.AssertLogsOmit {
		if len(la.Markers) == 0 {
			return fmt.Errorf("assert_logs_omit[%d]: markers must not be empty", i)
		}
		for j, m := range la.Markers {
			if strings.TrimSpace(m) == "" {
				return fmt.Errorf("assert_logs_omit[%d].markers[%d] is blank — a blank marker matches every log line", i, j)
			}
			if reason := unmatchableMarkerReason(m); reason != "" {
				return fmt.Errorf("assert_logs_omit[%d].markers[%d] %q can never match: %s. Pick an excerpt without it — the marker is compared against the handler's rendered output, so a marker the handler rewrites is a canary that cannot fail", i, j, m, reason)
			}
		}
		if strings.TrimSpace(la.RequirePresent) == "" {
			return fmt.Errorf("assert_logs_omit[%d]: require_present is required — an absence-only assertion passes when the marker never entered the system, so it cannot distinguish a fixed leak from a scenario that stopped exercising the path", i)
		}
		if strings.TrimSpace(la.Description) == "" {
			return fmt.Errorf("assert_logs_omit[%d]: description is required so a failure says what leaked", i)
		}
		for _, m := range la.Markers {
			if strings.Contains(la.RequirePresent, m) || strings.Contains(m, la.RequirePresent) {
				return fmt.Errorf("assert_logs_omit[%d]: require_present %q overlaps marker %q — the anchor would satisfy itself by matching the very text the assertion forbids", i, la.RequirePresent, m)
			}
		}
	}

	return nil
}

func rejectMisspelledLogOmissionKey(data []byte) error {
	var top map[string]any
	if err := yaml.Unmarshal(data, &top); err != nil {
		return nil
	}
	const want = "assert_logs_omit"
	for key := range top {
		if key == want {
			continue
		}
		if strings.Contains(strings.ReplaceAll(key, "_", ""), "omit") {
			return fmt.Errorf("top-level key %q looks like a misspelling of %q; an unrecognised key is silently discarded, so the assertion would never run", key, want)
		}
	}
	return nil
}

func unmatchableMarkerReason(marker string) string {
	if strings.ContainsAny(marker, "\"\\") {
		return "it contains a quote or backslash, which the log handler escapes"
	}
	for _, r := range marker {
		if r < 0x20 || r == 0x7f {
			return "it contains a control character (newline, tab, or similar), which the log handler escapes"
		}
	}
	if strings.TrimSpace(marker) != marker {
		return "it has leading or trailing whitespace, which does not survive the handler's field rendering predictably"
	}
	return ""
}

func LoadScenariosFromDir(dir string) ([]*TestScenario, error) {
	var scenarios []*TestScenario
	var parseErrors []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		sc, err := LoadScenario(path)
		if err != nil {
			parseErrors = append(parseErrors, fmt.Sprintf("%s: %v", path, err))
			return nil
		}
		scenarios = append(scenarios, sc)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(parseErrors) > 0 {
		return nil, fmt.Errorf("%d scenario file(s) failed to load:\n  %s",
			len(parseErrors), strings.Join(parseErrors, "\n  "))
	}

	return scenarios, nil
}

type EventMeshTestConfig struct {
	EventHandlers  []map[string]any `yaml:"event_handlers"`
	OutputHandlers []map[string]any `yaml:"output_handlers"`
	AckPolicy      map[string]any   `yaml:"acknowledgment_policy"`
}

type EventMeshInput struct {
	Topic          string            `yaml:"topic"`
	Payload        any               `yaml:"payload"`
	UserProperties map[string]string `yaml:"user_properties"`
}

type ExpectedEventMeshOutput struct {
	TopicContains       string         `yaml:"topic_contains"`
	TopicExact          string         `yaml:"topic_exact"`
	PayloadContains     map[string]any `yaml:"payload_contains"`
	PayloadTextContains StringOrList   `yaml:"payload_text_contains"`
	PayloadExact        string         `yaml:"payload_exact"`
}

type VolumeSlotConfig struct {
	Name      string `yaml:"name"`
	Provision string `yaml:"provision"`
	VolumeID  string `yaml:"volume_id,omitempty"`
	TTL       string `yaml:"ttl,omitempty"`
}

type SetupVolume struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	UserID      string `yaml:"user_id"`
	CreatorType string `yaml:"creator_type"`
	TTL         string `yaml:"ttl,omitempty"`
	SessionID   string `yaml:"session_id,omitempty"`
}

type VolumeStateAssertion struct {
	NameContains string `yaml:"name_contains,omitempty"`
	UserID       string `yaml:"user_id,omitempty"`
	CreatorType  string `yaml:"creator_type,omitempty"`
	SessionID    string `yaml:"session_id,omitempty"`
	VolumeCount  *int   `yaml:"volume_count,omitempty"`
}

type Scenario = TestScenario

type GatewayAuth struct {
	FakeIDPs             []FakeIDPSpec  `yaml:"fake_idps"`
	Catalog              []CatalogEntry `yaml:"catalog"`
	ExternalAuthProvider string         `yaml:"external_auth_provider"`
	SessionSecretKey     string         `yaml:"session_secret_key,omitempty"`
	AuthorizationService map[string]any `yaml:"authorization_service,omitempty"`
}

type FakeIDPSpec struct {
	Name              string         `yaml:"name"`
	ClientID          string         `yaml:"client_id"`
	ClientSecret      string         `yaml:"client_secret"`
	Users             []FakeOIDCUser `yaml:"users"`
	IDTokenOnlyClaims []string       `yaml:"id_token_only_claims,omitempty"`
}

type CatalogEntry struct {
	Name   string   `yaml:"name"`
	IDPRef string   `yaml:"idp_ref"`
	Scopes []string `yaml:"scopes,omitempty"`
}

type HTTPFlowStep struct {
	Step    string         `yaml:"step"`
	FakeIDP string         `yaml:"fake_idp,omitempty"`
	AsUser  string         `yaml:"as_user,omitempty"`
	Method  string         `yaml:"method,omitempty"`
	Path    string         `yaml:"path,omitempty"`
	Bearer  *BearerSpec    `yaml:"bearer,omitempty"`
	Expect  HTTPFlowExpect `yaml:"expect"`
}

type BearerSpec struct {
	Literal     string         `yaml:"literal,omitempty"`
	FromFakeIDP *BearerFromIDP `yaml:"from_fake_idp,omitempty"`
}

type BearerFromIDP struct {
	Name string `yaml:"name"`
	Sub  string `yaml:"sub"`
}

type HTTPFlowExpect struct {
	Status           int    `yaml:"status,omitempty"`
	FinalStatus      int    `yaml:"final_status,omitempty"`
	SessionCookieSet bool   `yaml:"session_cookie_set,omitempty"`
	CookieName       string `yaml:"cookie_name,omitempty"`
	BodyContains     string `yaml:"body_contains,omitempty"`
}

type LogOmissionAssert struct {
	Description    string   `yaml:"description"`
	Markers        []string `yaml:"markers"`
	RequirePresent string   `yaml:"require_present"`
}

type OIDCCallAssert struct {
	FakeIDP               string `yaml:"fake_idp"`
	AuthorizeCount        *int   `yaml:"authorize_count,omitempty"`
	AuthorizeCountAtLeast *int   `yaml:"authorize_count_at_least,omitempty"`
	TokenCount            *int   `yaml:"token_count,omitempty"`
	TokenCountAtLeast     *int   `yaml:"token_count_at_least,omitempty"`
	UserinfoCount         *int   `yaml:"userinfo_count,omitempty"`
	UserinfoCountAtLeast  *int   `yaml:"userinfo_count_at_least,omitempty"`
}

func (s *TestScenario) validateGatewayAuth() error {
	if s.GatewayAuth == nil {
		return nil
	}
	ga := s.GatewayAuth

	idpNames := make(map[string]struct{}, len(ga.FakeIDPs))
	for i, idp := range ga.FakeIDPs {
		if _, dup := idpNames[idp.Name]; dup {
			return fmt.Errorf("gateway_auth.fake_idps[%d]: duplicate name %q", i, idp.Name)
		}
		idpNames[idp.Name] = struct{}{}
	}

	catalogNames := make(map[string]struct{}, len(ga.Catalog))
	for i, ce := range ga.Catalog {
		if _, ok := idpNames[ce.IDPRef]; !ok {
			return fmt.Errorf("gateway_auth.catalog[%d] (%q): idp_ref %q does not match any fake_idps[*].name", i, ce.Name, ce.IDPRef)
		}
		if _, dup := catalogNames[ce.Name]; dup {
			return fmt.Errorf("gateway_auth.catalog[%d]: duplicate name %q", i, ce.Name)
		}
		catalogNames[ce.Name] = struct{}{}
	}

	if len(s.HTTPFlow) == 0 {
		return fmt.Errorf("gateway_auth requires a non-empty http_flow")
	}

	if ga.ExternalAuthProvider != "" && ga.ExternalAuthProvider != "enterprise" {
		if _, ok := catalogNames[ga.ExternalAuthProvider]; !ok {
			return fmt.Errorf("gateway_auth.external_auth_provider %q does not match any catalog[*].name", ga.ExternalAuthProvider)
		}
	}

	for i, step := range s.HTTPFlow {
		switch step.Step {
		case "login":
			if step.FakeIDP == "" {
				return fmt.Errorf("http_flow[%d] (step=%q): fake_idp is required", i, step.Step)
			}
			if _, ok := idpNames[step.FakeIDP]; !ok {
				return fmt.Errorf("http_flow[%d] (step=%q): fake_idp %q does not match any fake_idps[*].name", i, step.Step, step.FakeIDP)
			}
			if step.Expect.Status != 0 {
				return fmt.Errorf("http_flow[%d] (step=%q): expect.status is not allowed; use expect.final_status", i, step.Step)
			}
			if step.Expect.BodyContains != "" {
				return fmt.Errorf("http_flow[%d] (step=%q): expect.body_contains is not allowed on a login step", i, step.Step)
			}
		case "request", "request_with_bearer":
			if step.Expect.FinalStatus != 0 {
				return fmt.Errorf("http_flow[%d] (step=%q): expect.final_status is not allowed; use expect.status", i, step.Step)
			}
			if step.Expect.SessionCookieSet {
				return fmt.Errorf("http_flow[%d] (step=%q): expect.session_cookie_set is not allowed on a non-login step", i, step.Step)
			}
			if step.Step == "request_with_bearer" && step.Bearer == nil {
				return fmt.Errorf("http_flow[%d] (step=%q): bearer is required", i, step.Step)
			}
			if step.Step == "request" && step.Bearer != nil {
				return fmt.Errorf("http_flow[%d] (step=%q): bearer is not allowed; use step: request_with_bearer", i, step.Step)
			}
		default:
			return fmt.Errorf("http_flow[%d]: unknown step %q (must be one of login, request, request_with_bearer)", i, step.Step)
		}

		if step.Bearer != nil {
			hasLiteral := step.Bearer.Literal != ""
			hasFromIDP := step.Bearer.FromFakeIDP != nil
			if hasLiteral && hasFromIDP {
				return fmt.Errorf("http_flow[%d].bearer: literal and from_fake_idp are mutually exclusive", i)
			}
			if !hasLiteral && !hasFromIDP {
				return fmt.Errorf("http_flow[%d].bearer: exactly one of literal or from_fake_idp must be set", i)
			}
			if hasFromIDP {
				if _, ok := idpNames[step.Bearer.FromFakeIDP.Name]; !ok {
					return fmt.Errorf("http_flow[%d].bearer.from_fake_idp.name %q does not match any fake_idps[*].name", i, step.Bearer.FromFakeIDP.Name)
				}
			}
		}
	}

	for i, oa := range s.AssertOIDCCalls {
		if _, ok := idpNames[oa.FakeIDP]; !ok {
			return fmt.Errorf("assert_oidc_calls[%d].fake_idp %q does not match any fake_idps[*].name", i, oa.FakeIDP)
		}
		if oa.AuthorizeCount != nil && oa.AuthorizeCountAtLeast != nil {
			return fmt.Errorf("assert_oidc_calls[%d]: authorize_count and authorize_count_at_least are mutually exclusive", i)
		}
		if oa.TokenCount != nil && oa.TokenCountAtLeast != nil {
			return fmt.Errorf("assert_oidc_calls[%d]: token_count and token_count_at_least are mutually exclusive", i)
		}
		if oa.UserinfoCount != nil && oa.UserinfoCountAtLeast != nil {
			return fmt.Errorf("assert_oidc_calls[%d]: userinfo_count and userinfo_count_at_least are mutually exclusive", i)
		}
	}

	return nil
}
