// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"regexp"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/analytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type activityFeedbackScope struct {
	Vis          store.FeedbackVisibility
	ForcedUserID string
}

func resolveActivityFeedbackScope(identity *auth.Identity, callerUserID string, isActivityAdmin bool) activityFeedbackScope {
	everything := store.FeedbackVisibility{
		Agents:    store.SubjectScope{All: true},
		Workflows: store.SubjectScope{All: true},
	}
	if isActivityAdmin {
		return activityFeedbackScope{Vis: everything}
	}
	if identity != nil && len(identity.Scopes) > 0 {
		reach := rbac.ResolveFeedbackReach(identity.Scopes)
		if hasFeedbackReadGrant(reach) {
			return activityFeedbackScope{Vis: store.FeedbackVisibility{
				Agents:    store.SubjectScope{All: reach.AgentRead.All, IDs: reach.AgentRead.IDs},
				Workflows: store.SubjectScope{All: reach.WorkflowRead.All, IDs: reach.WorkflowRead.IDs},
			}}
		}
	}
	if callerUserID == "" {
		return activityFeedbackScope{}
	}
	return activityFeedbackScope{Vis: everything, ForcedUserID: callerUserID}
}

func hasFeedbackReadGrant(reach rbac.FeedbackReach) bool {
	return !reach.AgentRead.IsEmpty() || !reach.WorkflowRead.IsEmpty()
}

type subjectMatch struct {
	Agents     []string
	Workflows  []string
	Candidates []analytics.AgentRef
	Unmatched  []string
	Ambiguous  []string
	Guessed    []string
}

type registrySubject struct {
	ref  analytics.AgentRef
	kind a2a.ComponentKind
}

func (e *GatewayExecutor) resolveSubjectNames(names []string, vis store.FeedbackVisibility) subjectMatch {
	var out subjectMatch
	subjects := e.reachableSubjects(vis)
	for _, name := range names {
		matched := matchSubjects(subjects, name)
		if len(matched) == 0 {
			before := len(out.Agents) + len(out.Workflows)
			var guessed bool
			out.Agents, out.Workflows, guessed = appendIDWithoutCard(out.Agents, out.Workflows, name, vis)
			switch {
			case len(out.Agents)+len(out.Workflows) == before:
				out.Unmatched = append(out.Unmatched, name)
			case guessed:
				out.Guessed = append(out.Guessed, name)
			}
			continue
		}
		if len(matched) > 1 {
			out.Ambiguous = append(out.Ambiguous, name)
			for _, s := range matched {
				if !slices.Contains(out.Candidates, s.ref) {
					out.Candidates = append(out.Candidates, s.ref)
				}
			}
			continue
		}
		for _, s := range matched {
			if s.kind == a2a.KindWorkflow {
				out.Workflows = appendUnique(out.Workflows, s.ref.ID)
			} else {
				out.Agents = appendUnique(out.Agents, s.ref.ID)
			}
		}
	}
	return out
}

var subjectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func appendIDWithoutCard(agents, workflows []string, name string, vis store.FeedbackVisibility) (_, _ []string, guessed bool) {
	idShaped := subjectIDPattern.MatchString(name)
	agentListed, workflowListed := slices.Contains(vis.Agents.IDs, name), slices.Contains(vis.Workflows.IDs, name)
	if agentListed || vis.Agents.All && idShaped {
		agents = appendUnique(agents, name)
	}
	if workflowListed || vis.Workflows.All && idShaped {
		workflows = appendUnique(workflows, name)
	}
	return agents, workflows, !agentListed && !workflowListed && idShaped && (vis.Agents.All || vis.Workflows.All)
}

func matchSubjects(subjects []registrySubject, name string) []registrySubject {
	var matched []registrySubject
	for _, s := range subjects {
		if s.ref.ID == name || strings.EqualFold(s.ref.Name, name) {
			matched = append(matched, s)
		}
	}
	return matched
}

func appendUnique(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

func (e *GatewayExecutor) reachableSubjects(vis store.FeedbackVisibility) []registrySubject {
	reg := e.agentRegistry()
	if reg == nil {
		return nil
	}
	var out []registrySubject
	for _, card := range reg.List() {
		id, kind, ok := a2a.IDFromInstanceName(card.Name)
		if !ok {
			id = card.Name
			kind = a2a.KindAgent
			if reg.HandlerTypeFromName(card.Name) == store.TaskHandlerTypeWorkflow {
				kind = a2a.KindWorkflow
			}
		}
		scope := vis.Agents
		if kind == a2a.KindWorkflow {
			scope = vis.Workflows
		}
		if !scope.All && !slices.Contains(scope.IDs, id) {
			continue
		}
		out = append(out, registrySubject{
			ref:  analytics.AgentRef{ID: id, Name: platpkg.DisplayName(&card), Type: string(kind)},
			kind: kind,
		})
	}
	return out
}

func (e *GatewayExecutor) agentRegistry() *AgentRegistry {
	for _, core := range e.Gateways() {
		if core != nil && core.AgentRegistry() != nil {
			return core.AgentRegistry()
		}
	}
	return nil
}
