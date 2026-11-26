// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"errors"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

type AuthAdapter struct {
	svc Service
}

func NewAuthAdapter(svc Service) *AuthAdapter {
	return &AuthAdapter{svc: svc}
}

var _ auth.CredentialService = (*AuthAdapter)(nil)
var _ auth.Sealer = (*AuthAdapter)(nil)

var errNoSealer = errors.New("credential service has no identity key")

func (a *AuthAdapter) Seal(plaintext string) (string, error) {
	if s, ok := a.svc.(auth.Sealer); ok {
		return s.Seal(plaintext)
	}
	return "", errNoSealer
}

func (a *AuthAdapter) Open(sealed string) (string, error) {
	if s, ok := a.svc.(auth.Sealer); ok {
		return s.Open(sealed)
	}
	return "", errNoSealer
}

func (a *AuthAdapter) Get(ctx context.Context, agentName, appName, userID, key string) (string, error) {
	return a.svc.Get(ctx, Scope{AgentName: agentName, AppName: appName, UserID: userID}, key)
}

func (a *AuthAdapter) Set(ctx context.Context, agentName, appName, userID, key string, value string) error {
	return a.svc.Set(ctx, Scope{AgentName: agentName, AppName: appName, UserID: userID}, key, value, 0)
}

func (a *AuthAdapter) Delete(ctx context.Context, agentName, appName, userID, key string) error {
	return a.svc.Delete(ctx, Scope{AgentName: agentName, AppName: appName, UserID: userID}, key)
}

func (a *AuthAdapter) List(ctx context.Context, agentName, appName, userID string) ([]string, error) {
	return a.svc.List(ctx, Scope{AgentName: agentName, AppName: appName, UserID: userID})
}

func (a *AuthAdapter) SetWithTTL(ctx context.Context, agentName, appName, userID, key string, value string, ttl time.Duration) error {
	return a.svc.Set(ctx, Scope{AgentName: agentName, AppName: appName, UserID: userID}, key, value, ttl)
}

var _ auth.VersionedCredentialService = (*AuthAdapter)(nil)

type versionedService interface {
	GetVersioned(ctx context.Context, scope Scope, key string) (auth.VersionedCredential, error)
	SetAt(ctx context.Context, scope Scope, key, value string, ttl time.Duration, read auth.VersionedCredential) (auth.VersionedCredential, error)
	DeleteAt(ctx context.Context, scope Scope, key string, read auth.VersionedCredential) error
}

var _ versionedService = (*SessionStore)(nil)

func refScope(ref auth.CredentialRef) Scope {
	return Scope{AgentName: ref.AgentName, AppName: ref.AppName, UserID: ref.UserID}
}

func (a *AuthAdapter) GetVersioned(ctx context.Context, ref auth.CredentialRef) (auth.VersionedCredential, error) {
	scope := refScope(ref)
	if v, ok := a.svc.(versionedService); ok {
		return v.GetVersioned(ctx, scope, ref.Key)
	}
	value, err := a.svc.Get(ctx, scope, ref.Key)
	return auth.VersionedCredential{Value: value, Found: value != ""}, err
}

func (a *AuthAdapter) SetAt(ctx context.Context, ref auth.CredentialRef, value string, ttl time.Duration, read auth.VersionedCredential) (auth.VersionedCredential, error) {
	scope := refScope(ref)
	if v, ok := a.svc.(versionedService); ok {
		return v.SetAt(ctx, scope, ref.Key, value, ttl, read)
	}
	return auth.VersionedCredential{Value: value, Found: true}, a.svc.Set(ctx, scope, ref.Key, value, ttl)
}

func (a *AuthAdapter) DeleteAt(ctx context.Context, ref auth.CredentialRef, read auth.VersionedCredential) error {
	scope := refScope(ref)
	if v, ok := a.svc.(versionedService); ok {
		return v.DeleteAt(ctx, scope, ref.Key, read)
	}
	return a.svc.Delete(ctx, scope, ref.Key)
}
