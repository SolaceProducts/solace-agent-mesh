// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func respLimitSpec(baseURL, operationID, path string) string {
	return fmt.Sprintf(`{
  "openapi": "3.0.0",
  "info": {"title": "Sized", "version": "1.0.0"},
  "servers": [{"url": %q}],
  "paths": {
    %q: {
      "get": {
        "operationId": %q,
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"type": "object"}}}}}
      }
    }
  }
}`, baseURL, path, operationID)
}

func respLimitServer(t *testing.T, bodySize int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewUnstartedServer(mux)

	mux.HandleFunc("/spec/", func(w http.ResponseWriter, r *http.Request) {
		op := strings.TrimPrefix(r.URL.Path, "/spec/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respLimitSpec(srv.URL, op, "/data/"+op)))
	})
	mux.HandleFunc("/data/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"payload":%q}`, strings.Repeat("x", bodySize))
	})

	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func respLimitDiscoverOne(t *testing.T, mgr *Manager, raw map[string]any) *OpenAPITool {
	t.Helper()
	cfg, err := ParseToolsetConfig(raw)
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	tools, err := mgr.LoadAndDiscoverTools(cfg)
	if err != nil {
		t.Fatalf("LoadAndDiscoverTools: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("discovered %d tools, want 1", len(tools))
	}
	return tools[0]
}

func TestResponseLimit_TwoToolsetsOnSharedManager(t *testing.T) {
	t.Parallel()
	const bodySize = 4096
	srv := respLimitServer(t, bodySize)
	mgr := NewManager(slog.Default(), nil)

	strict := respLimitDiscoverOne(t, mgr, map[string]any{
		"specification_url": srv.URL + "/spec/fetchStrict",
		"max_response_size": 512,
	})
	roomy := respLimitDiscoverOne(t, mgr, map[string]any{
		"specification_url": srv.URL + "/spec/fetchRoomy",
		"max_response_size": 65536,
	})

	strictResult, err := mgr.ExecuteTool(t.Context(), strict, nil)
	if err != nil {
		t.Fatalf("strict: unexpected error: %v", err)
	}
	if strictResult.Status != tool.StatusError {
		t.Errorf("strict: status = %q, want %q", strictResult.Status, tool.StatusError)
	}
	if !strings.Contains(strictResult.Message, "exceeds limit 512 bytes") {
		t.Errorf("strict: message = %q, want it to name the 512 byte limit", strictResult.Message)
	}

	roomyResult, err := mgr.ExecuteTool(t.Context(), roomy, nil)
	if err != nil {
		t.Fatalf("roomy: unexpected error: %v", err)
	}
	if roomyResult.Status == tool.StatusError {
		t.Fatalf("roomy: status = %q, want success: %s", roomyResult.Status, roomyResult.Message)
	}
	payload, ok := roomyResult.Data["payload"].(string)
	if !ok {
		t.Fatalf("roomy: payload missing from %#v", roomyResult.Data)
	}
	if len(payload) != bodySize {
		t.Errorf("roomy: payload is %d bytes, want the whole %d", len(payload), bodySize)
	}
}

func TestReadBodyLimited_Boundary(t *testing.T) {
	t.Parallel()
	const limit = 1024
	for _, size := range []int{limit - 1, limit, limit + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			body := strings.Repeat("y", size)
			buf, truncated, err := readBodyLimited(strings.NewReader(body), limit)
			if err != nil {
				t.Fatalf("readBodyLimited: %v", err)
			}
			wantTruncated := size > limit
			if truncated != wantTruncated {
				t.Errorf("truncated = %v, want %v for %d bytes at limit %d",
					truncated, wantTruncated, size, limit)
			}
			if wantTruncated {
				return
			}
			if len(buf) != size {
				t.Errorf("returned %d bytes, want the whole %d", len(buf), size)
			}
		})
	}
}

func TestResponseLimit_CeilingBoundsConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		configured int64
		want       int64
	}{
		{"unset falls back to the default", 0, DefaultMaxResponseSize},
		{"a modest value is used as given", 2048, 2048},
		{"the ceiling itself is allowed", MaxResponseSizeCeiling, MaxResponseSizeCeiling},
		{"one byte above the ceiling is clamped", MaxResponseSizeCeiling + 1, MaxResponseSizeCeiling},
		{"an absurd value is clamped", 5_000_000_000, MaxResponseSizeCeiling},
		{"MaxInt64 is clamped", math.MaxInt64, MaxResponseSizeCeiling},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg := &ToolsetConfig{MaxResponseSize: c.configured}
			if got := clampResponseSize(cfg.MaxResponseSize); got != c.want {
				t.Errorf("clampResponseSize() = %d, want %d", got, c.want)
			}
			mgr := NewManager(slog.Default(), nil)
			if got := mgr.maxResponseSize(&OpenAPITool{maxResponseSize: c.configured}); got != c.want {
				t.Errorf("Manager.maxResponseSize() = %d, want %d", got, c.want)
			}
		})
	}
}

func TestReadBodyLimited_MaxIntDoesNotReadEmpty(t *testing.T) {
	t.Parallel()
	const body = "hello world"
	buf, truncated, err := readBodyLimited(strings.NewReader(body), math.MaxInt64)
	if err != nil {
		t.Fatalf("readBodyLimited: %v", err)
	}
	if truncated {
		t.Errorf("truncated = true for an %d byte body", len(body))
	}
	if string(buf) != body {
		t.Errorf("body = %q, want %q", string(buf), body)
	}
}

func TestParseToolsetConfig_MaxResponseSizeRejections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"negative", -1, "must be positive"},
		{"zero, which the schema declares invalid", 0, "must be positive"},
		{"a size with units", "512KB", "whole number of bytes"},
		{"a fractional byte count", 512.5, "not fractional"},
		{"a fractional string", "512.5", "whole number of bytes"},
		{"a bool", true, "whole number of bytes"},
		{"a list", []any{1}, "whole number of bytes"},
		{"a mapping", map[string]any{"bytes": 512}, "whole number of bytes"},
		{"above the ceiling", MaxResponseSizeCeiling + 1, "exceeds the"},
		{"an absurd byte count", 5_000_000_000, "exceeds the"},
		{"a float beyond int64, identically on every arch", 1e19, "out of range"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseToolsetConfig(map[string]any{
				"specification_url": "http://example.com",
				"max_response_size": c.value,
			})
			if err == nil {
				t.Fatalf("ParseToolsetConfig accepted max_response_size: %v", c.value)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestParseToolsetConfig_MaxResponseSizeNotSpecified(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  map[string]any
	}{
		{"absent", map[string]any{"specification_url": "http://example.com"}},
		{"present but null", map[string]any{"specification_url": "http://example.com", "max_response_size": nil}},
		{"present but empty after substitution", map[string]any{"specification_url": "http://example.com", "max_response_size": ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := ParseToolsetConfig(c.raw)
			if err != nil {
				t.Fatalf("ParseToolsetConfig: %v", err)
			}
			if cfg.MaxResponseSize != 0 {
				t.Errorf("MaxResponseSize = %d, want 0 so the caller applies the default", cfg.MaxResponseSize)
			}
			if got := clampResponseSize(cfg.MaxResponseSize); got != DefaultMaxResponseSize {
				t.Errorf("resolved limit = %d, want the %d byte default", got, DefaultMaxResponseSize)
			}
		})
	}
}

func TestParseToolsetConfig_MaxResponseSizeAccepted(t *testing.T) {
	t.Parallel()
	cfg, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com",
		"max_response_size": 2048,
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig: %v", err)
	}
	if cfg.MaxResponseSize != 2048 {
		t.Errorf("MaxResponseSize = %d, want 2048", cfg.MaxResponseSize)
	}

	fromEnv, err := ParseToolsetConfig(map[string]any{
		"specification_url": "http://example.com",
		"max_response_size": "4096",
	})
	if err != nil {
		t.Fatalf("ParseToolsetConfig with a string value: %v", err)
	}
	if fromEnv.MaxResponseSize != 4096 {
		t.Errorf("MaxResponseSize = %d, want 4096 from the string form", fromEnv.MaxResponseSize)
	}

	absent, err := ParseToolsetConfig(map[string]any{"specification_url": "http://example.com"})
	if err != nil {
		t.Fatalf("ParseToolsetConfig without the key: %v", err)
	}
	if absent.MaxResponseSize != 0 {
		t.Errorf("MaxResponseSize = %d, want 0 so the caller applies the default", absent.MaxResponseSize)
	}
	if clampResponseSize(absent.MaxResponseSize) != DefaultMaxResponseSize {
		t.Errorf("clampResponseSize() = %d, want %d",
			clampResponseSize(absent.MaxResponseSize), DefaultMaxResponseSize)
	}
}

type capBreachLog struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capBreachLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *capBreachLog) WithAttrs([]slog.Attr) slog.Handler       { return h }
func (h *capBreachLog) WithGroup(string) slog.Handler            { return h }

func (h *capBreachLog) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *capBreachLog) find(msg string) (slog.Record, map[string]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message != msg {
			continue
		}
		attrs := map[string]string{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		return r, attrs, true
	}
	return slog.Record{}, nil, false
}

func TestResponseLimit_RejectionIsLoggedForBothStatuses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		status int
	}{
		{"an oversized success", http.StatusOK},
		{"an oversized upstream failure", http.StatusBadGateway},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			mux := http.NewServeMux()
			srv := httptest.NewUnstartedServer(mux)
			mux.HandleFunc("/spec/fetchBig", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(respLimitSpec(srv.URL, "fetchBig", "/big")))
			})
			mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(strings.Repeat("z", 4096)))
			})
			srv.Start()
			t.Cleanup(srv.Close)

			capture := &capBreachLog{}
			mgr := NewManager(slog.New(capture), nil)
			tl := respLimitDiscoverOne(t, mgr, map[string]any{
				"specification_url": srv.URL + "/spec/fetchBig",
				"max_response_size": 512,
			})

			if _, err := mgr.ExecuteTool(t.Context(), tl, nil); err != nil {
				t.Fatalf("ExecuteTool: %v", err)
			}

			rec, attrs, ok := capture.find("openapi response exceeded configured cap")
			if !ok {
				t.Fatal("no cap-exceeded log record; an operator would see only " +
					"internal/agent's message-less \"tool returned error\"")
			}
			if rec.Level != slog.LevelWarn {
				t.Errorf("level = %v, want WARN for operator-visible misconfiguration", rec.Level)
			}
			if attrs["limitBytes"] != "512" {
				t.Errorf("limitBytes = %q, want \"512\"", attrs["limitBytes"])
			}
			if attrs["httpStatus"] != strconv.Itoa(c.status) {
				t.Errorf("httpStatus = %q, want %q", attrs["httpStatus"], strconv.Itoa(c.status))
			}
		})
	}
}
