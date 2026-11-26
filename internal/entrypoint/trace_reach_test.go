// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestTraceRowsCovered(t *testing.T) {
	const idA, idB = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	reach := rbac.ResolveFeedbackReach([]string{rbac.AgentReadTraceScope(idA)})

	handler := func(kind a2a.ComponentKind, handlerType, id string) store.TaskHandler {
		return store.TaskHandler{HandlerName: a2a.InstanceName(kind, id), HandlerType: handlerType}
	}
	agent := func(id string) store.TaskHandler {
		return handler(a2a.KindAgent, store.TaskHandlerTypeAgent, id)
	}
	rootOn := func(h store.TaskHandler) *store.Task {
		name, handlerType := h.HandlerName, h.HandlerType
		return &store.Task{ID: "root", HandlerName: &name, HandlerType: &handlerType}
	}
	parentID := "parent"
	childOfA := rootOn(agent(idA))
	childOfA.ParentTaskID = &parentID

	tests := []struct {
		name     string
		root     *store.Task
		handlers []store.TaskHandler
		want     string
	}{
		{"covered root and participant", rootOn(agent(idA)), []store.TaskHandler{agent(idA)}, ""},
		{"root outside reach", rootOn(agent(idB)), []store.TaskHandler{agent(idB)}, TraceDenySubjectNotInReach},
		{"root still carrying a parent id", childOfA, []store.TaskHandler{agent(idA)}, TraceDenySubjectNotInReach},
		{
			"workflow root",
			rootOn(handler(a2a.KindWorkflow, store.TaskHandlerTypeWorkflow, idA)),
			[]store.TaskHandler{agent(idA)},
			TraceDenySubjectNotInReach,
		},
		{"participant outside reach", rootOn(agent(idA)), []store.TaskHandler{agent(idA), agent(idB)}, TraceDenyParticipantUncovered},
		{"unclassified participant", rootOn(agent(idA)), []store.TaskHandler{agent(idA), {}}, TraceDenyParticipantUncovered},
		{"no participants is never vacuously open", rootOn(agent(idA)), nil, TraceDenyParticipantUncovered},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, traceRowsCovered(tc.root, tc.handlers, reach))
		})
	}
}
