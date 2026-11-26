// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

const (
	SubjectKindAgent    = "agent"
	SubjectKindWorkflow = "workflow"
)

func AgentReadFeedbackScope(id string) string { return SubjectKindAgent + ":" + id + ":read_feedback" }

func WorkflowReadFeedbackScope(id string) string {
	return SubjectKindWorkflow + ":" + id + ":read_feedback"
}

func AgentReadTraceScope(id string) string { return SubjectKindAgent + ":" + id + ":read_trace" }

type SubjectReach struct {
	All bool
	IDs []string
}

func (s SubjectReach) IsEmpty() bool { return !s.All && len(s.IDs) == 0 }

func (s SubjectReach) covers(id string) bool {
	if s.All {
		return true
	}
	for _, have := range s.IDs {
		if have == id {
			return true
		}
	}
	return false
}

type FeedbackReach struct {
	AgentRead    SubjectReach
	WorkflowRead SubjectReach
	AgentTrace   SubjectReach
}

func (r FeedbackReach) ReadsSubject(kind, id string) bool {
	switch kind {
	case SubjectKindAgent:
		return r.AgentRead.covers(id)
	case SubjectKindWorkflow:
		return r.WorkflowRead.covers(id)
	}
	return false
}

func (r FeedbackReach) TracesAgent(id string) bool { return r.AgentTrace.covers(id) }

type reachSlot struct {
	reach *SubjectReach
	kind  string
	anyOf string
	perID func(string) string
	seen  map[string]bool
}

func ResolveFeedbackReach(granted []string) FeedbackReach {
	var reach FeedbackReach
	slots := []reachSlot{
		{&reach.AgentRead, SubjectKindAgent, ScopeAgentReadFeedbackAny, AgentReadFeedbackScope, map[string]bool{}},
		{&reach.WorkflowRead, SubjectKindWorkflow, ScopeWorkflowReadFeedbackAny, WorkflowReadFeedbackScope, map[string]bool{}},
		{&reach.AgentTrace, SubjectKindAgent, ScopeAgentReadTraceAny, AgentReadTraceScope, map[string]bool{}},
	}
	for _, g := range granted {
		if ScopeHasPartialGlob(g) {
			continue
		}
		matchedWildcard := false
		for i := range slots {
			if auth.MatchScope(g, slots[i].anyOf) {
				slots[i].reach.All = true
				slots[i].reach.IDs = nil
				matchedWildcard = true
			}
		}
		if matchedWildcard {
			continue
		}
		parts := strings.Split(g, ":")
		if len(parts) != 3 {
			continue
		}
		id := parts[1]
		if id == "" || id == "_" {
			continue
		}
		for i := range slots {
			s := &slots[i]
			if parts[0] != s.kind || s.reach.All || s.seen[id] || !auth.MatchScope(g, s.perID(id)) {
				continue
			}
			s.seen[id] = true
			s.reach.IDs = append(s.reach.IDs, id)
		}
	}
	return reach
}
