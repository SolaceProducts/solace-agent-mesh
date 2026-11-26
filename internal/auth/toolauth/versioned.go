// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func getVersioned(ctx context.Context, svc auth.CredentialService, ref auth.CredentialRef) (auth.VersionedCredential, error) {
	if v, ok := svc.(auth.VersionedCredentialService); ok {
		return v.GetVersioned(ctx, ref)
	}
	value, err := svc.Get(ctx, ref.AgentName, ref.AppName, ref.UserID, ref.Key)
	return auth.VersionedCredential{Value: value, Found: value != ""}, err
}

func setAt(ctx context.Context, svc auth.CredentialService, ref auth.CredentialRef, value string, ttl time.Duration, read auth.VersionedCredential) (auth.VersionedCredential, error) {
	if v, ok := svc.(auth.VersionedCredentialService); ok {
		return v.SetAt(ctx, ref, value, ttl, read)
	}
	written := auth.VersionedCredential{Value: value, Found: true}
	if ttlSvc, ok := svc.(ttlCredentialService); ok && ttl > 0 {
		return written, ttlSvc.SetWithTTL(ctx, ref.AgentName, ref.AppName, ref.UserID, ref.Key, value, ttl)
	}
	return written, svc.Set(ctx, ref.AgentName, ref.AppName, ref.UserID, ref.Key, value)
}

func deleteAt(ctx context.Context, svc auth.CredentialService, ref auth.CredentialRef, read auth.VersionedCredential) error {
	if v, ok := svc.(auth.VersionedCredentialService); ok {
		return v.DeleteAt(ctx, ref, read)
	}
	return svc.Delete(ctx, ref.AgentName, ref.AppName, ref.UserID, ref.Key)
}

func (h *AuthHandler) tokenRef(toolCtx *tool.Context) auth.CredentialRef {
	return auth.CredentialRef{AgentName: toolCtx.AgentName, AppName: toolCtx.AgentName, UserID: toolCtx.UserID, Key: h.cfg.CredentialKey}
}

func (h *AuthHandler) persistedClientRef(toolCtx *tool.Context) auth.CredentialRef {
	return auth.CredentialRef{AgentName: toolCtx.AgentName, AppName: toolCtx.AgentName, UserID: sharedDCRClientUserID, Key: h.persistedClientKey()}
}

func clientIDOf(cred *AuthCredential) string {
	if cred == nil {
		return ""
	}
	return cred.ClientID
}
