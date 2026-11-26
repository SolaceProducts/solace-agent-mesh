// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"strings"
	"unicode"
)

const maxLoggedHeaderBytes = 1024

func sanitizeForLog(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if r < 0x20 || r == 0x7F || unicode.Is(unicode.Cc, r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	out := strings.TrimSpace(b.String())
	if len(out) > maxLoggedHeaderBytes {
		out = out[:maxLoggedHeaderBytes] + "...(truncated)"
	}
	return out
}

func SanitizeForLog(s string) string { return sanitizeForLog(s) }

type PolicyAction string

const (
	PolicyAccept     PolicyAction = "accept"
	PolicyReject     PolicyAction = "reject"
	PolicyQuarantine PolicyAction = "quarantine"
)

type AuthPolicy struct {
	Mode   PolicyMode
	Logger *slog.Logger
}

func NewAuthPolicy(mode PolicyMode, logger *slog.Logger) *AuthPolicy {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthPolicy{Mode: mode, Logger: logger}
}

func (p AuthPolicy) Decide(ctx context.Context, v AuthVerdict, trust InternalTrustResult, msgID string) PolicyAction {
	if trust.Trusted {
		p.logInternalTrustAdmit(ctx, v, trust, msgID)
		return PolicyAccept
	}

	switch p.Mode {
	case ModeRequireDMARC:
		if v.DMARC == AuthPass {
			return PolicyAccept
		}
		p.logRejection(ctx, v, msgID, "dmarc not pass")
		return PolicyReject
	case ModeStrict:
		if v.SPF == AuthPass && v.DKIM == AuthPass && v.DMARC == AuthPass {
			return PolicyAccept
		}
		p.logRejection(ctx, v, msgID, "strict: not all pass")
		return PolicyReject
	case ModeLogOnly:
		p.logEvaluated(ctx, v, msgID)
		return PolicyAccept
	default:
		p.warnUnknownMode(ctx, msgID)
		if v.DMARC == AuthPass {
			return PolicyAccept
		}
		p.logRejection(ctx, v, msgID, "dmarc not pass")
		return PolicyReject
	}
}

func (p AuthPolicy) logEvaluated(ctx context.Context, v AuthVerdict, msgID string) {
	p.Logger.LogAttrs(ctx, slog.LevelInfo, "auth verdict (log-only mode)",
		slog.String("eventType", "email.auth_evaluated"),
		slog.String("msgID", msgID),
		slog.String("authSPF", string(v.SPF)),
		slog.String("authDKIM", string(v.DKIM)),
		slog.String("authDMARC", string(v.DMARC)),
		slog.String("decision", string(PolicyAccept)),
	)
}

func (p AuthPolicy) logRejection(ctx context.Context, v AuthVerdict, msgID, reason string) {
	p.Logger.LogAttrs(ctx, slog.LevelInfo, "auth rejected",
		slog.String("eventType", "email.auth_rejected"),
		slog.String("msgID", msgID),
		slog.String("authSPF", string(v.SPF)),
		slog.String("authDKIM", string(v.DKIM)),
		slog.String("authDMARC", string(v.DMARC)),
		slog.String("decision", string(PolicyReject)),
		slog.String("reason", reason),
	)
}

func (p AuthPolicy) logInternalTrustAdmit(ctx context.Context, v AuthVerdict, trust InternalTrustResult, msgID string) {
	p.Logger.LogAttrs(ctx, slog.LevelInfo, "auth admitted via internal trust",
		slog.String("eventType", "email.allow_internal_trust"),
		slog.String("msgID", msgID),
		slog.String("authSPF", string(v.SPF)),
		slog.String("authDKIM", string(v.DKIM)),
		slog.String("authDMARC", string(v.DMARC)),
		slog.String("decision", "allow_internal_trust"),
		slog.String("internalTrustReason", trust.Reason),
		slog.String("internalTrustTenantID", trust.TenantID),
	)
}

func (p AuthPolicy) warnUnknownMode(ctx context.Context, msgID string) {
	p.Logger.LogAttrs(ctx, slog.LevelWarn, "unknown policy mode; treating as require_dmarc",
		slog.String("eventType", "email.policy_mode_unknown"),
		slog.String("mode", string(p.Mode)),
		slog.String("msgID", msgID),
	)
}
