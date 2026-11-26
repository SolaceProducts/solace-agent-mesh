// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"fmt"
	"slices"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func scopesForKind(componentKind string) (create, update string, ok bool) {
	switch componentKind {
	case kindConnector:
		return rbac.ScopeConnectorCreate, rbac.ScopeConnectorUpdate, true
	case kindAgent, kindA2AAgent:
		return rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderUpdate, true
	case kindWorkflow:
		return rbac.ScopeWorkflowBuilderCreate, rbac.ScopeWorkflowBuilderUpdate, true
	case kindGateway:
		return rbac.ScopeEntrypointCreate, rbac.ScopeEntrypointUpdate, true
	}
	return "", "", false
}

var buildEntryScopes = []string{
	rbac.ScopeAgentBuilderCreate, rbac.ScopeAgentBuilderUpdate,
	rbac.ScopeWorkflowBuilderCreate, rbac.ScopeWorkflowBuilderUpdate,
	rbac.ScopeConnectorCreate, rbac.ScopeConnectorUpdate,
	rbac.ScopeEntrypointCreate, rbac.ScopeEntrypointUpdate,
}

func readScopeForKind(componentKind string) (string, bool) {
	switch componentKind {
	case kindConnector:
		return rbac.ScopeConnectorRead, true
	case kindSkill:
		return rbac.ScopeSkillRead, true
	case kindAgent, kindA2AAgent:
		return rbac.ScopeAgentBuilderRead, true
	case kindWorkflow:
		return rbac.ScopeWorkflowBuilderRead, true
	case kindGateway:
		return rbac.ScopeEntrypointRead, true
	}
	return "", false
}

func authorizeReads(identity *auth.Subject, req ApplyBuildRequest) error {
	if identity == nil {
		return fmt.Errorf("authentication required")
	}

	needed := map[string]struct{}{
		rbac.ScopeConnectorRead: {},
		rbac.ScopeSkillRead:     {},
	}
	for _, c := range req.Components {
		if scope, ok := readScopeForKind(c.Kind); ok {
			needed[scope] = struct{}{}
		}
	}
	return missingScopeError(identity, needed)
}

func authorizePlan(identity *auth.Subject, plans []kindPlan) error {
	if identity == nil {
		return fmt.Errorf("authentication required")
	}

	needed := map[string]struct{}{}
	for _, kp := range plans {
		create, update, ok := scopesForKind(kp.componentKind)
		if !ok {
			return fmt.Errorf("no scope mapping for component kind %q", kp.componentKind)
		}
		if len(kp.plan.Creates) > 0 {
			needed[create] = struct{}{}
		}
		if len(kp.plan.Updates) > 0 {
			needed[update] = struct{}{}
		}
	}
	return missingScopeError(identity, needed)
}

func canUpdateKind(identity *auth.Subject, componentKind string) bool {
	if identity == nil {
		return false
	}
	_, update, ok := scopesForKind(componentKind)
	return ok && identity.HasScope(update)
}

func missingScopeError(identity *auth.Subject, needed map[string]struct{}) error {
	var missing []string
	for scope := range needed {
		if !identity.HasScope(scope) {
			missing = append(missing, scope)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return &entplatform.ForbiddenError{
		Message: "Insufficient permissions: this build requires " + strings.Join(missing, ", "),
	}
}
