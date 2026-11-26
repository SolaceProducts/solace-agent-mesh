// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package audit

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type StructuredLogger struct {
	logger *slog.Logger
	seq    atomic.Uint64
}

func NewStructuredLogger(logger *slog.Logger) *StructuredLogger {
	if logger != nil && !handlerIsJSON(logger.Handler()) {
		logger.Warn("audit logger initialised with non-JSON slog handler; recommended deployment is format: json - custom handlers may re-introduce log injection",
			"logger_name", "audit",
		)
	}
	return &StructuredLogger{logger: logger.With("logger_name", "audit", "processID", idgen.NewRandom())}
}

func handlerIsJSON(h slog.Handler) bool {
	if h == nil {
		return false
	}
	if _, ok := h.(*slog.JSONHandler); ok {
		return true
	}
	type jsonInside interface {
		JSONHandlerInside() bool
	}
	if w, ok := h.(jsonInside); ok && w.JSONHandlerInside() {
		return true
	}
	return false
}

func (l *StructuredLogger) nextSeq() uint64 {
	return l.seq.Add(1)
}

func (l *StructuredLogger) LogAuth(_ context.Context, event auth.AuthEvent) {
	seq := l.nextSeq()
	if event.Success {
		l.logger.Info("authentication succeeded", withCredential([]any{
			"seq", seq,
			"userID", event.UserID,
			"taskID", event.TaskID,
		}, event.CredentialID)...)
	} else {
		l.logger.Warn("authentication failed",
			"seq", seq,
			"userID", event.UserID,
			"taskID", event.TaskID,
			"reason", event.Reason,
		)
	}
}

func (l *StructuredLogger) LogToolAccess(_ context.Context, event auth.ToolAccessEvent) {
	seq := l.nextSeq()
	if event.Allowed {
		l.logger.Debug("tool access granted", withCredential([]any{
			"seq", seq,
			"userID", event.UserID,
			"tool", event.ToolName,
			"scopes", event.Scopes,
		}, event.CredentialID)...)
	} else {
		l.logger.Warn("tool access denied", withCredential([]any{
			"seq", seq,
			"userID", event.UserID,
			"tool", event.ToolName,
			"requiredScopes", event.Scopes,
		}, event.CredentialID)...)
	}
}

func (l *StructuredLogger) LogAgentAccess(_ context.Context, event auth.AgentAccessEvent) {
	seq := l.nextSeq()
	if event.Allowed {
		l.logger.Debug("agent delegation granted", withCredential([]any{
			"seq", seq,
			"userID", event.UserID,
			"agentName", event.AgentName,
		}, event.CredentialID)...)
	} else {
		l.logger.Warn("agent delegation denied", withCredential([]any{
			"seq", seq,
			"userID", event.UserID,
			"agentName", event.AgentName,
		}, event.CredentialID)...)
	}
}

func appendBuiltinFailureContext(attrs []any, event auth.ToolExecutionEvent) []any {
	if event.ToolErrorCode != "" {
		attrs = append(attrs, "errorCode", event.ToolErrorCode)
	}
	if event.ToolErrorSite != "" {
		attrs = append(attrs, "errorSite", event.ToolErrorSite)
	}
	if event.ToolErrorClass != "" {
		attrs = append(attrs, "errorClass", event.ToolErrorClass)
	}
	return attrs
}

func withCredential(attrs []any, credentialID string) []any {
	if credentialID == "" {
		return attrs
	}
	return append(attrs, "credentialID", credentialID)
}

func (l *StructuredLogger) LogToolExecution(ctx context.Context, event auth.ToolExecutionEvent) {
	seq := l.nextSeq()
	attrs := []any{
		"seq", seq,
		"userID", event.UserID,
		"agentName", event.AgentName,
		"tool", event.ToolName,
		"sessionID", event.SessionID,
		"duration_ms", event.DurationMs,
	}
	attrs = withCredential(attrs, event.CredentialID)
	if event.ToolCategory != "" {
		attrs = append(attrs, "toolCategory", event.ToolCategory)
	}
	if event.TraceID != "" {
		attrs = append(attrs, "traceID", event.TraceID)
	}
	if event.ApprovalReason != "" {
		attrs = append(attrs, "approvalReason", event.ApprovalReason)
	}
	if event.ApprovalRefusal != "" {
		attrs = append(attrs, "approvalRefusal", event.ApprovalRefusal)
	}
	if event.ApprovalRecipient != "" && event.ApprovalRecipient != event.UserID {
		attrs = append(attrs, "approvalRecipient", event.ApprovalRecipient)
	}
	if event.Success {
		l.logger.Info("tool execution succeeded", attrs...)
	} else {
		attrs = append(attrs, "error", event.Error)
		if event.ToolCategory == taxonomy.ToolCategoryBuiltin {
			attrs = appendBuiltinFailureContext(attrs, event)
		}
		l.logger.Log(ctx, toolFailureLevelFor(event), "tool execution failed", attrs...)
	}
}

var resultLevelToolFailures = map[string]bool{
	tool.AuditToolErrorCode:                true,
	string(remotetool.ErrToolError):        true,
	taxonomy.DispatchClassValidation:       true,
	string(remotetool.ErrInvalidRequest):   true,
	string(remotetool.ErrArtifactNotFound): true,
	taxonomy.DispatchClassCanceled:         true,
	taxonomy.DispatchClassAuthRequired:     true,
	taxonomy.DispatchClassBackend4xx:       true,
}

var decisionRefusals = map[string]bool{
	string(tool.RefusalDenied):             true,
	string(tool.RefusalTimedOut):           true,
	string(tool.RefusalCancelled):          true,
	string(tool.RefusalUnreadable):         true,
	string(tool.RefusalRunEnded):           true,
	string(tool.RefusalRunEndedUnanswered): true,
	string(tool.RefusalNoHumanInEvalRun):   true,
}

func toolFailureLevelFor(event auth.ToolExecutionEvent) slog.Level {
	if event.ApprovalRefusal != "" && !decisionRefusals[event.ApprovalRefusal] {
		return slog.LevelError
	}
	return toolFailureLevel(event.Error)
}

func toolFailureLevel(code string) slog.Level {
	if resultLevelToolFailures[code] {
		return slog.LevelWarn
	}
	return slog.LevelError
}

func (l *StructuredLogger) LogControlAccess(_ context.Context, event auth.ControlAccessEvent) {
	seq := l.nextSeq()
	if event.Allowed {
		l.logger.Debug("control access granted",
			"seq", seq,
			"userID", event.UserID,
			"method", event.Method,
			"appName", event.AppName,
		)
	} else {
		l.logger.Warn("control access denied",
			"seq", seq,
			"userID", event.UserID,
			"method", event.Method,
			"appName", event.AppName,
		)
	}
}

func (l *StructuredLogger) LogShareGrant(_ context.Context, event auth.ShareGrantEvent) {
	l.logger.Info("share granted",
		"seq", l.nextSeq(),
		"actor", event.Actor,
		"resourceType", event.ResourceType,
		"resourceID", event.ResourceID,
		"recipientEmailFP", strutil.PIIFingerprint(event.RecipientEmail),
		"accessLevel", event.AccessLevel,
	)
}

func (l *StructuredLogger) LogShareLevelChange(_ context.Context, event auth.ShareLevelChangeEvent) {
	l.logger.Info("share level changed",
		"seq", l.nextSeq(),
		"actor", event.Actor,
		"resourceType", event.ResourceType,
		"resourceID", event.ResourceID,
		"recipientEmailFP", strutil.PIIFingerprint(event.RecipientEmail),
		"accessLevel", event.AccessLevel,
	)
}

func (l *StructuredLogger) LogShareRevoke(_ context.Context, event auth.ShareRevokeEvent) {
	l.logger.Info("share revoked",
		"seq", l.nextSeq(),
		"actor", event.Actor,
		"resourceType", event.ResourceType,
		"resourceID", event.ResourceID,
		"recipientEmailFP", strutil.PIIFingerprint(event.RecipientEmail),
	)
}

func (l *StructuredLogger) LogTemplateDeleted(_ context.Context, event auth.TemplateDeletedEvent) {
	l.logger.Info("template deleted",
		"seq", l.nextSeq(),
		"actor", event.Actor,
		"resourceType", event.ResourceType,
		"resourceID", event.ResourceID,
		"recipientEmailFPs", fingerprintAll(event.RecipientEmails),
	)
}

func fingerprintAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strutil.PIIFingerprint(v)
	}
	return out
}

func (l *StructuredLogger) LogConsentChange(_ context.Context, event auth.ConsentChangeEvent) {
	l.logger.Info("consent changed",
		"seq", l.nextSeq(),
		"actor", event.Actor,
		"consentScope", event.Scope,
		"from", event.From,
		"to", event.To,
	)
}

func (l *StructuredLogger) LogActivationChange(_ context.Context, event auth.ActivationChangeEvent) {
	l.logger.Info("activation changed",
		"seq", l.nextSeq(),
		"actor", event.Actor,
		"change", event.Change,
		"subject", event.Subject,
		"freeModelTerms", event.FreeModelTerms,
		"agreementId", event.AgreementID,
	)
}

func (l *StructuredLogger) LogHILApproval(ctx context.Context, event auth.HILApprovalEvent) {
	level := slog.LevelInfo
	switch event.Outcome {
	case auth.HILApprovalRefused, auth.HILApprovalRequestDenied, auth.HILApprovalUndeliverable, auth.HILApprovalLate:
		level = slog.LevelWarn
	}
	l.logger.Log(ctx, level, "hil approval",
		"seq", l.nextSeq(),
		"requestID", event.RequestID,
		"outcome", event.Outcome,
		"approvers", event.Approvers,
		"placeTypes", event.PlaceTypes,
		"answerer", event.Answerer,
		"via", event.Via,
		"decision", event.Decision,
		"reason", event.Reason,
	)
}

var (
	_ auth.AuditLogger        = (*StructuredLogger)(nil)
	_ auth.HILApprovalAuditor = (*StructuredLogger)(nil)
)
