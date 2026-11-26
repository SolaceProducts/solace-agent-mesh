// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package audit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

func TestLogToolExecution_FailureContextIsBuiltinOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		want     map[string]string
		absent   []string
	}{
		{
			name:     "a built-in failure names code, site and class",
			category: taxonomy.ToolCategoryBuiltin,
			want: map[string]string{
				"toolCategory": "builtin", "traceID": "trace-1", "errorCode": "ARTIFACT_NOT_FOUND",
				"errorSite": "internal/tool/remote/tool.go:12", "errorClass": "deadline",
			},
		},
		{
			name:     "a custom tool failure carries only category and trace",
			category: taxonomy.ToolCategoryCustom,
			want:     map[string]string{"toolCategory": "custom", "traceID": "trace-1"},
			absent:   []string{"errorCode", "errorSite", "errorClass"},
		},
		{
			name:     "an unclassified tool failure carries no failure context",
			category: "",
			want:     map[string]string{"traceID": "trace-1"},
			absent:   []string{"toolCategory", "errorCode", "errorSite", "errorClass"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, buf := testLoggerJSON()
			l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
				UserID: "u1", AgentName: "a1", ToolName: "probe", SessionID: "s1",
				ToolCategory: tc.category, TraceID: "trace-1",
				Success: false, Error: "tool_error",
				ToolErrorCode: "ARTIFACT_NOT_FOUND", ToolErrorSite: "internal/tool/remote/tool.go:12", ToolErrorClass: "deadline",
			})

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("record is not JSON: %v: %s", err, buf.String())
			}
			if rec["msg"] != "tool execution failed" {
				t.Errorf("msg = %v, want tool execution failed", rec["msg"])
			}
			for key, want := range tc.want {
				if rec[key] != want {
					t.Errorf("%s = %v, want %q", key, rec[key], want)
				}
			}
			for _, key := range tc.absent {
				if _, present := rec[key]; present {
					t.Errorf("%s present on a record that must not carry it: %s", key, buf.String())
				}
			}
		})
	}
}

func TestLogToolExecution_SuccessCarriesNoFailureContext(t *testing.T) {
	l, buf := testLoggerJSON()
	l.LogToolExecution(context.Background(), auth.ToolExecutionEvent{
		UserID: "u1", AgentName: "a1", ToolName: "probe", SessionID: "s1",
		ToolCategory: taxonomy.ToolCategoryBuiltin, Success: true,
		ToolErrorCode: "LEFTOVER", ToolErrorSite: "internal/x.go:1", ToolErrorClass: "other",
	})
	for _, key := range []string{"errorCode", "errorSite", "errorClass"} {
		if strings.Contains(buf.String(), key) {
			t.Errorf("success record carries %s: %s", key, buf.String())
		}
	}
}
