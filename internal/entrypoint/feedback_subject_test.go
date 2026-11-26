// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestSubjectKindsMatchFeedbackSubjectTypes(t *testing.T) {
	if rbac.SubjectKindAgent != store.SubjectTypeAgent {
		t.Errorf("rbac.SubjectKindAgent = %q, store.SubjectTypeAgent = %q", rbac.SubjectKindAgent, store.SubjectTypeAgent)
	}
	if rbac.SubjectKindWorkflow != store.SubjectTypeWorkflow {
		t.Errorf("rbac.SubjectKindWorkflow = %q, store.SubjectTypeWorkflow = %q", rbac.SubjectKindWorkflow, store.SubjectTypeWorkflow)
	}
}

func TestDeriveFeedbackSubject(t *testing.T) {
	name := func(s string) *string { return &s }
	task := func(handlerName, handlerType string) *store.Task {
		return &store.Task{HandlerName: name(handlerName), HandlerType: name(handlerType)}
	}

	tests := []struct {
		desc   string
		task   *store.Task
		want   FeedbackSubject
		wantOK bool
	}{
		{
			desc:   "platform agent card normalizes to dashed uuid",
			task:   task("agent_019e47f4_1234_7abc_8def_0123456789ab", store.TaskHandlerTypeAgent),
			want:   FeedbackSubject{ID: "019e47f4-1234-7abc-8def-0123456789ab", Type: store.SubjectTypeAgent},
			wantOK: true,
		},
		{
			desc:   "workflow card normalizes to dashed uuid",
			task:   task("workflow_019e47f4_1234_7abc_8def_0123456789ab", store.TaskHandlerTypeWorkflow),
			want:   FeedbackSubject{ID: "019e47f4-1234-7abc-8def-0123456789ab", Type: store.SubjectTypeWorkflow},
			wantOK: true,
		},
		{
			desc:   "plain built-in name is its own subject id",
			task:   task("OrchestratorAgent", store.TaskHandlerTypeAgent),
			want:   FeedbackSubject{ID: "OrchestratorAgent", Type: store.SubjectTypeAgent},
			wantOK: true,
		},
		{
			desc:   "plain-named workflow classifies as a workflow",
			task:   task("order_fulfillment", store.TaskHandlerTypeWorkflow),
			want:   FeedbackSubject{ID: "order_fulfillment", Type: store.SubjectTypeWorkflow},
			wantOK: true,
		},
		{
			desc:   "prefixed but not a uuid stays a plain agent name",
			task:   task("agent_not-a-uuid", store.TaskHandlerTypeAgent),
			want:   FeedbackSubject{ID: "agent_not-a-uuid", Type: store.SubjectTypeAgent},
			wantOK: true,
		},
		{desc: "nil task", task: nil, wantOK: false},
		{desc: "nil handler columns", task: &store.Task{}, wantOK: false},
		{desc: "empty handler_name", task: task("", store.TaskHandlerTypeAgent), wantOK: false},
		{
			desc:   "unclassified handler_type fails closed",
			task:   task("SomeAgent", ""),
			wantOK: false,
		},
		{
			desc:   "agent_name alone is not enough — handler columns are the source",
			task:   &store.Task{AgentName: name("OrchestratorAgent")},
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, ok := DeriveFeedbackSubject(tt.task)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
