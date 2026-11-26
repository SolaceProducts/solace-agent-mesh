// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import "context"

type Identity struct {
	UserID string

	Roles []string

	Scopes []string

	AuthMethod string

	CredentialID string

	PlatformUserID string

	Claims map[string]any

	AuthToken string
}

func (id *Identity) HasScope(required string) bool {
	return HasScope(id.Scopes, required)
}

type Authenticator interface {
	Authenticate(ctx context.Context, authToken string, taskID string) (*Identity, error)
}

type PeerAuthenticator interface {
	AuthenticateWithoutTaskBinding(ctx context.Context, authToken string) (*Identity, error)

	AuthenticateWithSoftTaskBinding(ctx context.Context, authToken string, expectedTaskID string) (*Identity, error)
}

var ErrTaskIDBindingMismatch = errTaskIDBindingMismatch{}

type errTaskIDBindingMismatch struct{}

func (errTaskIDBindingMismatch) Error() string { return "task_id binding mismatch" }

type ToolAuthorizer interface {
	AuthorizeTool(ctx context.Context, identity *Identity, toolName string, requiredScopes []string) error

	FilterTools(ctx context.Context, identity *Identity, tools []ToolInfo) []ToolInfo
}

type ToolInfo struct {
	Name           string
	RequiredScopes []string
}

type AgentAuthorizer interface {
	AuthorizeAgent(ctx context.Context, identity *Identity, agentName string) error

	AuthorizeAgentScopes(ctx context.Context, identity *Identity, requiredScopes []string) error

	FilterAgents(ctx context.Context, identity *Identity, agentNames []string) []string

	AuthorizeWorkflow(ctx context.Context, identity *Identity, workflowName string) error

	FilterWorkflows(ctx context.Context, identity *Identity, workflowNames []string) []string
}

type ControlAuthorizer interface {
	AuthorizeControl(ctx context.Context, identity *Identity, method string, appName string) error
}

type AuditLogger interface {
	LogAuth(ctx context.Context, event AuthEvent)

	LogToolAccess(ctx context.Context, event ToolAccessEvent)

	LogAgentAccess(ctx context.Context, event AgentAccessEvent)

	LogToolExecution(ctx context.Context, event ToolExecutionEvent)

	LogControlAccess(ctx context.Context, event ControlAccessEvent)

	LogShareGrant(ctx context.Context, event ShareGrantEvent)

	LogShareLevelChange(ctx context.Context, event ShareLevelChangeEvent)

	LogShareRevoke(ctx context.Context, event ShareRevokeEvent)

	LogTemplateDeleted(ctx context.Context, event TemplateDeletedEvent)

	LogConsentChange(ctx context.Context, event ConsentChangeEvent)

	LogActivationChange(ctx context.Context, event ActivationChangeEvent)
}

type CredentialService interface {
	Get(ctx context.Context, agentName, appName, userID, key string) (string, error)

	Set(ctx context.Context, agentName, appName, userID, key string, value string) error

	Delete(ctx context.Context, agentName, appName, userID, key string) error

	List(ctx context.Context, agentName, appName, userID string) ([]string, error)
}

type Sealer interface {
	Seal(plaintext string) (string, error)
	Open(sealed string) (string, error)
}

type AuthEvent struct {
	UserID       string
	CredentialID string
	TaskID       string
	Success      bool
	Reason       string
}

type ToolAccessEvent struct {
	UserID       string
	CredentialID string
	ToolName     string
	Allowed      bool
	Scopes       []string
}

type AgentAccessEvent struct {
	UserID       string
	CredentialID string
	AgentName    string
	Allowed      bool
}

type ToolExecutionEvent struct {
	UserID            string
	CredentialID      string
	AgentName         string
	ToolName          string
	ToolCategory      string
	SessionID         string
	TraceID           string
	DurationMs        float64
	Success           bool
	Error             string
	ToolErrorCode     string
	ToolErrorSite     string
	ToolErrorClass    string
	ApprovalReason    string
	ApprovalRefusal   string
	ApprovalRecipient string
}

type ControlAccessEvent struct {
	UserID  string
	Method  string
	AppName string
	Allowed bool
}

type ShareGrantEvent struct {
	Actor          string
	ResourceType   string
	ResourceID     string
	RecipientEmail string
	AccessLevel    string
}

type ShareLevelChangeEvent struct {
	Actor          string
	ResourceType   string
	ResourceID     string
	RecipientEmail string
	AccessLevel    string
}

type ShareRevokeEvent struct {
	Actor          string
	ResourceType   string
	ResourceID     string
	RecipientEmail string
}

type TemplateDeletedEvent struct {
	Actor           string
	ResourceType    string
	ResourceID      string
	RecipientEmails []string
}

type ConsentChangeEvent struct {
	Actor string
	Scope string
	From  string
	To    string
}

const (
	ActivationChangeSignedIn = "signed_in"
	ActivationChangeAccepted = "accepted"
)

type ActivationChangeEvent struct {
	Actor          string
	Change         string
	Subject        string
	FreeModelTerms bool
	AgreementID    string
}

const (
	HILApprovalAsked         = "asked"
	HILApprovalRequestDenied = "request_refused"
	HILApprovalUndeliverable = "undeliverable"
	HILApprovalAccepted      = "accepted"
	HILApprovalRefused       = "refused"
	HILApprovalLate          = "late"
	HILApprovalTimedOut      = "timed_out"
	HILApprovalWithdrawn     = "withdrawn"
)

type HILApprovalEvent struct {
	RequestID  string
	Outcome    string
	Approvers  []string
	PlaceTypes []string
	Answerer   string
	Via        string
	Decision   string
	Reason     string
}

type HILApprovalAuditor interface {
	LogHILApproval(ctx context.Context, event HILApprovalEvent)
}
