// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
)

type levelByErrorID struct {
	mu     sync.Mutex
	levels map[string]slog.Level
}

func (h *levelByErrorID) Enabled(context.Context, slog.Level) bool { return true }

func (h *levelByErrorID) Handle(_ context.Context, r slog.Record) error {
	var errID string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "errorId" {
			errID = a.Value.String()
			return false
		}
		return true
	})
	if errID == "" {
		return nil
	}
	h.mu.Lock()
	h.levels[errID] = r.Level
	h.mu.Unlock()
	return nil
}

func (h *levelByErrorID) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelByErrorID) WithGroup(string) slog.Handler      { return h }

func TestWriteErrorLogLevel(t *testing.T) {
	capture := &levelByErrorID{levels: map[string]slog.Level{}}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cases := []struct {
		name   string
		errID  string
		status int
		want   slog.Level
	}{
		{"bad request", "errid-400", 400, slog.LevelWarn},
		{"not found", "errid-404", 404, slog.LevelWarn},
		{"conflict", "errid-409", 409, slog.LevelWarn},
		{"server error", "errid-500", 500, slog.LevelError},
	}
	for _, tc := range cases {
		writeErrorWithID(httptest.NewRecorder(), tc.status, "boom", tc.errID)
	}

	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, tc := range cases {
		if got := capture.levels[tc.errID]; got != tc.want {
			t.Errorf("status %d (%s): logged at %v, want %v", tc.status, tc.name, got, tc.want)
		}
	}
}
