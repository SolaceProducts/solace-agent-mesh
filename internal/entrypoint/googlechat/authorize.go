// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

type authzVerdict struct {
	Identity   *auth.Subject
	Retryable  bool
	Refusal    entrypoint.SenderUnresolvedReason
	Resolution identity.LookupResolution
	Cause      string
}

const (
	causeNoEmail          = "no_email_in_event"
	causeDomainNotAllowed = "email_domain_not_allowed"
	causeNoAuthorizer     = "no_identity_authorizer"
	causeUnresolved       = "resolution_unresolved"
	causePlatform         = "platform_refused"
)

func (a *Adapter) authorizeSender(ctx context.Context, ev *inboundEvent) authzVerdict {
	if ev.SenderEmail == "" {
		return authzVerdict{Refusal: entrypoint.SenderNotUsable, Cause: causeNoEmail}
	}
	if !a.cfg.emailDomainAllowed(ev.SenderEmail) {
		return authzVerdict{Refusal: entrypoint.SenderNotUsable, Cause: causeDomainNotAllowed}
	}
	authorizer := a.identityAuthorizer()
	if authorizer == nil {
		a.logIdentityGateUnavailable()
		return authzVerdict{Refusal: entrypoint.SenderNotUsable, Cause: causeNoAuthorizer}
	}

	lookupCtx, cancel := context.WithTimeout(ctx, senderAuthzTimeout)
	defer cancel()
	res, err := authorizer.AuthorizeSender(lookupCtx, auth.SenderAuthorizationRequest{
		UserID:     ev.SenderEmail,
		ClaimKey:   a.cfg.IdentityClaimKey,
		ClaimValue: ev.SenderEmail,
	})
	if err != nil {
		a.logger.Warn("google chat sender authorization could not be decided; holding the message for redelivery",
			"senderFP", strutil.PIIFingerprint(ev.SenderName), "identityClaimKey", a.cfg.IdentityClaimKey, "error", err)
		return authzVerdict{Retryable: true, Resolution: identity.LookupDegraded}
	}

	switch res.Resolution {
	case identity.LookupMatched:
		return authzVerdict{Identity: auth.ForGoogleChatSender(ev.SenderEmail, ev.SenderDisplayName, ev.SenderName, res), Resolution: res.Resolution}
	case identity.LookupDegraded:
		return authzVerdict{Retryable: true, Resolution: res.Resolution}
	case identity.LookupNoMatch:
		return authzVerdict{Refusal: entrypoint.SenderNotLinked, Resolution: res.Resolution, Cause: causePlatform}
	case identity.LookupAmbiguous, identity.LookupRefused, identity.LookupNotConfigured:
		return authzVerdict{Refusal: entrypoint.SenderNotUsable, Resolution: res.Resolution, Cause: causePlatform}
	case identity.LookupUnknown:
		a.logIdentityGateUnresolved()
		return authzVerdict{Refusal: entrypoint.SenderNotUsable, Resolution: res.Resolution, Cause: causeUnresolved}
	default:
		a.logger.Warn("google chat sender authorization returned an unrecognized resolution; holding the message rather than admitting the sender",
			"senderFP", strutil.PIIFingerprint(ev.SenderName), "resolutionLen", len(res.Resolution))
		return authzVerdict{Retryable: true, Resolution: identity.LookupDegraded}
	}
}

func (a *Adapter) identityAuthorizer() auth.SenderAuthorizer {
	if a.core == nil {
		return nil
	}
	return a.core.IdentityAuthorizer()
}

func (a *Adapter) logIdentityGateUnavailable() {
	if !a.gateNotices.allow("no_authorizer", identityGateNoticeWindow) {
		return
	}
	a.logger.Error("google chat entrypoint has no identity authorizer wired; every sender is refused until the platform identity service is reachable from this gateway",
		"gatewayID", a.gatewayID)
}

func (a *Adapter) logIdentityGateUnresolved() {
	if !a.gateNotices.allow("unresolved", identityGateNoticeWindow) {
		return
	}
	a.logger.Error("google chat sender lookup returned no resolution; refusing senders because this entrypoint fails closed — the identity service responding to this gateway does not implement claim matching",
		"gatewayID", a.gatewayID, "identityClaimKey", a.cfg.IdentityClaimKey)
}
