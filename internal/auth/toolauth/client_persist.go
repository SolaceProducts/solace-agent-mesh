// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	dcrClientKeySuffix    = ".dcrclient"
	sharedDCRClientUserID = "system:dcr-client"
	dcrClientPersistTTL   = 10 * 365 * 24 * time.Hour
)

type ttlCredentialService interface {
	SetWithTTL(ctx context.Context, agentName, appName, userID, key, value string, ttl time.Duration) error
}

type persistedDCRClient struct {
	ClientID                string `json:"client_id"`
	ClientSecret            string `json:"client_secret,omitempty"`
	TokenEndpointAuthMethod string `json:"token_endpoint_auth_method,omitempty"`
	ClientSecretExpiresAt   int64  `json:"client_secret_expires_at,omitempty"`
	ClientIDIssuedAt        int64  `json:"client_id_issued_at,omitempty"`
	Issuer                  string `json:"issuer,omitempty"`
	RegistrationEndpoint    string `json:"registration_endpoint,omitempty"`
}

func (h *AuthHandler) persistedClientKey() string {
	return h.cfg.CredentialKey + dcrClientKeySuffix
}

func (h *AuthHandler) readPersistedClient(ctx context.Context, toolCtx *tool.Context) (auth.VersionedCredential, error) {
	return getVersioned(ctx, toolCtx.CredentialService, h.persistedClientRef(toolCtx))
}

func (h *AuthHandler) loadPersistedClient(ctx context.Context, toolCtx *tool.Context, meta *OAuthMetadata) (*ClientRegistration, auth.VersionedCredential) {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return nil, auth.VersionedCredential{}
	}
	stored, err := h.readPersistedClient(ctx, toolCtx)
	if err != nil {
		h.logger.Warn("failed to load persisted dynamic client registration; will register fresh",
			"credentialKey", h.cfg.CredentialKey, "error", err)
		return nil, stored
	}
	if stored.Value == "" {
		return nil, stored
	}
	var pc persistedDCRClient
	if err := json.Unmarshal([]byte(stored.Value), &pc); err != nil || pc.ClientID == "" {
		h.logger.Warn("discarding unusable persisted dynamic client registration; will register fresh",
			"credentialKey", h.cfg.CredentialKey, "reason", "corrupt_or_empty")
		return nil, stored
	}
	if meta != nil && (pc.Issuer != meta.Issuer || pc.RegistrationEndpoint != meta.RegistrationEndpoint) {
		h.logger.Warn("discarding persisted dynamic client registration from a different auth server; will register fresh",
			"credentialKey", h.cfg.CredentialKey, "reason", "issuer_or_endpoint_changed")
		return nil, stored
	}
	reg := &ClientRegistration{
		ClientID:                pc.ClientID,
		ClientSecret:            pc.ClientSecret,
		TokenEndpointAuthMethod: pc.TokenEndpointAuthMethod,
		ClientSecretExpiresAt:   pc.ClientSecretExpiresAt,
		ClientIDIssuedAt:        pc.ClientIDIssuedAt,
	}
	if reg.IsExpired() {
		h.logger.Warn("discarding persisted dynamic client registration with expired client_secret; will register fresh",
			"credentialKey", h.cfg.CredentialKey, "reason", "client_secret_expired")
		return nil, stored
	}
	return reg, stored
}

func (h *AuthHandler) storePersistedClient(ctx context.Context, toolCtx *tool.Context, reg *ClientRegistration, meta *OAuthMetadata, read auth.VersionedCredential) error {
	if toolCtx == nil || toolCtx.CredentialService == nil || reg == nil || reg.FromEnv || reg.ClientID == "" {
		return nil
	}
	pc := persistedDCRClient{
		ClientID:                reg.ClientID,
		ClientSecret:            reg.ClientSecret,
		TokenEndpointAuthMethod: reg.TokenEndpointAuthMethod,
		ClientSecretExpiresAt:   reg.ClientSecretExpiresAt,
		ClientIDIssuedAt:        reg.ClientIDIssuedAt,
	}
	if meta != nil {
		pc.Issuer = meta.Issuer
		pc.RegistrationEndpoint = meta.RegistrationEndpoint
	}
	data, err := json.Marshal(pc)
	if err != nil {
		return nil
	}
	_, err = setAt(context.WithoutCancel(ctx), toolCtx.CredentialService, h.persistedClientRef(toolCtx), string(data), dcrClientPersistTTL, read)
	if err != nil && !errors.Is(err, auth.ErrCredentialChanged) {
		h.logger.Warn("failed to persist dynamic client registration; a future restart will re-register and force users to re-authenticate",
			"credentialKey", h.cfg.CredentialKey, "error", err)
	}
	return err
}

func (h *AuthHandler) deletePersistedClient(ctx context.Context, toolCtx *tool.Context, rejectedClientID string) (anotherClientStored bool) {
	if toolCtx == nil || toolCtx.CredentialService == nil {
		return false
	}
	ctx = context.WithoutCancel(ctx)
	stored, err := h.readPersistedClient(ctx, toolCtx)
	if err != nil {
		h.warnRevokedClientKept(err)
		return false
	}
	if stored.Value == "" {
		return false
	}
	var pc persistedDCRClient
	if json.Unmarshal([]byte(stored.Value), &pc) == nil && pc.ClientID != "" && pc.ClientID != rejectedClientID {
		return true
	}
	err = deleteAt(ctx, toolCtx.CredentialService, h.persistedClientRef(toolCtx), stored)
	if err != nil && !errors.Is(err, auth.ErrCredentialChanged) {
		h.warnRevokedClientKept(err)
	}
	return false
}

func (h *AuthHandler) warnRevokedClientKept(err error) {
	h.logger.Warn("failed to delete persisted dynamic client registration; a revoked client_id may be replayed until it is cleared",
		"credentialKey", h.cfg.CredentialKey, "error", err)
}

func (h *AuthHandler) storeRegisteredClient(ctx context.Context, toolCtx *tool.Context, reg *ClientRegistration, meta *OAuthMetadata, read auth.VersionedCredential) (*ClientRegistration, error) {
	for attempt := 1; ; attempt++ {
		err := h.storePersistedClient(ctx, toolCtx, reg, meta, read)
		if !errors.Is(err, auth.ErrCredentialChanged) {
			return reg, nil
		}
		var winner *ClientRegistration
		winner, read = h.loadPersistedClient(ctx, toolCtx, meta)
		if winner != nil {
			return winner, nil
		}
		if attempt == maxConditionalWriteAttempts {
			return nil, errors.New("stored dynamic client registration kept changing")
		}
	}
}
