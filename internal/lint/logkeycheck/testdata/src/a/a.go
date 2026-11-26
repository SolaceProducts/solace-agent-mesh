package a

import (
	"context"
	"log/slog"
)

func good(logger *slog.Logger) {
	ctx := context.Background()

	logger.Info("ok", "taskID", "t1")
	logger.Info("ok", "userID", "u1", "traceID", "x")
	logger.Debug("ok", "baseURL", "http://x")
	logger.Warn("ok", "mcpServer", "s")
	logger.Error("ok", "duration_ms", 12)
	logger.Info("ok", "timeout_s", 30)
	logger.InfoContext(ctx, "ok", "taskID", "t1")
	logger.With("agentName", "a")

	slog.Info("ok", "taskID", "t1")
	slog.Warn("ok", "elapsed_ms", 5)
	slog.With("nodeID", "n")
	slog.String("baseURL", "http://x")
	slog.Int("retryCount", 3)
	slog.Group("payload", slog.String("toolCallID", "c"))

	logger.Info("ok", "iss", "issuer", "sub", "subject", "access_token", "tok")
	slog.String("grant_type", "client_credentials")

	// Reserved Datadog key "status" renamed to a domain key — not flagged.
	logger.Info("ok", "httpStatus", 200, "taskStatus", "completed")
	logger.Info("ok", "status", 200) //logkeys:allow
}

func bad(logger *slog.Logger) {
	ctx := context.Background()

	logger.Info("bad", "node_id", "n")                  // want `snake_case slog key "node_id"`
	logger.Warn("bad", "client_id", "c")                // want `snake_case slog key "client_id"`
	logger.Error("bad", "mime_type", "m")               // want `snake_case slog key "mime_type"`
	logger.InfoContext(ctx, "bad", "tool_call_id", "t") // want `snake_case slog key "tool_call_id"`
	slog.Info("bad", "provider_count", 2)               // want `snake_case slog key "provider_count"`
	slog.With("retry_count", 1)                         // want `snake_case slog key "retry_count"`
	slog.String("base_url", "http://x")                 // want `snake_case slog key "base_url"`
	slog.Group("p", slog.Int("msg_uid", 1))             // want `snake_case slog key "msg_uid"`
	logger.Error("bad", "status", 403)                  // want `reserved slog key "status"`

	logger.Info("wire", "agent_name", "a") //logkeys:allow
}
