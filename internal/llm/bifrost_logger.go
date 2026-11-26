// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"log/slog"

	"github.com/maximhq/bifrost/core/schemas"
)

type bifrostSlogLogger struct {
	logger *slog.Logger
}

func newBifrostLogger(logger *slog.Logger) *bifrostSlogLogger {
	if logger == nil {
		logger = slog.Default()
	}
	return &bifrostSlogLogger{logger: logger.With("source", "bifrost")}
}

func (l *bifrostSlogLogger) Debug(msg string, args ...any) { l.emit(l.logger.Debug, msg, args) }

func (l *bifrostSlogLogger) Info(msg string, args ...any) { l.emit(l.logger.Debug, msg, args) }

func (l *bifrostSlogLogger) Warn(msg string, args ...any) { l.emit(l.logger.Warn, msg, args) }

func (l *bifrostSlogLogger) Error(msg string, args ...any) { l.emit(l.logger.Error, msg, args) }

func (l *bifrostSlogLogger) Fatal(msg string, args ...any) { l.emit(l.logger.Error, msg, args) }

func (l *bifrostSlogLogger) emit(fn func(string, ...any), msg string, args []any) {
	if len(args) == 0 {
		fn(msg)
		return
	}
	fn(msg, "args", args)
}

func (l *bifrostSlogLogger) SetLevel(schemas.LogLevel) {}

func (l *bifrostSlogLogger) SetOutputType(schemas.LoggerOutputType) {}

func (l *bifrostSlogLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}
