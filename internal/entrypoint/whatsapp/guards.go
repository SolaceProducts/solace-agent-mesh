// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"fmt"
	"strings"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var claimedBindingNamespaces sync.Map

func claimBindingNamespace(gatewayID string, owner *Adapter) (collided bool) {
	if gatewayID == "" {
		return false
	}
	existing, loaded := claimedBindingNamespaces.LoadOrStore(gatewayID, owner)
	return loaded && existing != owner
}

func releaseBindingNamespace(gatewayID string, owner *Adapter) {
	if gatewayID == "" {
		return
	}
	claimedBindingNamespaces.CompareAndDelete(gatewayID, owner)
}

func checkTestOnlyConfig(cfg *WhatsAppConfig) error {
	if cfg.DisableRateLimiting && cfg.GraphAPIBaseURL == defaultGraphAPIBaseURL {
		return fmt.Errorf("whatsapp adapter: disable_rate_limiting is a test-only escape hatch and is refused against the production Graph API (%s) — it removes the per-recipient send spacing Meta enforces with error 131056; remove the key", defaultGraphAPIBaseURL)
	}
	return nil
}

func (a *Adapter) logStartupAdvisories(adapterCfg map[string]any) {
	if _, present := adapterCfg["max_media_size_mb"]; !present {
		a.logger.Info("whatsapp media size cap using the default", "maxMediaSizeMb", a.cfg.MaxMediaSizeMB)
	}

	a.warnIfRetiredAllowlistConfigured(adapterCfg)
	a.logIdentityPosture()
}

var RetiredAllowlistKeys = []string{"allowed_senders", "allowed_prefixes"}

func (a *Adapter) warnIfRetiredAllowlistConfigured(adapterCfg map[string]any) {
	var present []string
	for _, key := range RetiredAllowlistKeys {
		if _, ok := adapterCfg[key]; ok {
			present = append(present, key)
		}
	}
	if len(present) == 0 {
		return
	}
	a.logger.Warn("whatsapp entrypoint still configures the removed inbound sender allowlist, which no longer restricts anyone: every sender who can message this business number now reaches the entrypoint, so remove the keys and scope access with sender identity or run_as instead",
		"retiredKeys", strings.Join(present, ","))
}

func (a *Adapter) warnIfWithheldNumbersDeadEnd() {
	if a.cfg.RunAs != "" || a.cfg.IdentityLookupKey != IdentityLookupPhone {
		return
	}
	if a.cfg.ContactRequestTemplate == "" {
		a.logger.Warn("whatsapp senders whose phone number the provider withholds cannot be identified and will be turned away: set contact_request_template to the name of an approved template carrying a Request Contact Info button so they can share it in one tap",
			"identityLookupKey", a.cfg.IdentityLookupKey)
	}
}

func (a *Adapter) logIdentityPosture() {
	a.warnIfMatchingOnAUsername()
	a.warnIfIdentityGateUnwired()

	if a.cfg.RunAs == "" && a.cfg.IdentityClaimKey != "" {
		a.core.WarnIfSignInUnreachable(a.logger, "whatsapp")
	}
	a.warnIfWithheldNumbersDeadEnd()

	if a.cfg.RunAs == "" {
		if a.core.AuthzEnforced() {
			a.logger.Warn("whatsapp entrypoint does not restrict senders: anyone who can message this business number reaches authorization, and a sender who matches no grant is resolved with the deployment's default roles")
		} else {
			a.logger.Warn("whatsapp entrypoint accepts messages from any sender: authorization is not enforced, so anyone who can message this business number can start agent tasks on it")
		}
		return
	}

	principal := rbac.SystemUserPrincipal(a.cfg.RunAs)

	if principal == rbac.DefaultSystemUserSub {
		a.logger.Warn("whatsapp entrypoint runs as the built-in default service account, which can invoke any agent or workflow that declares no required_scopes; name a scoped service account to narrow what an unidentified sender can reach",
			"runAs", principal)
	}
}

func (a *Adapter) warnIfIdentityGateUnwired() {
	if a.cfg.RunAs != "" || a.cfg.IdentityClaimKey == "" || a.identityAuthorizer() != nil {
		return
	}
	const msg = "whatsapp entrypoint identifies senders by an IdP claim and has no identity authorizer wired, so every sender is refused"
	fields := []any{"identityLookupKey", a.cfg.IdentityLookupKey, "identityClaimKey", a.cfg.IdentityClaimKey}
	if a.core != nil && a.core.AuthzEnforced() {
		a.logger.Error(msg, fields...)
		return
	}
	a.logger.Warn(msg, fields...)
}

func (a *Adapter) warnIfMatchingOnAUsername() {
	if a.cfg.IdentityLookupKey != IdentityLookupWAUsername {
		return
	}
	a.logger.Warn("whatsapp entrypoint authorizes senders by WhatsApp username, which is user-chosen and transferable: the configured claim must be administrator-provisioned from a handle each user demonstrably holds, and revoked when they release it, or whoever reserves that handle next inherits their access",
		"identityClaimKey", a.cfg.IdentityClaimKey)
}
