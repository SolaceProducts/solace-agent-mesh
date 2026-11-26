// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestAgentAudit_FailureRecordCarriesDiagnosisFields(t *testing.T) {
	for _, tc := range []struct {
		name         string
		category     string
		exec         stubExecFn
		wantSuccess  bool
		wantCode     string
		wantSitePart string
	}{
		{
			name:     "a tool error result is a failure with its code and site",
			category: taxonomy.ToolCategoryBuiltin,
			exec: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return tool.Error("secret customer text", tool.WithErrorCode("ARTIFACT_NOT_FOUND")), nil
			},
			wantCode:     "ARTIFACT_NOT_FOUND",
			wantSitePart: "internal/agent/tool_failure_audit_test.go:",
		},
		{
			name:     "a failed result is a failure carrying its detail code",
			category: taxonomy.ToolCategoryBuiltin,
			exec: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return tool.Failed("crashed", &tool.ToolFailedDetail{Code: "TOOL_EXIT_NONZERO"}), nil
			},
			wantCode:     "TOOL_EXIT_NONZERO",
			wantSitePart: "internal/agent/tool_failure_audit_test.go:",
		},
		{
			name:     "a failed result without a detail code falls back to the generic failed code",
			category: taxonomy.ToolCategoryBuiltin,
			exec: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return tool.Failed("crashed", &tool.ToolFailedDetail{}), nil
			},
			wantCode:     tool.ErrorCodeToolFailed,
			wantSitePart: "internal/agent/tool_failure_audit_test.go:",
		},
		{
			name:     "a customer tool failure never hands its code or site to the audit logger",
			category: taxonomy.ToolCategoryCustom,
			exec: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return tool.Error("secret customer text", tool.WithErrorCode("CUSTOMER_CODE"), tool.WithCause(context.DeadlineExceeded)), nil
			},
		},
		{
			name:     "a successful result carries no failure fields",
			category: taxonomy.ToolCategoryBuiltin,
			exec: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
				return tool.OK("fine"), nil
			},
			wantSuccess: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newToolStatusHarness(t, "task-fail-audit")
			h.registerTool(t, "probe", tc.exec)
			rec := &toolAuditRecorder{}
			h.agent.svc.(*mockServices).auditLog = rec
			h.agent.RecordToolProvenance("probe", ToolProvenance{Category: tc.category})
			tCtx := &tool.Context{
				AgentName:       h.agent.cfg.Name,
				UserID:          "user-1",
				SessionID:       "ctx-1",
				TraceID:         "trace-1",
				ArtifactService: &artifact.ScopedService{Inner: memory.New()},
			}

			h.agent.dispatchToolCallsWithStatus(t.Context(),
				[]llm.ToolCall{{ID: "call-1", Name: "probe", Arguments: `{}`}}, tCtx, h.sp, "")

			ev := rec.only(t)
			if ev.Success != tc.wantSuccess {
				t.Errorf("success = %v, want %v", ev.Success, tc.wantSuccess)
			}
			if ev.ToolCategory != tc.category || ev.TraceID != "trace-1" {
				t.Errorf("category/traceID = %q/%q, want %q/trace-1", ev.ToolCategory, ev.TraceID, tc.category)
			}
			if ev.ToolErrorCode != tc.wantCode {
				t.Errorf("errorCode = %q, want %q", ev.ToolErrorCode, tc.wantCode)
			}
			if tc.wantSitePart == "" && ev.ToolErrorSite != "" {
				t.Errorf("errorSite = %q on a success, want empty", ev.ToolErrorSite)
			}
			if !strings.HasPrefix(ev.ToolErrorSite, tc.wantSitePart) {
				t.Errorf("errorSite = %q, want prefix %q", ev.ToolErrorSite, tc.wantSitePart)
			}
		})
	}
}

func TestAgentAudit_RefusalRecordNamesTraceAndCategory(t *testing.T) {
	h := newGatedAgentHarness(t, "task-hil-audit-refusal-fields", false)
	h.agent.RecordToolProvenance("web_request", ToolProvenance{Category: taxonomy.ToolCategoryBuiltin})
	h.tCtx.TraceID = "trace-9"

	h.agent.dispatchToolCallsWithStatus(t.Context(), gatedCall(), h.tCtx, h.sp, "")

	ev := h.audit.only(t)
	if ev.TraceID != "trace-9" || ev.ToolCategory != taxonomy.ToolCategoryBuiltin {
		t.Errorf("refusal record traceID/category = %q/%q, want trace-9/builtin", ev.TraceID, ev.ToolCategory)
	}
}
