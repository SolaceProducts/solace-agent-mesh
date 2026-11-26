// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"net/url"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

const senderAuthzTimeout = 4 * time.Second

type senderAuthzOutcome struct {
	Identity   *auth.Subject
	Source     string
	Resolution identity.LookupResolution
	Retryable  bool

	NoLookupValue bool

	RefusalReason string
}

func (a *Adapter) authorizeSender(ctx context.Context, env *ingressEnvelope, profileName string) senderAuthzOutcome {
	sender := senderIdentity(env.Message.senderID(), profileName)

	if runAs, swapped := auth.ForWhatsAppSender(sender, a.cfg.RunAs); swapped {
		return senderAuthzOutcome{Identity: runAs, Source: principalSourceRunAs, Resolution: identity.LookupUnknown}
	}

	authorizer := a.identityAuthorizer()
	if authorizer == nil {
		if a.cfg.IdentityClaimKey != "" {
			a.logIdentityGateUnavailable(env, "no identity authorizer is wired on this gateway")
		}
		return senderAuthzOutcome{Identity: sender, Source: principalSourceSender, Resolution: identity.LookupUnknown}
	}
	if a.cfg.IdentityClaimKey == "" {
		return senderAuthzOutcome{Identity: sender, Source: principalSourceSender, Resolution: identity.LookupUnknown}
	}

	if env.Identity.LookupValue == "" && env.Identity.ContinuityToken == "" {
		recovered := a.resolveIngressIdentity(&env.Context, &env.Message)
		recovered.LookupValueExact = false
		if recovered.LookupValue != "" || recovered.ContinuityToken != "" {
			a.logger.Info("recomputed the sender identity for a message spooled before the upgrade",
				"wamid", env.Message.ID, "senderFP", strutil.PIIFingerprint(env.Message.senderID()))
			env.Identity = recovered
		}
	}

	if env.Identity.LookupValue == "" {
		outcome := senderAuthzOutcome{Resolution: identity.LookupNoMatch, NoLookupValue: true}
		a.logger.Debug("whatsapp sender carried no value to identify them by",
			append(a.identityLookupFields(outcome, env), "wamid", env.Message.ID,
				"senderFP", strutil.PIIFingerprint(env.Message.senderID()))...)
		return outcome
	}

	a.logIfNoContinuityToken(env)

	lookupCtx, cancel := context.WithTimeout(ctx, senderAuthzTimeout)
	defer cancel()
	res, err := authorizer.AuthorizeSender(lookupCtx, auth.SenderAuthorizationRequest{
		UserID:          sender.UserID(),
		ClaimKey:        a.cfg.IdentityClaimKey,
		ClaimValue:      env.Identity.LookupValue,
		ContinuityToken: env.Identity.ContinuityToken,
	})
	if err != nil {
		a.logger.Warn("whatsapp sender authorization could not be decided; holding the message for redelivery",
			"wamid", env.Message.ID, "identityLookupKey", a.cfg.IdentityLookupKey,
			"identityClaimKey", a.cfg.IdentityClaimKey,
			"senderFP", strutil.PIIFingerprint(env.Message.senderID()),
			"continuityTokenFP", strutil.PIIFingerprint(env.Identity.ContinuityToken), "error", err)
		return senderAuthzOutcome{Resolution: identity.LookupDegraded, Retryable: true}
	}

	switch res.Resolution {
	case identity.LookupNotConfigured:
		a.logIdentityGateUnavailable(env, "the platform has no per-user identity store wired to answer the lookup")
		return senderAuthzOutcome{Resolution: res.Resolution}

	case identity.LookupMatched:
		principal, source := a.matchedPrincipal(env, res, sender.UserID())
		resolved := auth.ForAuthorizedWhatsAppSender(sender, principal, res)
		a.retireSuppressionOnResolve(ctx, env.Message.senderID())
		return senderAuthzOutcome{Identity: resolved, Source: source, Resolution: res.Resolution}

	case identity.LookupDegraded:
		return senderAuthzOutcome{Resolution: res.Resolution, Retryable: true}

	case identity.LookupNoMatch, identity.LookupAmbiguous, identity.LookupRefused:
		return senderAuthzOutcome{Resolution: res.Resolution, RefusalReason: res.RefusalReason}

	case identity.LookupUnknown:
		return senderAuthzOutcome{Identity: sender, Source: principalSourceSender, Resolution: identity.LookupUnknown}

	default:
		a.logger.Warn("whatsapp sender authorization returned an unrecognized resolution; holding the message rather than admitting the sender",
			"wamid", env.Message.ID, "resolutionLen", len(res.Resolution),
			"senderFP", strutil.PIIFingerprint(env.Message.senderID()))
		return senderAuthzOutcome{Resolution: identity.LookupDegraded, Retryable: true}
	}
}

func (a *Adapter) logIdentityGateUnavailable(env *ingressEnvelope, cause string) {
	if warn, _, _ := a.identityGateNotices.shouldWarn(cause, unidentifiedNoticeWindow); !warn {
		return
	}
	const msg = "whatsapp sender identity gate is unavailable; senders cannot be identified until it is restored"
	fields := append(a.identityLookupFields(senderAuthzOutcome{Resolution: identity.LookupNotConfigured}, env),
		"wamid", env.Message.ID, "cause", cause)
	if a.core != nil && a.core.AuthzEnforced() {
		a.logger.Error(msg, fields...)
		return
	}
	a.logger.Warn(msg, fields...)
}

func (a *Adapter) logIfNoContinuityToken(env *ingressEnvelope) {
	if env.Identity.ContinuityToken != "" || a.noContinuityTokenWarned.Swap(true) {
		return
	}
	a.logger.Warn("whatsapp inbound messages carry no business-scoped sender id, so a value that changes hands is caught only by the claim match and the corroborated holder; check the webhook version and any provider in front of this entrypoint",
		"identityLookupKey", a.cfg.IdentityLookupKey, "gatewayID", a.gatewayID())
}

func (a *Adapter) identityAuthorizer() auth.SenderAuthorizer {
	if a.core == nil {
		return nil
	}
	return a.core.IdentityAuthorizer()
}

const unidentifiedNoticeWindow = time.Hour

func (a *Adapter) refuseUnidentifiedSender(ctx context.Context, env *ingressEnvelope, authz senderAuthzOutcome) inboundOutcome {
	msg := &env.Message

	if authz.NoLookupValue {
		if a.cfg.IdentityLookupKey != IdentityLookupPhone {
			return a.declineSender(env, refusalSenderNoValue,
				entrypoint.UnresolvedSenderText(entrypoint.SenderNoHandle, ""),
				logNoSenderHandle, authz)
		}
		return a.offerContactShare(ctx, env, authz)
	}

	if authz.Resolution == identity.LookupNotConfigured {
		return a.declineSender(env, refusalIdentityGateUnavailable,
			entrypoint.UnresolvedSenderText(entrypoint.SenderNotUsable, ""),
			logIdentityGateUnavailable, authz)
	}

	signInURL := a.signInURL()

	if authz.Resolution != identity.LookupNoMatch {
		return a.declineSender(env, refusalSenderNotUsable,
			entrypoint.UnresolvedSenderText(entrypoint.SenderNotUsable, ""),
			refusalLogMessage(authz.Resolution), authz)
	}

	if signInURL == "" {
		return a.declineSender(env, refusalSenderUnidentified,
			entrypoint.UnresolvedSenderText(entrypoint.SenderNotLinked, ""),
			refusalLogMessage(authz.Resolution), authz)
	}
	followUp := a.signInAlreadyOffered(ctx, env)
	body, fallback := entrypoint.UnresolvedSenderPromptText(), entrypoint.UnresolvedSenderText(entrypoint.SenderNotLinked, signInURL)
	if followUp {
		body, fallback = entrypoint.UnresolvedSenderReminderPromptText(loginButtonLabel), entrypoint.UnresolvedSenderReminderText(signInURL)
	}
	queued := !a.loginButtonRefused.active(msg.senderID(), store.DefaultPendingAuthTTL) &&
		a.sendLoginPrompt(env, signInURL, body, fallback)
	if !queued {
		if followUp {
			return a.declineSender(env, refusalSenderUnidentified, fallback, logAwaitingSignIn, authz)
		}
		a.loginPrompts.forget(msg.senderID())
		return a.declineSender(env, refusalSenderUnidentified, fallback,
			refusalLogMessage(authz.Resolution), authz)
	}
	a.auditRefused(msg, refusalSenderUnidentified)
	env.ackReason = refusalSenderUnidentified
	if followUp {
		a.logRefusal(msg, &a.unidentifiedSenders, unidentifiedNoticeWindow, refusalSenderUnidentified,
			logAwaitingSignIn, a.identityLookupFields(authz)...)
		return outcomeDeclined
	}
	a.logger.Info("sign-in prompt sent to an unidentified sender",
		append(a.identityLookupFields(authz, env), "wamid", msg.ID,
			"senderFP", strutil.PIIFingerprint(msg.senderID()))...)
	return outcomeDeclined
}

func (a *Adapter) signInAlreadyOffered(ctx context.Context, env *ingressEnvelope) bool {
	if a.identityPromptSuppressed(ctx, env) {
		return true
	}
	firstOffer, _, _ := a.loginPrompts.shouldWarn(env.Message.senderID(), unidentifiedNoticeWindow)
	return !firstOffer
}

func refusalLogMessage(resolution identity.LookupResolution) string {
	switch resolution {
	case identity.LookupNoMatch:
		return "whatsapp inbound message refused: no SAM user holds this sender's identity claim, so they were asked to sign in"
	case identity.LookupAmbiguous:
		return "whatsapp inbound message refused: this sender's identity claim is held by more than one SAM user, which locks all of them out until the duplicate is resolved"
	case identity.LookupRefused:
		return "whatsapp inbound message refused: the platform declined this identity lookup; its own log says whether the claim key is inadmissible, the matched user inactive, or the sender changed"
	case identity.LookupNotConfigured:
		return logIdentityGateUnavailable
	default:
		return "whatsapp inbound message refused: sender could not be identified"
	}
}

func (a *Adapter) identityLookupFields(authz senderAuthzOutcome, env ...*ingressEnvelope) []any {
	resolution := string(authz.Resolution)
	if resolution == "" {
		resolution = "unknown"
	}
	fields := []any{
		"resolution", resolution,
		"identityLookupKey", a.cfg.IdentityLookupKey,
		"identityClaimKey", a.cfg.IdentityClaimKey,
	}
	if authz.RefusalReason != "" {
		fields = append(fields, "refusalReason", authz.RefusalReason)
	}
	if len(env) > 0 && env[0] != nil && env[0].Identity.LookupValue != "" {
		fields = append(fields, "lookupValueExact", env[0].Identity.LookupValueExact)
	}
	return fields
}

func (a *Adapter) declineSender(env *ingressEnvelope, reason, notice, logMsg string, authz senderAuthzOutcome) inboundOutcome {
	a.refuseAndAlwaysReply(&env.Message, &a.unidentifiedSenders, unidentifiedNoticeWindow, reason, notice, logMsg,
		a.identityLookupFields(authz)...)
	env.ackReason = reason
	return outcomeDeclined
}

const (
	logAwaitingSignIn              = "whatsapp inbound message refused: sender has been sent a sign-in link and has not signed in yet"
	logAwaitingContactShare        = "whatsapp inbound message refused: sender has been sent a contact-share request and has not shared their number yet"
	logContactShareNotYetDisclosed = "whatsapp inbound message acknowledged: sender shared their number but the provider has not disclosed it yet, so their next message will resolve"
	logNoContactRequestTemplate    = "whatsapp inbound message refused: the provider withheld this sender's phone number and no contact_request_template is configured, so they could not be offered a way to share it"
	logContactRequestUnqueued      = "whatsapp inbound message refused: the provider withheld this sender's phone number and the contact-share request could not be queued"
	logNoSenderHandle              = "whatsapp inbound message refused: this sender has not adopted a WhatsApp username and the entrypoint identifies senders by one, so there was nothing to look them up by"
	logIdentityGateUnavailable     = "whatsapp inbound message refused: this entrypoint identifies senders and the identity lookup is unavailable, so no sender can be authorized"
)

const (
	signInSourceParam = "signInFrom"
	signInSource      = "whatsapp"
)

func (a *Adapter) signInURL() string {
	if a.core == nil {
		return ""
	}
	base := a.core.SignInURL()
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	if u.Path == "" {
		u.Path = "/"
	}
	source := url.Values{signInSourceParam: {signInSource}}.Encode()
	if u.RawQuery != "" {
		source = u.RawQuery + "&" + source
	}
	u.RawQuery = source
	return u.String()
}

func (a *Adapter) matchedPrincipal(env *ingressEnvelope, res auth.SenderAuthorization, senderPrincipal string) (principal, source string) {
	switch {
	case res.ResolvedPrincipal == "":
		return senderPrincipal, principalSourceSender
	case rbac.IsSystemPrincipal(res.ResolvedPrincipal):
		a.logger.Warn("whatsapp sender resolved to a reserved system principal; submitting as the sender instead",
			"wamid", env.Message.ID, "senderFP", strutil.PIIFingerprint(env.Message.senderID()),
			"resolvedUserId", res.ResolvedUserID)
		return senderPrincipal, principalSourceSender
	default:
		return res.ResolvedPrincipal, principalSourceMatchedEmail
	}
}

func resolvedUserID(id *auth.Subject) string {
	if id == nil {
		return ""
	}
	v, _ := id.Metadata()[auth.MetadataPlatformUserID].(string)
	return v
}

const loginButtonLabel = "Sign In"

const (
	signInNoticeAddressNotHTTPS   = "address_not_https"
	signInNoticePendingUnreadable = "pending_record_unreadable"
)

func (a *Adapter) sendLoginPrompt(env *ingressEnvelope, signInURL, body, fallback string) bool {
	u, err := url.Parse(signInURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		if warn, suppressed, _ := a.signInNotices.shouldWarn(signInNoticeAddressNotHTTPS, unidentifiedNoticeWindow); warn {
			a.logger.Warn("sign-in button disabled: the web UI address is not https, so unidentified senders get the link as text; set WEBUI_FRONTEND_SERVER_URL, or frontend_redirect_url on this gateway, to an https address",
				"suppressedSinceLast", suppressed)
		}
		return false
	}
	outboundID := a.enqueue(kindInteractivePrompt, outboundTarget{recipient: env.Message.senderID()}, outboundPayload{
		Text: body,
		Prompt: &promptPayload{
			Kind:          promptKindLogin,
			Mode:          promptModeCTAURL,
			CTA:           &ctaPayload{DisplayText: loginButtonLabel, URL: signInURL},
			LookupValueFP: strutil.PIIFingerprint(env.Identity.LookupValue),
			FallbackText:  fallback,
		},
	}, "")
	if outboundID == "" {
		a.logger.Error("failed to queue the sign-in prompt", "wamid", env.Message.ID)
		return false
	}
	return true
}

func (a *Adapter) identityPromptSuppressed(ctx context.Context, env *ingressEnvelope) bool {
	if a.pendingAuth == nil {
		return false
	}
	row, err := a.pendingAuth.Live(ctx, a.gatewayID(), env.Message.senderID(), a.nowMs())
	if err != nil {
		if warn, suppressed, _ := a.signInNotices.shouldWarn(signInNoticePendingUnreadable, unidentifiedNoticeWindow); warn {
			a.logger.Warn("could not read the pending sign-in record; treating the sender as not yet asked",
				"senderFP", strutil.PIIFingerprint(env.Message.senderID()), "error", err,
				"suppressedSinceLast", suppressed)
		}
		return false
	}
	if row == nil {
		return false
	}
	return row.LookupValueFP == strutil.PIIFingerprint(env.Identity.LookupValue)
}

func (a *Adapter) recordIdentityPrompt(ctx context.Context, q dbutil.Querier, row *store.WhatsAppOutbound, p *promptPayload, wamid string) error {
	if a.pendingAuth == nil {
		return nil
	}
	nowMs := a.nowMs()
	return a.pendingAuth.Upsert(ctx, q, &store.WhatsAppPendingAuth{
		GatewayID:     a.gatewayID(),
		SenderID:      row.Recipient,
		Kind:          pendingAuthKind(p.Kind),
		LookupKey:     a.cfg.IdentityLookupKey,
		LookupValueFP: p.LookupValueFP,
		OutboundID:    row.ID,
		PromptWamid:   wamid,
		ExpiresAt:     nowMs + store.DefaultPendingAuthTTL.Milliseconds(),
		CreatedAt:     nowMs,
		UpdatedAt:     nowMs,
	})
}

func pendingAuthKind(promptKind string) string {
	if promptKind == promptKindContactRequest {
		return store.PendingAuthKindConsent
	}
	return store.PendingAuthKindLogin
}

func (a *Adapter) offerContactShare(ctx context.Context, env *ingressEnvelope, authz senderAuthzOutcome) inboundOutcome {
	msg := &env.Message
	deadEnd := func(logMsg string) inboundOutcome {
		return a.declineSender(env, refusalSenderNoValue,
			entrypoint.UnresolvedSenderText(entrypoint.SenderNoValue, ""), logMsg, authz)
	}
	if sharedTheirOwnNumber(msg) {
		return a.declineSender(env, refusalSenderNoValue, msgContactShareReceived,
			logContactShareNotYetDisclosed, authz)
	}
	if a.cfg.ContactRequestTemplate == "" {
		return deadEnd(logNoContactRequestTemplate)
	}
	if a.identityPromptSuppressed(ctx, env) {
		return a.declineSender(env, refusalSenderNoValue, msgContactShareWaiting, logAwaitingContactShare, authz)
	}
	if warn, _, _ := a.contactRequests.shouldWarn(msg.senderID(), unidentifiedNoticeWindow); !warn {
		return a.declineSender(env, refusalSenderNoValue, msgContactShareWaiting, logAwaitingContactShare, authz)
	}
	if !a.sendContactRequest(env) {
		return deadEnd(logContactRequestUnqueued)
	}
	a.auditRefused(msg, refusalSenderNoValue)
	env.ackReason = refusalSenderNoValue
	a.logger.Info("contact-share request sent to a sender whose number the provider withheld",
		append(a.identityLookupFields(authz, env), "wamid", msg.ID,
			"senderFP", strutil.PIIFingerprint(msg.senderID()),
			"template", a.cfg.ContactRequestTemplate)...)
	return outcomeDeclined
}

func (a *Adapter) sendContactRequest(env *ingressEnvelope) bool {
	outboundID := a.enqueue(kindInteractivePrompt, outboundTarget{recipient: env.Message.senderID()}, outboundPayload{
		Prompt: &promptPayload{
			Kind: promptKindContactRequest,
			Mode: promptModeTemplate,
			Template: &templateRef{
				Name:     a.cfg.ContactRequestTemplate,
				Language: a.cfg.ContactRequestTemplateLanguage,
			},
			LookupValueFP: strutil.PIIFingerprint(env.Identity.LookupValue),
		},
	}, "")
	if outboundID == "" {
		a.logger.Error("failed to queue the contact-share request", "wamid", env.Message.ID)
		return false
	}
	return true
}

func (a *Adapter) retireSuppressionOnResolve(ctx context.Context, senderID string) {
	if a.pendingAuth == nil || senderID == "" {
		return
	}
	if clear, _, _ := a.resolvedSenders.shouldWarn(senderID, unidentifiedNoticeWindow); !clear {
		return
	}
	a.clearPendingLoginPrompt(ctx, senderID)
}

func (a *Adapter) clearPendingLoginPrompt(ctx context.Context, senderID string) {
	if a.pendingAuth == nil {
		return
	}
	a.loginPrompts.forget(senderID)
	a.loginButtonRefused.forget(senderID)
	if err := a.pendingAuth.Clear(ctx, nil, a.gatewayID(), senderID); err != nil {
		a.logger.Warn("could not clear the pending sign-in record",
			"senderFP", strutil.PIIFingerprint(senderID), "error", err)
	}
}

func (a *Adapter) handleSystemNotification(ctx context.Context, env *ingressEnvelope) {
	kind, recognized := systemRotation(env.Message.System, &env.Message)
	if !recognized {
		a.logger.Warn("unrecognized whatsapp system notification; no rotation applied",
			"wamid", env.Message.ID, "systemType", env.Message.System.Type)
		return
	}
	if kind == rotationNone {
		return
	}
	authorizer := a.identityAuthorizer()
	if authorizer == nil || a.cfg.IdentityClaimKey == "" {
		return
	}
	if a.cfg.IdentityLookupKey != IdentityLookupPhone {
		a.logger.Warn("whatsapp identity rotation names a phone number and this entrypoint matches on a handle; no rotation applied",
			"wamid", env.Message.ID, "identityLookupKey", a.cfg.IdentityLookupKey)
		return
	}

	notification := env.Message.System
	staleValue := notification.staleSenderID(&env.Message)
	if staleValue == "" {
		a.logger.Warn("whatsapp identity rotation named no prior identifier; no rotation applied",
			"wamid", env.Message.ID, "systemType", notification.Type)
		return
	}

	req := auth.SenderAuthorizationRequest{
		UserID:     senderIdentity(env.Message.senderID(), "").UserID(),
		ClaimKey:   a.cfg.IdentityClaimKey,
		ClaimValue: staleValue,
	}
	switch kind {
	case rotationIdentifier:
		req.ContinuityToken = notification.newContinuityToken()
		req.PredecessorToken = notification.stalePredecessorToken(&env.Message)
		if req.ContinuityToken == "" || req.PredecessorToken == "" {
			a.logger.Warn("whatsapp identity rotation named no business-scoped id to swap; binding left as it was",
				"wamid", env.Message.ID, "systemType", notification.Type)
			return
		}
	case rotationValue:
		req.DiscardBinding = true
	}

	lookupCtx, cancel := context.WithTimeout(ctx, senderAuthzTimeout)
	defer cancel()
	res, err := authorizer.AuthorizeSender(lookupCtx, req)
	if err != nil {
		a.logger.Warn("could not apply a whatsapp identifier rotation",
			"wamid", env.Message.ID, "senderFP", strutil.PIIFingerprint(env.Message.senderID()), "error", err)
		return
	}
	if !res.RotationApplied {
		a.logger.Warn("whatsapp identifier rotation was not applied; the stored binding is unchanged",
			"wamid", env.Message.ID, "systemType", notification.Type, "rebind", kind == rotationIdentifier,
			"resolution", string(res.Resolution))
		return
	}
	a.clearPendingLoginPrompt(ctx, env.Message.senderID())
	a.logger.Info("applied a whatsapp identifier rotation", "wamid", env.Message.ID, "rebind", kind == rotationIdentifier)
}

func (a *Adapter) sweepExpiredPendingAuth() {
	if a.pendingAuth == nil {
		return
	}
	ctx, cancel := a.settleCtx()
	defer cancel()
	removed, err := a.pendingAuth.DeleteExpired(ctx, a.nowMs())
	if err != nil {
		a.logger.Warn("failed to reap expired sign-in records", "error", err)
		return
	}
	if removed > 0 {
		a.logger.Debug("reaped expired sign-in records", "count", removed)
	}
}
