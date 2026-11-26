// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"golang.org/x/net/idna"
)

var idnaProfile = idna.New(
	idna.MapForLookup(),
	idna.Transitional(true),
	idna.StrictDomainName(false),
)

const eventTypeDropped = "email.dropped"

type Dispatcher interface {
	Dispatch(ctx context.Context, n *NormalizedEmail, caller *auth.Subject, trust InternalTrustResult) (submitted bool, err error)
}

type HandleResult int

const (
	HandleProcessed HandleResult = iota
	HandleDropped
	HandleRetry
)

type SubmitRefusalReporter interface {
	ReportSubmitRefusal(reason string)
}

type Worker struct {
	Provider               EmailProvider
	Resolver               IdentityResolver
	Policy                 *AuthPolicy
	TrustedAuthservIDs     []string
	MaxMessageSizeBytes    int64
	MaxAttachmentSizeBytes int64
	Dispatcher             Dispatcher
	Logger                 *slog.Logger

	TrustedInternalTenantID string

	AllowedSenders []string

	MIMEFilter *MIMEFilter

	Refusals SubmitRefusalReporter

	Scanner AttachmentScanner
}

func (w *Worker) Handle(ctx context.Context, m RawMessage) HandleResult {
	result, refusal := w.classify(ctx, m)
	if w.Refusals != nil {
		w.Refusals.ReportSubmitRefusal(refusal)
	}
	return result
}

func (w *Worker) classify(ctx context.Context, m RawMessage) (HandleResult, string) {
	logger := w.Logger.With("msgUID", string(m.UID))

	if w.MaxMessageSizeBytes > 0 && int64(len(m.RFC822)) > w.MaxMessageSizeBytes {
		logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: oversized",
			slog.String("eventType", eventTypeDropped),
			slog.String("reason", "size_exceeded"),
			slog.Int("size_bytes", len(m.RFC822)),
			slog.Int64("limit", w.MaxMessageSizeBytes),
		)
		return HandleDropped, taxonomy.RejectMalformed
	}

	n, err := NormalizeWithOpts(m.RFC822, NormalizeOpts{
		TrustedAuthservIDs:     w.TrustedAuthservIDs,
		MaxAttachmentSizeBytes: w.MaxAttachmentSizeBytes,
	})
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: normalize failed",
			slog.String("eventType", eventTypeDropped),
			slog.String("errorClass", "malformed_input"),
			slog.String("reason", sanitizeForLog(err.Error())),
		)
		return HandleDropped, taxonomy.RejectMalformed
	}
	n.ReceivedAt = m.FetchedAt
	n.ProviderUID = m.UID
	logger = logger.With("msgID", n.MessageID, "from", maskEmail(n.From.Address))

	if len(w.AllowedSenders) > 0 {
		fromCanonical := strings.ToLower(strings.TrimSpace(n.From.Address))
		if !slices.Contains(w.AllowedSenders, fromCanonical) {
			logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: sender not in allowlist",
				slog.String("eventType", "email.sender_not_allowed"),
				slog.String("reason", "sender_not_allowlisted"),
			)
			return HandleDropped, taxonomy.RejectForbidden
		}
	}

	trust := EvaluateInternalTrust(n.RawHeaders, w.TrustedInternalTenantID, w.TrustedAuthservIDs)
	if w.TrustedInternalTenantID != "" && !trust.Trusted {
		logger.LogAttrs(ctx, slog.LevelInfo, "internal trust not applied",
			slog.String("eventType", "email.internal_trust_not_applied"),
			slog.String("authAs", sanitizeForLog(firstHeader(n.RawHeaders, "X-MS-Exchange-Organization-AuthAs"))),
			slog.String("crossTenantID", sanitizeForLog(firstHeader(n.RawHeaders, "X-MS-Exchange-CrossTenant-Id"))),
			slog.String("authSourcePresent", boolish(firstHeader(n.RawHeaders, "X-MS-Exchange-Organization-AuthSource") != "")),
			slog.String("configuredTenantID", w.TrustedInternalTenantID),
		)
	}

	switch w.Policy.Decide(ctx, n.AuthResults, trust, n.MessageID) {
	case PolicyReject:
		return HandleDropped, taxonomy.RejectForbidden
	case PolicyQuarantine:
	}

	if !trust.Trusted && n.AuthResults.AlignedFromDomain != "" {
		fromDomain := domainOf(n.From.Address)
		if !strings.EqualFold(fromDomain, n.AuthResults.AlignedFromDomain) {
			logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: from domain not aligned with DMARC verdict",
				slog.String("eventType", eventTypeDropped),
				slog.String("reason", "from_aligned_domain_mismatch"),
				slog.String("fromDomain", fromDomain),
				slog.String("alignedFromDomain", n.AuthResults.AlignedFromDomain),
			)
			return HandleDropped, taxonomy.RejectForbidden
		}
	}

	identity, err := w.Resolver.Resolve(ctx, n.From, n.AuthResults, trust)
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: identity resolve failed",
			slog.String("eventType", "email.identity_unknown"),
			slog.String("reason", sanitizeForLog(err.Error())),
		)
		return HandleDropped, taxonomy.RejectForbidden
	}
	identityID := userIDFromIdentity(identity)

	if w.MaxAttachmentSizeBytes > 0 {
		for _, a := range n.Attachments {
			if a.SizeBytes > w.MaxAttachmentSizeBytes {
				logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: attachment oversized",
					slog.String("eventType", eventTypeDropped),
					slog.String("reason", "attachment_size_exceeded"),
					slog.String("filename", sanitizeForLog(a.Filename)),
					slog.Int64("size_bytes", a.SizeBytes),
					slog.Int64("limit", w.MaxAttachmentSizeBytes),
				)
				return HandleDropped, taxonomy.RejectMalformed
			}
		}
	}

	if w.MIMEFilter != nil {
		for _, a := range n.Attachments {
			if ok, reason := w.MIMEFilter.Decision(a.DeclaredMIMEType, a.DetectedMIMEType); !ok {
				logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: attachment mime",
					slog.String("eventType", eventTypeDropped),
					slog.String("reason", reason),
					slog.String("filename", sanitizeForLog(a.Filename)),
					slog.String("declaredMime", sanitizeForLog(a.DeclaredMIMEType)),
					slog.String("detectedMime", sanitizeForLog(a.DetectedMIMEType)),
				)
				return HandleDropped, taxonomy.RejectMalformed
			}
		}
	}

	if w.Scanner != nil {
		for _, a := range n.Attachments {
			if len(a.Content.InlineBytes) == 0 {
				continue
			}
			verdict, scanErr := w.Scanner.Scan(ctx, a.Filename, a.Content.InlineBytes)
			if scanErr != nil {
				logger.LogAttrs(ctx, slog.LevelInfo, "scan error: retrying",
					slog.String("eventType", "email.scan_failed"),
					slog.String("errorClass", "transient_infra"),
					slog.String("reason", sanitizeForLog(scanErr.Error())),
					slog.String("filename", sanitizeForLog(a.Filename)),
				)
				return HandleRetry, ""
			}
			switch verdict {
			case ScanClean:
				continue
			case ScanInfected:
				logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: attachment infected",
					slog.String("eventType", eventTypeDropped),
					slog.String("reason", "infected_attachment"),
					slog.String("filename", sanitizeForLog(a.Filename)),
				)
				return HandleDropped, taxonomy.RejectMalformed
			default:
				logger.LogAttrs(ctx, slog.LevelInfo, "message dropped: scan inconclusive",
					slog.String("eventType", eventTypeDropped),
					slog.String("reason", "scan_inconclusive"),
					slog.String("verdict", string(verdict)),
					slog.String("filename", sanitizeForLog(a.Filename)),
				)
				return HandleDropped, taxonomy.RejectInternal
			}
		}
	}

	submitted, err := w.Dispatcher.Dispatch(ctx, n, identity, trust)
	if errors.Is(err, ErrAlreadyHandled) {
		logger.LogAttrs(ctx, slog.LevelInfo, "message already handled; flagging it seen",
			slog.String("eventType", "email.already_handled"),
		)
		return HandleProcessed, ""
	}
	if entrypoint.IsBudgetRefusal(err) {
		logger.LogAttrs(ctx, slog.LevelDebug, "message dropped: a budget is spent",
			slog.String("eventType", eventTypeDropped),
			slog.String("budgetTier", entrypoint.BudgetRefusalTier(err)),
			slog.String("reason", taxonomy.RejectBudgetExhausted),
			slog.String("identity", maskEmail(identityID)),
		)
		return HandleDropped, taxonomy.RejectBudgetExhausted
	}
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelWarn, "dispatch failed: retrying",
			slog.String("eventType", "email.dispatch_failed"),
			slog.String("errorClass", "transient_infra"),
			slog.String("reason", sanitizeForLog(err.Error())),
			slog.String("identity", maskEmail(identityID)),
		)
		return HandleRetry, ""
	}

	if submitted {
		logger.LogAttrs(ctx, slog.LevelInfo, "message dispatched",
			slog.String("eventType", "email.dispatched"),
			slog.String("identity", maskEmail(identityID)),
		)
		return HandleProcessed, ""
	}
	return HandleProcessed, taxonomy.RejectAgentNotFound
}

func userIDFromIdentity(id *auth.Subject) string {
	if id == nil {
		return ""
	}
	return id.UserID()
}

func boolish(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func domainOf(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 0 || at == len(addr)-1 {
		return ""
	}
	raw := strings.ToLower(strings.TrimSpace(addr[at+1:]))
	if raw == "" {
		return ""
	}
	punycode, err := idnaProfile.ToASCII(raw)
	if err != nil {
		return raw
	}
	return punycode
}
