// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type IngressTargetRequest struct {
	Caller *auth.Subject

	Text string

	DefaultAgentName string

	SignInLinkable bool
}

type IngressTarget struct {
	Name string

	Misconfigured bool

	Refusal string

	RefusalReason string
}

func (t IngressTarget) Refused() bool { return t.Refusal != "" }

func (g *GatewayCore) ResolveIngressTarget(ctx context.Context, req IngressTargetRequest) IngressTarget {
	if g == nil {
		return IngressTarget{}
	}
	reg := g.AgentRegistry()
	if reg == nil {
		return IngressTarget{}
	}

	if strings.Contains(req.Text, "@") {
		usable := g.usableCards(ctx, req.Caller, reg.List())
		if name := MatchMentionedAgent(usable, req.Text); name != "" {
			return IngressTarget{Name: name}
		}
		if token, ok := leadingMention(req.Text); ok {
			if name := matchLeadingTarget(usable, token); name != "" {
				return IngressTarget{Name: name}
			}
			reason := g.explicitTargetReason(ctx, req.Caller, token)
			g.logIngressRefusal(ctx, req.Caller, "explicit_target", reason)
			return IngressTarget{
				Refusal:       unknownTargetText(token, g.accessHint(req.SignInLinkable)),
				RefusalReason: rejectReasonFor(reason),
			}
		}
	}

	res := reg.ResolveByDefaultName(req.DefaultAgentName)
	if res.Misconfigured {
		return IngressTarget{Misconfigured: true}
	}
	if res.Name == "" {
		return IngressTarget{}
	}
	card := reg.Get(res.Name)
	if card == nil || !g.CanAccessAgent(ctx, req.Caller, *card) {
		reason := g.delegationDenialReason(ctx, req.Caller, res.Name)
		g.logIngressRefusal(ctx, req.Caller, "default_target", reason)
		return IngressTarget{
			Refusal:       g.defaultTargetRefusal(reason, req.SignInLinkable),
			RefusalReason: taxonomy.RejectForbidden,
		}
	}
	return IngressTarget{Name: res.Name}
}

const reasonUnknownAgent = "unknown_agent"

func (g *GatewayCore) logIngressRefusal(ctx context.Context, caller *auth.Subject, target, reason string) {
	userID := ""
	if caller != nil {
		userID = caller.UserID()
	}
	g.logger.Log(ctx, refusalLogLevel(reason), "entrypoint refused agent target",
		"userFP", strutil.PIIFingerprint(userID),
		"target", target,
		"reason", reason,
	)
}

func refusalLogLevel(reason string) slog.Level {
	if reason == reasonUnknownAgent {
		return slog.LevelDebug
	}
	return slog.LevelWarn
}

func rejectReasonFor(reason string) string {
	if reason == reasonUnknownAgent {
		return taxonomy.RejectAgentNotFound
	}
	return taxonomy.RejectForbidden
}

func (g *GatewayCore) explicitTargetReason(ctx context.Context, caller *auth.Subject, token string) string {
	name := matchLeadingTarget(g.AgentRegistry().List(), token)
	if name == "" {
		return reasonUnknownAgent
	}
	if reason := g.delegationDenialReason(ctx, caller, name); reason != "" {
		return reason
	}
	return denyReasonRequiredScopes
}

func (g *GatewayCore) defaultTargetRefusal(reason string, signInLinkable bool) string {
	if reason == denyReasonNoIdentity {
		return unidentifiedText(g.webUIBaseURL())
	}
	return defaultTargetDeniedText(g.accessHint(signInLinkable))
}

func (g *GatewayCore) usableCards(ctx context.Context, caller *auth.Subject, cards []a2a.AgentCard) []a2a.AgentCard {
	usable := make([]a2a.AgentCard, 0, len(cards))
	for _, card := range cards {
		if a2a.IsEphemeralCard(card) {
			continue
		}
		if !g.CanAccessAgent(ctx, caller, card) {
			continue
		}
		usable = append(usable, card)
	}
	return usable
}

const trailingMentionPunctuation = "._-"

func leadingMention(text string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(text), "@")
	if !ok {
		return "", false
	}
	end := 0
	for end < len(rest) && isMentionNameChar(lowerASCII(rest[end])) {
		end++
	}
	token := strings.TrimRight(rest[:end], trailingMentionPunctuation)
	if token == "" {
		return "", false
	}
	return token, true
}

func matchLeadingTarget(cards []a2a.AgentCard, token string) string {
	for _, card := range cards {
		if strings.EqualFold(card.Name, token) {
			return card.Name
		}
		if display := a2a.DisplayNameFromCard(card); display != "" && strings.EqualFold(display, token) {
			return card.Name
		}
	}
	return ""
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
