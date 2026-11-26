// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

type IdentityResolver interface {
	Resolve(ctx context.Context, from EmailAddress, verdict AuthVerdict, trust InternalTrustResult) (*auth.Subject, error)
}

type EmailIdentityResolver struct {
	logger       *slog.Logger
	requireDMARC bool
}

func NewEmailIdentityResolver(logger *slog.Logger, requireDMARCForIdentity bool) *EmailIdentityResolver {
	return &EmailIdentityResolver{logger: logger, requireDMARC: requireDMARCForIdentity}
}

func (r *EmailIdentityResolver) Resolve(ctx context.Context, from EmailAddress, verdict AuthVerdict, trust InternalTrustResult) (*auth.Subject, error) {
	canonical := canonicalEmail(from.Address)
	if canonical == "" {
		return nil, fmt.Errorf("email: cannot resolve identity from empty sender address")
	}
	if r.requireDMARC && verdict.DMARC != AuthPass && !trust.Trusted {
		if r.logger != nil {
			r.logger.LogAttrs(ctx, slog.LevelInfo, "identity refused: DMARC not pass",
				slog.String("eventType", "email.identity_refused_dmarc"),
				slog.String("from", maskEmail(canonical)),
				slog.String("authDMARC", string(verdict.DMARC)),
			)
		}
		return nil, fmt.Errorf("email: identity refused; DMARC verdict %q is not pass (require_dmarc_for_identity=true)", verdict.DMARC)
	}
	return auth.ForEmailSender(auth.ExternalID{Type: auth.IDEmail, Value: canonical}, from.DisplayName,
		verdict.DMARC == AuthPass || trust.Trusted)
}

func canonicalEmail(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}
