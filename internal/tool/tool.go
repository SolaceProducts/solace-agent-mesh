// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type Tool interface {
	Name() string

	Description() string

	Parameters() *JSONSchema

	Instructions() string

	IsLongRunning() bool

	RawStringArgs() []string

	ArtifactParams() map[string]ArtifactParamInfo

	ContextParam() string
}

type BuiltinTool interface {
	Tool
	Execute(ctx context.Context, args map[string]any, toolCtx *Context) (*Result, error)
}

type ResolvedEffectExecutor interface {
	BuiltinTool
	ExecuteResolved(ctx context.Context, args map[string]any, toolCtx *Context, effect EffectClass) (*Result, error)
}

func ExecuteWithEffect(ctx context.Context, t BuiltinTool, args map[string]any, toolCtx *Context, effect EffectClass) (*Result, error) {
	if resolved, ok := t.(ResolvedEffectExecutor); ok {
		return resolved.ExecuteResolved(ctx, args, toolCtx, effect)
	}
	return t.Execute(ctx, args, toolCtx)
}

type BrokerTool interface {
	Tool
	InvocationTopic() string
	ResponseTopic() string
}

type ScopedTool interface {
	RequiredScopes() []string
}

func RequiredScopesOf(t Tool) []string {
	if st, ok := t.(ScopedTool); ok {
		return st.RequiredScopes()
	}
	return nil
}

type ResultSchemaProvider interface {
	ResultSchema() *JSONSchema
}

func ResultSchemaOf(t Tool) *JSONSchema {
	if rp, ok := t.(ResultSchemaProvider); ok {
		return rp.ResultSchema()
	}
	return nil
}

type ArtifactNotFoundHinter interface {
	ArtifactNotFoundHint() string
}

func ArtifactNotFoundHintOf(t Tool) string {
	if h, ok := t.(ArtifactNotFoundHinter); ok {
		return h.ArtifactNotFoundHint()
	}
	return ""
}

type ArgumentErrorHinter interface {
	ArgumentErrorHint(args map[string]any) string
}

func ArgumentErrorHintOf(t Tool, args map[string]any) string {
	if h, ok := t.(ArgumentErrorHinter); ok {
		return h.ArgumentErrorHint(args)
	}
	return ""
}

func withArgumentErrorHint(t Tool, args map[string]any, msg string) string {
	if hint := ArgumentErrorHintOf(t, args); hint != "" {
		return msg + "\n" + hint
	}
	return msg
}

type AuthoredSchemaTool interface {
	AuthoredSchemas() bool
}

func HasAuthoredSchemas(t Tool) bool {
	a, ok := t.(AuthoredSchemaTool)
	return ok && a.AuthoredSchemas()
}

type AuthResponseHandler interface {
	CredentialKey() string

	HandleAuthResponse(ctx context.Context, resp *a2a.AuthResponseData, toolCtx *Context) error
}

type MCPAuthResponseHandler = AuthResponseHandler

type AuthSignalBuilder interface {
	BuildAuthSignal(ctx context.Context, toolCtx *Context) (*a2a.AuthenticationRequiredData, error)
}

type StoredCredential struct {
	Connected bool
	ExpiresAt time.Time
	GrantRef  string
}

type StoredCredentialChecker interface {
	CheckStoredCredential(ctx context.Context, toolCtx *Context) StoredCredential
}

type CredentialClearer interface {
	ClearCredential(ctx context.Context, toolCtx *Context) error
}

type UserAuthDescriptor interface {
	SupportsUserAuth() bool
	AuthType() string
	AuthDisplayName() string
}

type ArtifactParamInfo struct {
	IsList     bool
	IsOptional bool

	FilenameKey string
}

type ArtifactSavedInfo struct {
	Filename    string
	Version     int
	MIMEType    string
	SizeBytes   int64
	Description string
	Tags        []string
	ToolCallID  string
}

type VolumeBinding struct {
	VolumeID  string
	MountPath string
	ReadOnly  bool

	AllowExec bool
}

type NetworkBinding struct {
	ParamName  string
	PolicyName string
	Allowlist  []string
}

type VolumeParamInfo struct {
	Name      string
	Mode      string
	MountPath string
}

type VolumeParamProvider interface {
	VolumeParams() []VolumeParamInfo
}

type JSONSchema struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
	Required   []string       `json:"required,omitempty"`
	Raw        map[string]any `json:"-"`
}

func (s *JSONSchema) Map() map[string]any {
	if s == nil {
		return nil
	}
	if s.Raw != nil {
		return s.Raw
	}
	doc := map[string]any{"type": s.Type, "properties": map[string]any{}}
	if s.Properties != nil {
		doc["properties"] = s.Properties
	}
	if len(s.Required) > 0 {
		doc["required"] = s.Required
	}
	return doc
}

type Artifact struct {
	Content  []byte
	Filename string
	Version  int
	MIMEType string
	Metadata map[string]any
}

func (a *Artifact) AsText() string {
	return string(a.Content)
}

type MultimodalPart interface {
	isMultimodalPart()
}

type TextPart struct {
	Text string
}

type ImagePart struct {
	Data        []byte
	MIMEType    string
	Description string
}

type AudioPart struct {
	Data        []byte
	Format      string
	Description string
}

func (TextPart) isMultimodalPart()  {}
func (ImagePart) isMultimodalPart() {}
func (AudioPart) isMultimodalPart() {}

type InternalLLMCallMultimodalOpts struct {
	Model string

	Temperature *float64
}

type Context struct {
	Identity *auth.Identity

	UserID string

	SessionID string

	AgentName string

	ToolConfig map[string]any

	ArtifactService *artifact.ScopedService

	SendStatus func(message string) error

	SendSignal func(signal any) error

	BeginAuthFlow func(userID, traceID, credentialKey string) bool

	EndAuthFlow func(userID, traceID, credentialKey string)

	AuthGaveUp func(userID, traceID, credentialKey string) bool

	Namespace string

	A2AStatusTopic string

	A2AUserConfig string

	ParentTaskID string

	ClientID string

	TraceID string

	Trigger string

	AuthPromptUnavailable bool

	RootKind   string
	RootID     string
	RootNodeID string

	Origin a2a.Origin

	DisplaysTemplateSignals bool

	ToolCallID string

	RemoteSpend *remotetool.SpendAttribution

	BindToolCall func(callCtx *Context, toolName string)

	InternalLLMCall func(ctx context.Context, systemPrompt, userPrompt string, temperature float64) (string, error)

	InternalLLMCallWithModel func(ctx context.Context, model, systemPrompt, userPrompt string, temperature float64) (string, error)

	InternalLLMCallMultimodal func(ctx context.Context, systemPrompt string, parts []MultimodalPart, opts InternalLLMCallMultimodalOpts) (string, error)

	NotifyArtifactSaved func(ctx context.Context, info ArtifactSavedInfo)

	CredentialService auth.CredentialService

	CallDepth int

	IncomingInstruction string

	MaxSubTaskFanout int

	SubTaskCount *atomic.Int64
	SearchTurns  *SearchTurns

	OAuthToken any

	AuthHeaders any

	SkillState SkillStateProvider

	WaitForUserInput func(ctx context.Context, requestID, source string, surface json.RawMessage, timeout time.Duration) *a2a.UserInputResponseData

	PublishUserInput func(ctx context.Context, requestID, source string, surface json.RawMessage, timeout time.Duration)

	PublishUserInputRequest func(ctx context.Context, data a2a.UserInputRequestData)

	PublishNotification func(ctx context.Context, req a2a.NotifyRequest) error

	ScheduleTask func(ctx context.Context, req a2a.ScheduleRequest) error

	PauseResume bool

	PauseRemoteTools bool

	ApprovalReason ApprovalReason

	VolumeBindings []VolumeBinding

	NetworkBindings []NetworkBinding

	CallerVolumeIDs []string

	ProjectID string

	ControlQuerier ControlQuerier

	GatewayCapabilities map[string]bool

	AwaitDeepResearchPlanResponse PlanResponseAwaiter
}

const CapInteractiveUserInput = "interactive_user_input"

const CapLiveWebSurface = "live_web_surface"

func (c *Context) PrepareCall(callID, toolName string) {
	c.ToolCallID = callID
	if c.BindToolCall != nil {
		c.BindToolCall(c, toolName)
	}
}

func (c *Context) approvalReason() ApprovalReason {
	if c == nil {
		return ApprovalNone
	}
	return c.ApprovalReason
}

func (c *Context) CanPromptUser() bool {
	if c == nil {
		return false
	}
	if v, ok := c.GatewayCapabilities[CapInteractiveUserInput]; ok {
		return v
	}
	return c.WaitForUserInput != nil //nolint:staticcheck // legacy waiter is the absent-flag fallback signal
}

func (c *Context) Caller() auth.CallerIdentity {
	if c.Identity != nil {
		return auth.CallerFromIdentity(c.Identity)
	}
	return auth.CallerIdentity{Sub: c.UserID}
}

type PlanResponseAwaiter func(ctx context.Context, planID, userID string) (*PlanResponse, error)

type PlanResponse struct {
	Action string
	Steps  []string
}

type ControlQuerier interface {
	Custom(ctx context.Context, appName, method, customPath string, body json.RawMessage) (json.RawMessage, error)
}

type SkillStateProvider interface {
	IsLoaded(name string) bool
	LoadedNames() []string
	Load(name string)
	Unload(name string)
	Version() int64
}

func (c *Context) Facade() *Facade {
	return NewFacade(c)
}
