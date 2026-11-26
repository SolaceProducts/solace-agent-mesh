// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"context"
	"fmt"
)

type EmptyTokenAuthenticator interface {
	AllowEmptyToken()
}

type NoopAuthenticator struct{}

func (NoopAuthenticator) AllowEmptyToken() {}

var _ EmptyTokenAuthenticator = NoopAuthenticator{}

func (NoopAuthenticator) Authenticate(_ context.Context, _ string, _ string) (*Identity, error) {
	return nil, nil
}

func (NoopAuthenticator) AuthenticateWithoutTaskBinding(_ context.Context, _ string) (*Identity, error) {
	return nil, nil
}

func (NoopAuthenticator) AuthenticateWithSoftTaskBinding(_ context.Context, _ string, _ string) (*Identity, error) {
	return nil, nil
}

type NoopToolAuthorizer struct{}

func (NoopToolAuthorizer) AuthorizeTool(_ context.Context, _ *Identity, _ string, _ []string) error {
	return nil
}

func (NoopToolAuthorizer) FilterTools(_ context.Context, _ *Identity, tools []ToolInfo) []ToolInfo {
	return tools
}

type NoopAgentAuthorizer struct{}

func (NoopAgentAuthorizer) AuthorizeAgent(_ context.Context, _ *Identity, _ string) error {
	return nil
}

func (NoopAgentAuthorizer) AuthorizeAgentScopes(_ context.Context, _ *Identity, _ []string) error {
	return nil
}

func (NoopAgentAuthorizer) FilterAgents(_ context.Context, _ *Identity, agentNames []string) []string {
	return agentNames
}

func (NoopAgentAuthorizer) AuthorizeWorkflow(_ context.Context, _ *Identity, _ string) error {
	return nil
}

func (NoopAgentAuthorizer) FilterWorkflows(_ context.Context, _ *Identity, workflowNames []string) []string {
	return workflowNames
}

type NoopCredentialService struct{}

func (NoopCredentialService) Get(_ context.Context, _, _, _, _ string) (string, error) {
	return "", nil
}

func (NoopCredentialService) Set(_ context.Context, _, _, _, _ string, _ string) error {
	return nil
}

func (NoopCredentialService) Delete(_ context.Context, _, _, _, _ string) error {
	return nil
}

func (NoopCredentialService) List(_ context.Context, _, _, _ string) ([]string, error) {
	return nil, nil
}

type NoopControlAuthorizer struct{}

func (NoopControlAuthorizer) AuthorizeControl(_ context.Context, _ *Identity, _ string, _ string) error {
	return nil
}

type DenyAllControlAuthorizer struct{}

func (DenyAllControlAuthorizer) AuthorizeControl(_ context.Context, _ *Identity, _ string, _ string) error {
	return fmt.Errorf("control plane access denied")
}

type NoopAuditLogger struct{}

func (NoopAuditLogger) LogAuth(_ context.Context, _ AuthEvent) {}

func (NoopAuditLogger) LogToolAccess(_ context.Context, _ ToolAccessEvent) {}

func (NoopAuditLogger) LogAgentAccess(_ context.Context, _ AgentAccessEvent) {}

func (NoopAuditLogger) LogToolExecution(_ context.Context, _ ToolExecutionEvent) {}

func (NoopAuditLogger) LogControlAccess(_ context.Context, _ ControlAccessEvent) {}

func (NoopAuditLogger) LogShareGrant(_ context.Context, _ ShareGrantEvent) {
}

func (NoopAuditLogger) LogShareLevelChange(_ context.Context, _ ShareLevelChangeEvent) {
}

func (NoopAuditLogger) LogShareRevoke(_ context.Context, _ ShareRevokeEvent) {
}

func (NoopAuditLogger) LogTemplateDeleted(_ context.Context, _ TemplateDeletedEvent) {
}

func (NoopAuditLogger) LogConsentChange(_ context.Context, _ ConsentChangeEvent) {
}

func (NoopAuditLogger) LogActivationChange(_ context.Context, _ ActivationChangeEvent) {
}
