// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolinit

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegisterTools_PartiallyRegisteredGroupIsDegraded(t *testing.T) {
	opts := Options{
		AdditionalBuiltinGroup: func(name string) []string {
			if name == "partial_group" {
				return []string{"get_current_time", ""}
			}
			return nil
		},
	}
	host := newTestHost("agent-1")

	_, entries, err := RegisterTools(host, []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "partial_group"},
	}, newTestServices(t), opts)
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want one entry record, got %d", len(entries))
	}
	if !entries[0].Degraded() {
		t.Errorf("a group that registered %v of its 2 declared members is not marked degraded (error=%q); an hil.tools key naming the missing one would fail the boot",
			entries[0].ToolNames, entries[0].Error)
	}
}

func TestRegisterTools_FullyRegisteredGroupIsNotDegraded(t *testing.T) {
	opts := Options{
		AdditionalBuiltinGroup: func(name string) []string {
			if name == "whole_group" {
				return []string{"get_current_time"}
			}
			return nil
		},
	}
	host := newTestHost("agent-2")

	_, entries, err := RegisterTools(host, []any{
		map[string]any{"tool_type": "builtin-group", "group_name": "whole_group"},
	}, newTestServices(t), opts)
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want one entry record, got %d", len(entries))
	}
	if entries[0].Degraded() {
		t.Errorf("a fully registered group is marked degraded (error=%q), which forgives every typo on it", entries[0].Error)
	}
}

func TestBuildsOwnEntryRecord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		toolType string
		want     bool
	}{
		{"mcp", true},
		{"openapi", true},
		{"builtin", false},
		{"builtin-group", false},
		{"python", false},
		{"sam_remote", false},
		{"", false},
		{"no_such_type", false},
	} {
		t.Run(tc.toolType, func(t *testing.T) {
			t.Parallel()
			if got := buildsOwnEntryRecord(tc.toolType); got != tc.want {
				t.Errorf("buildsOwnEntryRecord(%q) = %v, want %v", tc.toolType, got, tc.want)
			}
		})
	}
}

func TestRegisterTools_DroppedGateWarningFiresOnlyWhenTheGateIsReallyDropped(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entry    map[string]any
		wantWarn bool
	}{
		{"an unsupported type really does drop the gate", map[string]any{
			"tool_type": "no_such_type",
			"hil":       map[string]any{"require_approval": true},
		}, true},
		{"a missing type really does drop the gate", map[string]any{
			"hil": map[string]any{"require_approval": true},
		}, true},
		{"an openapi entry is not a dropped gate", map[string]any{
			"tool_type":         "openapi",
			"specification_url": "http://127.0.0.1:1/no-such-spec",
			"hil":               map[string]any{"require_approval": true},
		}, false},
		{"an unsupported type with no gate", map[string]any{
			"tool_type": "no_such_type",
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logged strings.Builder
			logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
			host := newTestHost("agent-warn")

			_, _, err := RegisterTools(host, []any{tc.entry}, newTestServices(t), Options{Logger: logger})
			if err != nil {
				t.Fatalf("RegisterTools: %v", err)
			}
			got := strings.Contains(logged.String(), "the block is ignored and nothing is gated")
			if got != tc.wantWarn {
				t.Errorf("dropped-gate warning fired = %v, want %v; logs were:\n%s", got, tc.wantWarn, logged.String())
			}
		})
	}
}

func TestRegisterTools_OpenAPIWithNoOperationsSaysSo(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewUnstartedServer(mux)
	mux.HandleFunc("/spec", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"openapi":"3.0.0","info":{"title":"Empty","version":"1.0.0"},`+
			`"servers":[{"url":%q}],"paths":{}}`, srv.URL)
	})
	srv.Start()
	t.Cleanup(srv.Close)

	var logged strings.Builder
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))
	host := newTestHost("agent-openapi")

	_, _, err := RegisterTools(host, []any{
		map[string]any{
			"tool_type":         "openapi",
			"specification_url": srv.URL + "/spec",
			"hil":               map[string]any{"require_approval": true},
		},
	}, newTestServices(t), Options{Logger: logger})
	if err != nil {
		t.Fatalf("RegisterTools: %v", err)
	}

	if got := logged.String(); !strings.Contains(got, "registered no tools") {
		t.Errorf("an entry that registered nothing said nothing; logs were:\n%s", got)
	}
}
