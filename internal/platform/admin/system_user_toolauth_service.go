// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const auditResourceSystemUserToolAuth = "system_user_tool_auth"

const (
	auditActionConnectBegun     = "connect_begun"
	auditActionConnectCompleted = "connect_completed"
	auditActionDisconnected     = "disconnected"
)

type SystemUserToolCredential struct {
	CredentialKey string
	DisplayName   string
	AuthType      string
	Interactive   bool
	Status        string
	ExpiresAt     time.Time
	Record        *store.SystemUserToolAuth
}

type SystemUserAgentToolAuth struct {
	SystemUserID string
	Principal    string
	AgentName    string
	Credentials  []SystemUserToolCredential
}

type SystemUserToolAuthService struct {
	plane  *evalHandler
	rbac   *RBACService
	db     *store.DB
	logger *slog.Logger
	now    func() time.Time
}

func newSystemUserToolAuthService(plane *evalHandler, rbacSvc *RBACService, db *store.DB, logger *slog.Logger) *SystemUserToolAuthService {
	return &SystemUserToolAuthService{plane: plane, rbac: rbacSvc, db: db, logger: logger, now: time.Now}
}

type systemUserToolAuthTarget struct {
	systemUserID string
	principal    string
	agentName    string
	routingName  string
}

func (s *SystemUserToolAuthService) AgentCredentials(ctx context.Context, systemUserID, agentName string) (SystemUserAgentToolAuth, error) {
	target, err := s.resolve(ctx, systemUserID, agentName)
	if err != nil {
		return SystemUserAgentToolAuth{}, err
	}
	items, err := s.query(ctx, target)
	if err != nil {
		return SystemUserAgentToolAuth{}, err
	}
	records, err := store.ListSystemUserToolAuth(ctx, s.db, target.principal, target.agentName)
	if err != nil {
		return SystemUserAgentToolAuth{}, err
	}
	credentials := make([]SystemUserToolCredential, len(items))
	for i, item := range items {
		credential := SystemUserToolCredential{
			CredentialKey: item.CredentialKey,
			DisplayName:   item.DisplayName,
			AuthType:      item.AuthType,
			Interactive:   item.Interactive,
			Status:        item.Status,
			ExpiresAt:     item.ExpiresAt,
		}
		if record, ok := records[item.CredentialKey]; ok {
			s.observeCompletion(ctx, target, item, &record)
			credential.Record = &record
		}
		credentials[i] = credential
	}
	return SystemUserAgentToolAuth{
		SystemUserID: target.systemUserID,
		Principal:    target.principal,
		AgentName:    target.agentName,
		Credentials:  credentials,
	}, nil
}

func (s *SystemUserToolAuthService) Begin(ctx context.Context, systemUserID, agentName, credentialKey, actor string) (ToolAuthBegun, error) {
	target, err := s.resolve(ctx, systemUserID, agentName)
	if err != nil {
		return ToolAuthBegun{}, err
	}
	credentialKey, err = requireCredentialKey(credentialKey)
	if err != nil {
		return ToolAuthBegun{}, err
	}
	items, err := s.query(ctx, target)
	if err != nil {
		return ToolAuthBegun{}, err
	}
	held, err := heldCredential(items, credentialKey)
	if err != nil {
		return ToolAuthBegun{}, err
	}
	if !held.Interactive {
		return ToolAuthBegun{}, &ValidationError{Field: "credentialKey",
			Message: fmt.Sprintf("credential %q cannot be authorized from a browser", credentialKey)}
	}
	controlTarget := s.controlTarget(target, credentialKey)
	raw, err := s.plane.callTargetToolAuth(ctx, controlTarget, "begin")
	if err != nil {
		return ToolAuthBegun{}, err
	}
	var authData a2a.AuthenticationRequiredData
	if err = json.Unmarshal(raw, &authData); err != nil {
		return ToolAuthBegun{}, fmt.Errorf("decode toolAuth begin response: %w", err)
	}
	err = s.plane.registerToolAuthState(ctx, &authData, target.principal, target.routingName)
	if err != nil {
		return ToolAuthBegun{}, err
	}
	state, err := a2a.StateFromAuthURI(authData.AuthURI)
	if err != nil {
		return ToolAuthBegun{}, fmt.Errorf("extract state from agent auth URI: %w", err)
	}
	begunTime := s.now().UnixMilli()
	key := store.SystemUserToolAuthKey{Principal: target.principal, AgentName: target.agentName, CredentialKey: credentialKey}
	err = store.BeginSystemUserToolAuth(ctx, s.db, key, actor, begunTime, a2a.ToolAuthGrantRef(state))
	if err != nil {
		return ToolAuthBegun{}, err
	}
	s.audit(ctx, auditActionConnectBegun, target, credentialKey, actor)
	return ToolAuthBegun{
		AuthURI:       authData.AuthURI,
		CredentialKey: authData.CredentialKey,
		ExpiresAt:     ToTimestamp(time.UnixMilli(begunTime).Add(toolAuthStateTTL).UnixMilli()),
	}, nil
}

func (s *SystemUserToolAuthService) Disconnect(ctx context.Context, systemUserID, agentName, credentialKey, actor string) error {
	target, err := s.resolve(ctx, systemUserID, agentName)
	if err != nil {
		return err
	}
	credentialKey, err = requireCredentialKey(credentialKey)
	if err != nil {
		return err
	}
	if _, err := s.plane.callTargetToolAuth(ctx, s.controlTarget(target, credentialKey), "disconnect"); err != nil {
		return err
	}
	s.audit(ctx, auditActionDisconnected, target, credentialKey, actor)
	key := store.SystemUserToolAuthKey{Principal: target.principal, AgentName: target.agentName, CredentialKey: credentialKey}
	return store.DisconnectSystemUserToolAuth(ctx, s.db, key, actor, s.now().UnixMilli())
}

func (s *SystemUserToolAuthService) resolve(ctx context.Context, systemUserID, agentName string) (systemUserToolAuthTarget, error) {
	if s.rbac.usersRepo == nil {
		return systemUserToolAuthTarget{}, errUsersRepoNotConfigured
	}
	user, err := s.rbac.systemUserByID(ctx, systemUserID)
	if err != nil {
		return systemUserToolAuthTarget{}, err
	}
	if rbac.IsReservedSystemUserSub(user.Sub) {
		return systemUserToolAuthTarget{}, sayableConflict{&ConflictError{Message: fmt.Sprintf(
			"%s is a built-in service account, whose tool credentials are shared by everything that runs as it; connect tool credentials for a custom service account instead",
			user.Sub)}}
	}
	agentName = strings.TrimSpace(agentName)
	if !toolAuthTargetAgentPattern.MatchString(agentName) {
		return systemUserToolAuthTarget{}, &ValidationError{Field: "agentName",
			Message: "agentName must be 1 to 128 letters, digits, underscores, hyphens or dots"}
	}
	if !s.plane.toolAuthControlReady() {
		return systemUserToolAuthTarget{}, toolAuthUnavailable(nil)
	}
	canonical, routing, err := s.plane.toolAuthAgentNames(ctx, agentName)
	if err != nil {
		return systemUserToolAuthTarget{}, err
	}
	return systemUserToolAuthTarget{
		systemUserID: user.ID,
		principal:    user.Sub,
		agentName:    canonical,
		routingName:  routing,
	}, nil
}

func (s *SystemUserToolAuthService) controlTarget(target systemUserToolAuthTarget, credentialKey string) toolAuthTarget {
	return toolAuthTarget{
		agentName:     target.agentName,
		routingName:   target.routingName,
		credentialKey: credentialKey,
		principal:     target.principal,
	}
}

func (s *SystemUserToolAuthService) query(ctx context.Context, target systemUserToolAuthTarget) ([]agentToolAuthInfo, error) {
	raw, err := s.plane.callAgentInstanceToolAuth(ctx, target.routingName, "query", target.principal, "")
	if err != nil {
		return nil, s.plane.mapToolAuthControlError(err, target.agentName, "")
	}
	var items []agentToolAuthInfo
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("decode toolAuth query response: %w", err)
	}
	return items, nil
}

func (s *SystemUserToolAuthService) observeCompletion(ctx context.Context, target systemUserToolAuthTarget, item agentToolAuthInfo, record *store.SystemUserToolAuth) {
	if record.BegunRef == nil || item.Status != toolAuthStatusConnected || item.GrantRef != *record.BegunRef {
		return
	}
	observed := s.now().UnixMilli()
	key := store.SystemUserToolAuthKey{Principal: target.principal, AgentName: target.agentName, CredentialKey: item.CredentialKey}
	completed, err := store.CompleteSystemUserToolAuth(ctx, s.db, key, *record.BegunRef, observed)
	if err != nil {
		s.logger.Warn("failed to record service account tool auth completion",
			"systemUserId", target.systemUserID, "agentName", target.agentName, "credentialKey", item.CredentialKey, "error", err)
		return
	}
	if !completed {
		return
	}
	record.State = store.SystemUserToolAuthConnected
	record.AuthorizedBy = record.BegunBy
	record.AuthorizedTime = record.BegunTime
	record.BegunRef = nil
	record.ConnectedObservedTime = &observed
	actor := ""
	if record.BegunBy != nil {
		actor = *record.BegunBy
	}
	s.audit(ctx, auditActionConnectCompleted, target, item.CredentialKey, actor, "observedTime", observed)
}

func (s *SystemUserToolAuthService) audit(ctx context.Context, action string, target systemUserToolAuthTarget, credentialKey, actor string, extra ...any) {
	args := append([]any{"principal", target.principal, "agentName", target.agentName, "credentialKey", credentialKey}, extra...)
	logRBACAudit(withAuditCredential(ctx, s.logger), action, auditResourceSystemUserToolAuth, target.systemUserID, actor, args...)
}

const toolAuthStatusConnected = "connected"

func requireCredentialKey(credentialKey string) (string, error) {
	credentialKey = strings.TrimSpace(credentialKey)
	if credentialKey == "" {
		return "", &ValidationError{Field: "credentialKey", Message: "credentialKey is required"}
	}
	return credentialKey, nil
}

func heldCredential(items []agentToolAuthInfo, credentialKey string) (agentToolAuthInfo, error) {
	for _, item := range items {
		if item.CredentialKey == credentialKey {
			return item, nil
		}
	}
	return agentToolAuthInfo{}, &NotFoundError{Resource: "credential", ID: credentialKey}
}
