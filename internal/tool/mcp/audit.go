// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type redactionRule struct {
	re      *regexp.Regexp
	replace string
}

var secretRedactionRules = []redactionRule{
	{regexp.MustCompile(`(?i)\b(Bearer)\s+[^\s"',;]+`), "$1 [REDACTED]"},
	{regexp.MustCompile(`(?i)\b(Authorization):\s*[^\n\r]+`), "$1: [REDACTED]"},
	{regexp.MustCompile(`(?i)\b(Cookie):\s*[^\n\r]+`), "$1: [REDACTED]"},
	{regexp.MustCompile(`(?i)\b(api[_-]?key)\s*([:=])\s*[^\s"',;&]+`), "$1$2[REDACTED]"},
	{regexp.MustCompile(`(?i)\b((?:access|refresh|id)_token)\s*([:=])\s*[^\s"',;&]+`), "$1$2[REDACTED]"},
}

const (
	auditMsgCall    = "mcp_tool_call"
	auditMsgSuccess = "mcp_tool_success"
	auditMsgFailure = "mcp_tool_failure"
)

type auditIdentity struct {
	UserID    string
	AgentID   string
	SessionID string
	ToolName  string
	TraceID   string
}

func auditIdentityFor(toolName string, toolCtx *tool.Context) auditIdentity {
	id := auditIdentity{ToolName: toolName}
	if toolCtx == nil {
		return id
	}
	id.UserID = toolCtx.UserID
	id.AgentID = toolCtx.AgentName
	id.SessionID = toolCtx.SessionID
	id.TraceID = toolCtx.TraceID
	return id
}

func logCall(logger *slog.Logger, id auditIdentity) {
	if logger == nil {
		return
	}
	logger.Info(auditMsgCall,
		"userID", id.UserID,
		"agentID", id.AgentID,
		"toolName", id.ToolName,
		"sessionID", id.SessionID,
		"traceID", id.TraceID,
	)
}

func logSuccess(logger *slog.Logger, id auditIdentity, elapsed time.Duration) {
	if logger == nil {
		return
	}
	logger.Info(auditMsgSuccess,
		"userID", id.UserID,
		"agentID", id.AgentID,
		"toolName", id.ToolName,
		"sessionID", id.SessionID,
		"duration_ms", durationMilliseconds(elapsed),
		"traceID", id.TraceID,
	)
}

func logFailure(logger *slog.Logger, id auditIdentity, elapsed time.Duration, level slog.Level, errType, errMsg string) {
	if logger == nil {
		return
	}
	logger.Log(context.Background(), level, auditMsgFailure,
		"userID", id.UserID,
		"agentID", id.AgentID,
		"toolName", id.ToolName,
		"sessionID", id.SessionID,
		"duration_ms", durationMilliseconds(elapsed),
		"errorType", errType,
		"error", sanitizeErrorMessage(errMsg),
		"traceID", id.TraceID,
	)
}

func emitAuditResult(logger *slog.Logger, id auditIdentity, elapsed time.Duration, res *tool.Result, retErr error) {
	if logger == nil {
		return
	}
	if retErr != nil {
		logFailure(logger, id, elapsed, slog.LevelError, fmt.Sprintf("%T", retErr), retErr.Error())
		return
	}
	if res == nil {
		logFailure(logger, id, elapsed, slog.LevelError, "nil_result", "tool returned nil result")
		return
	}
	switch res.Status {
	case tool.StatusError:
		logFailure(logger, id, elapsed, slog.LevelWarn, "tool_error", res.Message)
	case tool.StatusPending:
		return
	default:
		logSuccess(logger, id, elapsed)
	}
}

func durationMilliseconds(d time.Duration) float64 {
	ms := float64(d.Microseconds()) / 1000.0
	return float64(int64(ms*100+0.5)) / 100.0
}

func sanitizeErrorMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	const maxLen = 512
	if len(msg) > maxLen {
		msg = msg[:maxLen] + "...(truncated)"
	}
	for _, rule := range secretRedactionRules {
		msg = rule.re.ReplaceAllString(msg, rule.replace)
	}
	return msg
}
