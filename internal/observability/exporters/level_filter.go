// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package exporters

import (
	"context"
	"log/slog"
	"strings"
)

type levelFilteringHandler struct {
	inner slog.Handler
	level slog.Leveler
}

func newLevelFilteringHandler(inner slog.Handler, level slog.Leveler) *levelFilteringHandler {
	return &levelFilteringHandler{inner: inner, level: level}
}

func (h *levelFilteringHandler) Enabled(ctx context.Context, level slog.Level) bool {
	if level < h.level.Level() {
		return false
	}
	return h.inner.Enabled(ctx, level)
}

func (h *levelFilteringHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level < h.level.Level() {
		return nil
	}
	return h.inner.Handle(ctx, r)
}

func (h *levelFilteringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelFilteringHandler{inner: h.inner.WithAttrs(attrs), level: h.level}
}

func (h *levelFilteringHandler) WithGroup(name string) slog.Handler {
	return &levelFilteringHandler{inner: h.inner.WithGroup(name), level: h.level}
}

const levelCritical = slog.LevelError + 4

func LevelFromString(s string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO", "":
		return slog.LevelInfo
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	case "CRITICAL":
		return levelCritical
	default:
		return slog.LevelInfo
	}
}
