// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

func ParseLogLevel(level string) slog.Level {
	switch normalized := strings.ToUpper(strings.TrimSpace(level)); normalized {
	case "DEBUG":
		return slog.LevelDebug
	case "INFO", "":
		return slog.LevelInfo
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "ERROR", "CRITICAL":
		return slog.LevelError
	default:
		fmt.Fprintf(os.Stderr,
			"WARN: unrecognized log level %q; using INFO (expected one of DEBUG, INFO, WARNING, WARN, ERROR, CRITICAL)\n",
			level)
		return slog.LevelInfo
	}
}

type MultiHandler struct {
	handlers []slog.Handler
}

func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	wrapped := make([]slog.Handler, len(handlers))
	for i, h := range handlers {
		wrapped[i] = maskHandler(h)
	}
	return &MultiHandler{handlers: wrapped}
}

func maskHandler(h slog.Handler) slog.Handler {
	if _, already := h.(maskingHandler); already {
		return h
	}
	return maskingHandler{inner: h}
}

type maskingHandler struct{ inner slog.Handler }

func (m maskingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return m.inner.Enabled(ctx, level)
}

func (m maskingHandler) Handle(ctx context.Context, r slog.Record) error {
	masked := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		masked.AddAttrs(maskAttrDeep(a))
		return true
	})
	return m.inner.Handle(ctx, masked)
}

func (m maskingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = maskAttrDeep(a)
	}
	return maskingHandler{inner: m.inner.WithAttrs(out)}
}

func (m maskingHandler) WithGroup(name string) slog.Handler {
	return maskingHandler{inner: m.inner.WithGroup(name)}
}

func maskAttrDeep(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		src := a.Value.Group()
		out := make([]slog.Attr, len(src))
		for i, g := range src {
			out[i] = maskAttrDeep(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	return MaskSecretAttr(nil, a)
}

func (m *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: handlers}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &MultiHandler{handlers: handlers}
}
