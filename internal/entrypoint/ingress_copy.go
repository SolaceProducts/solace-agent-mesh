// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

const (
	accessHintSignIn = "If you haven't signed in to Solace Agent Mesh yet, sign in at %s and try again. Otherwise contact your administrator to request access."

	accessHintAdmin = "Contact your administrator to request access."
)

func unknownTargetText(typedName, accessHint string) string {
	return fmt.Sprintf("No agent named %q is available to you. %s", typedName, accessHint)
}

func defaultTargetDeniedText(accessHint string) string {
	return "You don't have access to the agent this conversation uses. " + accessHint
}

func unidentifiedText(webUIBaseURL string) string {
	if webUIBaseURL == "" {
		return "I couldn't verify who you are. Contact your administrator."
	}
	return fmt.Sprintf("I couldn't verify who you are. Sign in at %s and try again.", webUIBaseURL)
}

func AgentLabel(card a2a.AgentCard) string {
	return platpkg.DisplayName(&card)
}

func DeniedWithoutSignInText() string {
	return defaultTargetDeniedText(accessHintAdmin)
}

const SubmitFailedText = "Sorry, I couldn't submit your message. Please try again, or contact your administrator if the problem persists."

func (g *GatewayCore) SubmitFailureText(err error, signInLinkable bool) string {
	if IsAuthorizationDenial(err) {
		return defaultTargetDeniedText(g.accessHint(signInLinkable))
	}
	if text := BudgetRefusalText(err); text != "" {
		return text
	}
	if IsAgentNotAvailable(err) {
		return AgentNotAvailableText
	}
	return SubmitFailedText
}

const AgentNotAvailableText = "Sorry, that agent is not available. It is not deployed at the moment. Contact your administrator if you need it."

const BudgetUnconfirmedText = "Sorry, I can't take new requests right now: this deployment's usage budgets can't be checked at the moment. Please try again in a few minutes."

func BudgetRefusalText(err error) string {
	if refusal, ok := BudgetRefusal(err); ok {
		return BudgetExhaustedText(refusal)
	}
	if IsBudgetUnconfirmed(err) {
		return BudgetUnconfirmedText
	}
	return ""
}

func BudgetExhaustedText(refusal *budget.ExhaustedError) string {
	lead := "Sorry, I can't take new requests right now: "
	if refusal.Tier == budget.TierUser && refusal.SystemUser == "" {
		lead = "Sorry, I can't take new requests from you right now: "
	}
	return lead + budget.SpentText(refusal) + ". " + budget.ContactAdminText
}

func ScheduledRunSkippedText(refusal *budget.ExhaustedError) string {
	return "This run was skipped: " + budget.SpentText(refusal) + ". " + budget.ContactAdminText
}

func ScheduledRunRefusedText(refusal *budget.ExhaustedError, callerIsOwner bool) string {
	spent := budget.SpentText(refusal)
	if !callerIsOwner && refusal.Tier == budget.TierUser && refusal.SystemUser == "" {
		spent = budget.SpentTextFor(refusal, "this task's owner")
	}
	return "This task can't run now: " + spent + ". " + budget.ContactAdminText
}

func (g *GatewayCore) accessHint(signInLinkable bool) string {
	if !signInLinkable {
		return accessHintAdmin
	}
	base := g.webUIBaseURL()
	if base == "" {
		return accessHintAdmin
	}
	return fmt.Sprintf(accessHintSignIn, base)
}

func (g *GatewayCore) webUIBaseURL() string {
	if g == nil {
		return ""
	}
	if base := normalizeBaseURL(os.Getenv("WEBUI_FRONTEND_SERVER_URL")); base != "" {
		return base
	}
	if g.cfg == nil {
		return ""
	}
	return originOf(g.cfg.FrontendRedirectURL)
}

func normalizeBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return trimmed
}

func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

type SenderUnresolvedReason int

const (
	SenderNotLinked SenderUnresolvedReason = iota
	SenderNotUsable
	SenderNoValue
	SenderNoHandle
)

func UnresolvedSenderText(reason SenderUnresolvedReason, signInURL string) string {
	switch {
	case reason == SenderNoValue:
		return "This WhatsApp account does not share a phone number, so the account cannot be identified. Contact the administrator to be set up with a WhatsApp username instead."
	case reason == SenderNoHandle:
		return "This account does not have a WhatsApp username. Set a username in WhatsApp settings, or contact the administrator."
	case reason == SenderNotLinked && signInURL != "":
		return fmt.Sprintf("%s Sign in at %s and send the message again.", senderNotRecognized, signInURL)
	case reason == SenderNotLinked:
		return senderNoMatch + " If sign-in to Solace Agent Mesh is pending, signing in will link this account. Otherwise, contact the administrator to request access."
	default:
		return senderNoMatch + " " + senderAccessHintAdmin
	}
}

const (
	senderNotRecognized   = "This account is not recognized as a Solace Agent Mesh user."
	senderNoMatch         = "This account does not match a Solace Agent Mesh user."
	senderAccessHintAdmin = "Contact the administrator to request access."
)

const signInWrongAccountHint = "If sign-in is complete but access is still unavailable, the account used to sign in may not match the one the administrator registered. This can happen when one person has more than one account. Ask the administrator which account is registered for access, then sign in again with that account."

func UnresolvedSenderReminderPromptText(buttonLabel string) string {
	return fmt.Sprintf("Tap %s, then send the message again.\n\n%s", buttonLabel, signInWrongAccountHint)
}

func UnresolvedSenderReminderText(signInURL string) string {
	if signInURL == "" {
		return UnresolvedSenderText(SenderNotLinked, "")
	}
	return fmt.Sprintf("Sign in at %s, then send the message again.\n\n%s", signInURL, signInWrongAccountHint)
}

func UnresolvedSenderPromptText() string {
	return senderNotRecognized + " Sign in and send the message again."
}

func (g *GatewayCore) SignInURL() string { return g.webUIBaseURL() }

func (g *GatewayCore) WebUIURL() string { return g.webUIBaseURL() }

func (g *GatewayCore) WarnIfSignInUnreachable(logger *slog.Logger, entrypoint string) {
	if logger == nil || g.webUIBaseURL() != "" {
		return
	}
	logger.Warn("entrypoint offers signing in to unidentified senders but no web UI address is configured, so they are told to contact an administrator instead: set WEBUI_FRONTEND_SERVER_URL, or frontend_redirect_url on this gateway",
		"entrypoint", entrypoint)
}
