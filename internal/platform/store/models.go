// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

const defaultDescriptionFallback = "No description provided"

func defaultStrPtr(p *string, fallback string) *string {
	if p != nil {
		return p
	}
	return &fallback
}

const PackageToolSeparator = "__"

type OutboxEvent struct {
	ID            string  `db:"id"`
	EventType     string  `db:"event_type"`
	AggregateID   string  `db:"aggregate_id"`
	Payload       string  `db:"payload"`
	CreatedAt     int64   `db:"created_at"`
	Published     bool    `db:"published"`
	PublishedAt   *int64  `db:"published_at"`
	RetryCount    int     `db:"retry_count"`
	LastError     *string `db:"last_error"`
	NextAttemptAt int64   `db:"next_attempt_at"`
	Dead          bool    `db:"dead"`
}

const (
	AgentTypeStandard = "standard"
	AgentTypeA2AProxy = "a2a_proxy"
	AgentTypeLink     = "link"
)

type Agent struct {
	ID                       string  `db:"id"`
	Name                     string  `db:"name"`
	Description              *string `db:"description"`
	Type                     string  `db:"type"`
	SystemPrompt             *string `db:"system_prompt"`
	Skills                   *string `db:"skills"`
	SkillRefs                *string `db:"skill_refs"`
	ModelProvider            *string `db:"model_provider"`
	AdditionalConfigurations []byte  `db:"additional_configurations"`
	CreatedBy                string  `db:"created_by"`
	UpdatedBy                string  `db:"updated_by"`
	CreatedTime              int64   `db:"created_time"`
	UpdatedTime              int64   `db:"updated_time"`

	IsEphemeral         bool    `db:"is_ephemeral"`
	ExpiresAt           *int64  `db:"expires_at"`
	SourceSessionID     *string `db:"source_session_id"`
	SourceComponentName *string `db:"source_component_name"`
	SourceComponentKind *string `db:"source_component_kind"`
}

type Connector struct {
	ID          string  `db:"id"`
	Name        string  `db:"name"`
	Description *string `db:"description"`
	Type        string  `db:"type"`
	Subtype     *string `db:"subtype"`
	Values      *string `db:"values"`
	ResourceKey *string `db:"resource_key"`
	ContentHash *string `db:"content_hash"`
	CreatedBy   string  `db:"created_by"`
	UpdatedBy   string  `db:"updated_by"`
	CreatedTime int64   `db:"created_time"`
	UpdatedTime int64   `db:"updated_time"`
}

type AgentDeployment struct {
	ID                  string  `db:"id"`
	AgentID             string  `db:"agent_id"`
	Status              string  `db:"status"`
	Action              string  `db:"action"`
	AgentConfigSnapshot *string `db:"agent_config_snapshot"`
	ConfigHash          *string `db:"config_hash"`
	PromptHash          *string `db:"prompt_hash"`
	ToolsetHash         *string `db:"toolset_hash"`
	CapabilityHash      *string `db:"capability_hash"`
	BehaviourHash       *string `db:"behaviour_hash"`
	CardHash            *string `db:"card_hash"`
	AgentDataSnapshot   *string `db:"agent_data_snapshot"`
	DeployedPayloads    *string `db:"deployed_payloads" json:"-"`
	ErrorMessage        *string `db:"error_message"`
	FailureReason       *string `db:"failure_reason"`
	ErrorID             *string `db:"error_id"`
	CreatedBy           string  `db:"created_by"`
	CreatedTime         int64   `db:"created_time"`
	UpdatedTime         int64   `db:"updated_time"`
}

type Gateway struct {
	ID          string  `db:"id"`
	Slug        string  `db:"slug"`
	DNSID       *string `db:"dns_id"`
	Name        string  `db:"name"`
	Description *string `db:"description"`
	Type        string  `db:"type"`
	Values      *string `db:"values"`
	PublicURL   *string `db:"public_url"`
	CreatedBy   string  `db:"created_by"`
	UpdatedBy   string  `db:"updated_by"`
	CreatedTime int64   `db:"created_time"`
	UpdatedTime int64   `db:"updated_time"`
}

type GatewayDeployment struct {
	ID                    string  `db:"id"`
	GatewayID             string  `db:"gateway_id"`
	Status                string  `db:"status"`
	Action                string  `db:"action"`
	GatewayConfigSnapshot *string `db:"gateway_config_snapshot"`
	ConfigHash            *string `db:"config_hash"`
	PromptTransformHash   *string `db:"prompt_transform_hash"`
	GatewayDataSnapshot   *string `db:"gateway_data_snapshot"`
	ErrorMessage          *string `db:"error_message"`
	FailureReason         *string `db:"failure_reason"`
	ErrorID               *string `db:"error_id"`
	CreatedBy             string  `db:"created_by"`
	CreatedTime           int64   `db:"created_time"`
	UpdatedTime           int64   `db:"updated_time"`
}

type Workflow struct {
	ID          string  `db:"id"`
	Name        string  `db:"name"`
	Description *string `db:"description"`
	AppConfig   string  `db:"app_config"`
	CreatedBy   string  `db:"created_by"`
	UpdatedBy   string  `db:"updated_by"`
	CreatedTime int64   `db:"created_time"`
	UpdatedTime int64   `db:"updated_time"`
}

type WorkflowConnectorBinding struct {
	WorkflowID  string `db:"workflow_id"`
	ConnectorID string `db:"connector_id"`
	RefName     string `db:"ref_name"`
}

type WorkflowBindings struct {
	Connectors []WorkflowConnectorBinding
	Toolsets   []string
}

type WorkflowDeployment struct {
	ID                     string  `db:"id"`
	WorkflowID             string  `db:"workflow_id"`
	Status                 string  `db:"status"`
	Action                 string  `db:"action"`
	WorkflowConfigSnapshot *string `db:"workflow_config_snapshot"`
	ErrorMessage           *string `db:"error_message"`
	FailureReason          *string `db:"failure_reason"`
	ErrorID                *string `db:"error_id"`
	CreatedBy              string  `db:"created_by"`
	CreatedTime            int64   `db:"created_time"`
	UpdatedTime            int64   `db:"updated_time"`
}

type A2AAgent struct {
	ID                    string  `db:"id"`
	AgentURL              string  `db:"agent_url"`
	Name                  string  `db:"name"`
	Description           *string `db:"description"`
	AgentCardLocation     string  `db:"agent_card_location"`
	AgentCardURL          *string `db:"agent_card_url"`
	AgentCardAuthType     *string `db:"agent_card_auth_type"`
	AgentCardAuthConfig   *string `db:"agent_card_auth_config"`
	AgentCardHeaders      *string `db:"agent_card_headers"`
	AgentAuthType         *string `db:"agent_auth_type"`
	AgentAuthConfig       *string `db:"agent_auth_config"`
	TaskHeaders           *string `db:"task_headers"`
	RequestTimeoutSeconds int     `db:"request_timeout_seconds"`
	Skills                *string `db:"skills"`
	InputModes            *string `db:"input_modes"`
	OutputModes           *string `db:"output_modes"`
	ProtocolVersion       string  `db:"protocol_version"`
	TrustedCardJWKSURLs   *string `db:"trusted_card_jwks_urls"`
	RequireSignedCard     bool    `db:"require_signed_card"`
	CreatedBy             string  `db:"created_by"`
	UpdatedBy             string  `db:"updated_by"`
	CreatedTime           int64   `db:"created_time"`
	UpdatedTime           int64   `db:"updated_time"`
}

type A2AAgentDeployment struct {
	A2AAgentID        string  `db:"a2a_agent_id"`
	AgentDeploymentID string  `db:"agent_deployment_id"`
	Action            *string `db:"action"`
	ConfigSnapshot    *string `db:"config_snapshot"`
}

type Skill struct {
	ID              string  `db:"id"`
	Name            string  `db:"name"`
	Description     *string `db:"description"`
	Tags            *string `db:"tags"`
	Origin          string  `db:"origin"`
	DiscoveryStatus string  `db:"discovery_status"`
	HasReferences   bool    `db:"has_references"`
	HasAssets       bool    `db:"has_assets"`
	HasTools        bool    `db:"has_tools"`
	ResourceCount   int     `db:"resource_count"`
	ReferenceCount  int     `db:"reference_count"`
	AssetCount      int     `db:"asset_count"`
	BundledTools    *string `db:"bundled_tools"`
	Toolsets        *string `db:"toolsets"`
	ConfigYAML      *string `db:"config_yaml"`
	ContentHash     *string `db:"content_hash"`
	CreatedBy       string  `db:"created_by"`
	UpdatedBy       string  `db:"updated_by"`
	CreatedTime     int64   `db:"created_time"`
	UpdatedTime     int64   `db:"updated_time"`
}

type ToolPackage struct {
	ID              string  `db:"id"`
	Name            string  `db:"name"`
	Description     *string `db:"description"`
	Runtime         *string `db:"runtime"`
	DiscoveryStatus string  `db:"discovery_status"`
	DiscoveryErrors *string `db:"discovery_errors"`
	ZipResourceKey  *string `db:"zip_resource_key"`
	ZipFilename     *string `db:"zip_filename"`
	ZipFileSize     *int64  `db:"zip_file_size"`
	ContentHash     *string `db:"content_hash"`
	Capabilities    string  `db:"capabilities"`
	CreatedBy       string  `db:"created_by"`
	UpdatedBy       string  `db:"updated_by"`
	CreatedTime     int64   `db:"created_time"`
	UpdatedTime     int64   `db:"updated_time"`
}

type ToolPackageFile struct {
	ID              string  `db:"id"`
	PackageID       string  `db:"package_id"`
	ToolName        string  `db:"tool_name"`
	ToolDescription *string `db:"tool_description"`
	TimeoutSeconds  int     `db:"timeout_seconds"`
	SandboxProfile  string  `db:"sandbox_profile"`
	Parameters      *string `db:"parameters"`
	ConfigSchema    *string `db:"config_schema"`
	Auth            *string `db:"auth"`
	CreatedTime     int64   `db:"created_time"`
}

type AgentToolsetRow struct {
	AgentID       string  `db:"agent_id"`
	Toolset       string  `db:"toolset"`
	ExcludedTools *string `db:"excluded_tools"`
}

type AgentToolsetConfig struct {
	AgentID      string  `db:"agent_id"`
	PackageID    string  `db:"package_id"`
	ConfigValues *string `db:"config_values"`
}

type ToolsetConfig struct {
	PackageID    string  `db:"package_id"`
	ConfigValues *string `db:"config_values"`
	UpdatedBy    string  `db:"updated_by"`
	UpdatedTime  int64   `db:"updated_time"`
}

type AgentBuiltinToolsetConfig struct {
	AgentID         string  `db:"agent_id"`
	ToolsetName     string  `db:"toolset_name"`
	ConfigValues    *string `db:"config_values"`
	VolumeBindings  *string `db:"volume_bindings"`
	NetworkBindings *string `db:"network_bindings"`
}

type SkillConfig struct {
	SkillID      string  `db:"skill_id"`
	ConfigValues *string `db:"config_values"`
	UpdatedBy    string  `db:"updated_by"`
	UpdatedTime  int64   `db:"updated_time"`
}

type AgentSkillRow struct {
	AgentID      string  `db:"agent_id"`
	SkillID      string  `db:"skill_id"`
	ConfigValues *string `db:"config_values"`
}

type RBACRole struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
	CreatedBy   string `db:"created_by"`
	UpdatedBy   string `db:"updated_by"`
	CreatedTime int64  `db:"created_time"`
	UpdatedTime int64  `db:"updated_time"`
	Builtin     bool   `db:"builtin"`
}

type RBACUserRole struct {
	ID           string `db:"id"`
	UserID       string `db:"user_id"`
	RoleID       string `db:"role_id"`
	SubjectType  string `db:"subject_type"`
	SubjectValue string `db:"subject_value"`
	Issuer       string `db:"issuer"`
	CreatedBy    string `db:"created_by"`
	UpdatedBy    string `db:"updated_by"`
	CreatedTime  int64  `db:"created_time"`
	UpdatedTime  int64  `db:"updated_time"`
}

type RBACGrantHolder struct {
	UserID      string  `db:"id"`
	Email       *string `db:"email"`
	Sub         string  `db:"sub"`
	Issuer      string  `db:"issuer"`
	EmailShared bool    `db:"email_shared"`
}

type RoleGrantSubject struct {
	Type   string
	Value  string
	Issuer string
}

const (
	PendingSubjectEmail  = "email"
	PendingSubjectSub    = "sub"
	PendingSubjectLegacy = "legacy"
)

type RBACPendingUserRole struct {
	ID           string `db:"id"`
	SubjectType  string `db:"subject_type"`
	SubjectValue string `db:"subject_value"`
	Issuer       string `db:"issuer"`
	RoleID       string `db:"role_id"`
	CreatedBy    string `db:"created_by"`
	UpdatedBy    string `db:"updated_by"`
	CreatedTime  int64  `db:"created_time"`
	UpdatedTime  int64  `db:"updated_time"`
}

type RBACIdpClaimMapping struct {
	ID           string `db:"id"`
	OIDCProvider string `db:"oidc_provider"`
	ClaimKey     string `db:"claim_key"`
	ClaimValue   string `db:"claim_value"`
	Name         string `db:"name"`
	CreatedBy    string `db:"created_by"`
	UpdatedBy    string `db:"updated_by"`
	CreatedTime  int64  `db:"created_time"`
	UpdatedTime  int64  `db:"updated_time"`
}

type User struct {
	ID             string  `db:"id"`
	Issuer         string  `db:"issuer"`
	Sub            string  `db:"sub"`
	OIDCProvider   string  `db:"oidc_provider"`
	ExternalID     *string `db:"external_id"`
	DisplayName    *string `db:"display_name"`
	UserName       *string `db:"user_name"`
	Email          *string `db:"email"`
	Phone          *string `db:"phone"`
	Source         string  `db:"source"`
	LastLoginAt    *int64  `db:"last_login_at"`
	ProvisionedVia string  `db:"provisioned_via"`
	Active         bool    `db:"active"`
	CreatedBy      string  `db:"created_by"`
	UpdatedBy      string  `db:"updated_by"`
	CreatedTime    int64   `db:"created_time"`
	UpdatedTime    int64   `db:"updated_time"`
}

type APIKey struct {
	ID           string `db:"id"`
	UserID       string `db:"user_id"`
	Label        string `db:"label"`
	KeyHash      string `db:"key_hash"`
	Prefix       string `db:"prefix"`
	ExpiresTime  *int64 `db:"expires_time"`
	LastUsedTime *int64 `db:"last_used_time"`
	CreatedBy    string `db:"created_by"`
	UpdatedBy    string `db:"updated_by"`
	CreatedTime  int64  `db:"created_time"`
	UpdatedTime  int64  `db:"updated_time"`
}

type PlatformIdentityConfig struct {
	ID          string  `db:"id"`
	ToolsetID   *string `db:"toolset_id"`
	LookupKey   string  `db:"lookup_key"`
	CreatedBy   string  `db:"created_by"`
	UpdatedBy   string  `db:"updated_by"`
	CreatedTime int64   `db:"created_time"`
	UpdatedTime int64   `db:"updated_time"`
}

const PlatformIdentityConfigSingletonID = "singleton"

type PlatformWebuiSettings struct {
	ID                    string  `db:"id"`
	AppName               *string `db:"app_name"`
	WelcomeMessage        *string `db:"welcome_message"`
	CollectFeedback       *bool   `db:"collect_feedback"`
	PublishFeedback       *bool   `db:"publish_feedback"`
	FeedbackTaskInfoLevel *string `db:"feedback_task_info_level"`
	DisclaimerText        *string `db:"disclaimer_text"`
	LogoURL               *string `db:"logo_url"`
	SmallLogoURL          *string `db:"small_logo_url"`
	SystemPurpose         *string `db:"system_purpose"`
	ResponseFormat        *string `db:"response_format"`
	CreatedBy             string  `db:"created_by"`
	UpdatedBy             string  `db:"updated_by"`
	CreatedTime           int64   `db:"created_time"`
	UpdatedTime           int64   `db:"updated_time"`
}

const PlatformWebuiSettingsSingletonID = "singleton"

type Tool struct {
	ID             string  `db:"id"`
	Name           string  `db:"name"`
	Description    *string `db:"description"`
	Origin         string  `db:"origin"`
	Status         string  `db:"status"`
	Parameters     *string `db:"parameters"`
	Instructions   *string `db:"instructions"`
	ArtifactParams *string `db:"artifact_params"`
	CreatedBy      string  `db:"created_by"`
	UpdatedBy      string  `db:"updated_by"`
	CreatedTime    int64   `db:"created_time"`
	UpdatedTime    int64   `db:"updated_time"`
}

type UserIdentityClaim struct {
	UserID              string `db:"user_id"`
	ClaimKey            string `db:"claim_key"`
	ClaimValueCanonical string `db:"claim_value_canonical"`
	Verified            bool   `db:"verified"`
	UpdatedTime         int64  `db:"updated_time"`
}

type IdentityClaimWrite struct {
	Replace bool
	Claims  []UserIdentityClaim
}
