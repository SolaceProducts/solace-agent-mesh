// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"strings"
	"testing"
)

func validWebhookValues() map[string]any {
	return map[string]any{
		"routePath":       "/orders",
		"method":          "POST",
		"authMode":        "token",
		"authToken":       "dev-webhook-token",
		"targetAgentName": "Orchestrator",
		"inputExpression": "input.payload:message",
	}
}

func TestValidateWebhookValues(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m map[string]any)
		wantErr string
	}{
		{name: "valid"},
		{name: "no target", mutate: func(m map[string]any) { delete(m, "targetAgentName") }, wantErr: "targetAgentName or targetWorkflowName"},
		{name: "target mutex", mutate: func(m map[string]any) { m["targetWorkflowName"] = "WF" }, wantErr: "mutually exclusive"},
		{name: "explicit agent target", mutate: func(m map[string]any) { m["targetType"] = "agent" }},
		{
			name: "explicit workflow target",
			mutate: func(m map[string]any) {
				m["targetType"] = "workflow"
				delete(m, "targetAgentName")
				m["targetWorkflowName"] = "OrderIntake"
			},
		},
		{
			name:    "target type workflow disagrees with the set agent target",
			mutate:  func(m map[string]any) { m["targetType"] = "workflow" },
			wantErr: `targetType is workflow but targetWorkflowName is empty — targetAgentName is set to "Orchestrator"`,
		},
		{
			name: "target type agent disagrees with the set workflow target",
			mutate: func(m map[string]any) {
				m["targetType"] = "agent"
				delete(m, "targetAgentName")
				m["targetWorkflowName"] = "OrderIntake"
			},
			wantErr: `targetType is agent but targetAgentName is empty — targetWorkflowName is set to "OrderIntake"`,
		},
		{name: "bad target type enum", mutate: func(m map[string]any) { m["targetType"] = "peer" }, wantErr: "targetType"},
		{
			name: "target mutex outranks a bad target type enum",
			mutate: func(m map[string]any) {
				m["targetType"] = "peer"
				m["targetWorkflowName"] = "OrderIntake"
			},
			wantErr: "mutually exclusive",
		},
		{name: "missing route path", mutate: func(m map[string]any) { delete(m, "routePath") }, wantErr: "routePath"},
		{name: "missing token", mutate: func(m map[string]any) { delete(m, "authToken") }, wantErr: "authToken"},
		{name: "short token", mutate: func(m map[string]any) { m["authToken"] = "short" }, wantErr: "authToken"},
		{name: "bad method enum", mutate: func(m map[string]any) { m["method"] = "DELETE" }, wantErr: "method"},
		{name: "default token location needs no name", mutate: func(m map[string]any) { m["authTokenLocation"] = "bearer" }},
		{
			name:    "header token location requires a name",
			mutate:  func(m map[string]any) { m["authTokenLocation"] = "header" },
			wantErr: "authTokenName",
		},
		{
			name:    "query token location requires a name",
			mutate:  func(m map[string]any) { m["authTokenLocation"] = "query" },
			wantErr: "authTokenName",
		},
		{
			name: "header token location with a name",
			mutate: func(m map[string]any) {
				m["authTokenLocation"] = "header"
				m["authTokenName"] = "X-Webhook-Token"
			},
		},
		{
			name:    "bad token location enum",
			mutate:  func(m map[string]any) { m["authTokenLocation"] = "cookie" },
			wantErr: "authTokenLocation",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := validWebhookValues()
			if tt.mutate != nil {
				tt.mutate(v)
			}
			err := validateWebhookValues(v)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
