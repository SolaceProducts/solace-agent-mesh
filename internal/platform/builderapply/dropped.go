// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/generic"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var droppableKinds = []string{kindAgent, kindWorkflow}

func isDroppableKind(componentKind string) bool {
	return slices.Contains(droppableKinds, componentKind)
}

func pairedResourceIDs(plans []kindPlan) map[string]map[string]struct{} {
	paired := map[string]map[string]struct{}{}
	for _, kp := range plans {
		if !isDroppableKind(kp.componentKind) {
			continue
		}
		ids := paired[kp.componentKind]
		if ids == nil {
			ids = map[string]struct{}{}
			paired[kp.componentKind] = ids
		}
		for _, ops := range [][]generic.Op{kp.plan.Creates, kp.plan.Updates, kp.plan.Unchanged} {
			for _, op := range ops {
				if op.ID != "" {
					ids[strings.ToLower(op.ID)] = struct{}{}
				}
			}
		}
	}
	return paired
}

func (s *Service) droppedFromPlan(ctx context.Context, req ApplyBuildRequest, identity *auth.Subject, plans []kindPlan) ([]DroppedResource, bool) {
	dropped := []DroppedResource{}
	if req.BuilderSessionID == "" {
		return dropped, false
	}
	if s.deps.Ledger == nil {
		return dropped, true
	}
	written, err := s.deps.Ledger.ListForSession(ctx, actorFrom(identity), req.BuilderSessionID)
	if err != nil {
		s.logger.Warn("builder dropped list: read session record failed", "error", err)
		return dropped, true
	}

	paired := pairedResourceIDs(plans)
	candidates := map[string][]store.BuilderSessionResource{}
	for _, w := range written {
		if isDroppableKind(w.Kind) && !hasID(paired[w.Kind], w.ResourceID) {
			candidates[w.Kind] = append(candidates[w.Kind], w)
		}
	}

	incomplete := false
	for _, componentKind := range droppableKinds {
		if len(candidates[componentKind]) == 0 {
			continue
		}
		listed, ok := s.droppedOfKind(ctx, componentKind, candidates[componentKind], identity, req.DryRun)
		dropped = append(dropped, listed...)
		incomplete = incomplete || !ok
	}
	slices.SortFunc(dropped, func(a, b DroppedResource) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), cmp.Compare(a.ID, b.ID))
	})
	return dropped, incomplete
}

func hasID(ids map[string]struct{}, id string) bool {
	_, ok := ids[strings.ToLower(id)]
	return ok
}

func (s *Service) droppedOfKind(ctx context.Context, componentKind string, candidates []store.BuilderSessionResource, identity *auth.Subject, dryRun bool) ([]DroppedResource, bool) {
	readScope, _ := readScopeForKind(componentKind)
	if identity == nil || !identity.HasScope(readScope) {
		return nil, false
	}
	names, err := s.storedNames(ctx, componentKind)
	if err != nil {
		s.logger.Warn("builder dropped list: read stored resources failed", "resourceKind", componentKind, "error", err)
		return nil, false
	}
	var out []DroppedResource
	complete := true
	for _, c := range candidates {
		name, exists := names[strings.ToLower(c.ResourceID)]
		if !exists {
			read, readErr := s.nameOf(ctx, RemovalTarget{Kind: componentKind, ID: c.ResourceID})
			switch {
			case readErr == nil:
				name = read
			case isNotFound(readErr):
				if !dryRun {
					s.forget(ctx, componentKind, c.ResourceID)
				}
				continue
			default:
				s.logger.Warn("builder dropped list: read a resource by id failed", "resourceKind", componentKind, "resourceId", c.ResourceID, "error", readErr)
				complete = false
				continue
			}
		}
		state, err := s.deploymentOf(ctx, componentKind, c.ResourceID)
		if err != nil {
			s.logger.Warn("builder dropped list: read deployment failed", "resourceKind", componentKind, "error", err)
			complete = false
			continue
		}
		out = append(out, DroppedResource{Kind: componentKind, ID: c.ResourceID, Name: name, CreatedBySession: c.CreatedBySession, Deployment: state})
	}
	return out, complete
}

func (s *Service) storedNames(ctx context.Context, componentKind string) (map[string]string, error) {
	names := map[string]string{}
	switch componentKind {
	case kindAgent:
		list, err := s.deps.listAgents(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range list {
			names[strings.ToLower(a.ID)] = a.Name
		}
	case kindWorkflow:
		list, err := s.deps.listWorkflows(ctx)
		if err != nil {
			return nil, err
		}
		for _, w := range list {
			names[strings.ToLower(w.ID)] = w.Name
		}
	default:
		return nil, fmt.Errorf("no reader for component kind %q", componentKind)
	}
	return names, nil
}

func (s *Service) deploymentOf(ctx context.Context, componentKind, id string) (string, error) {
	var status, action string
	switch componentKind {
	case kindAgent:
		if s.deps.AgentDeployments == nil {
			return "", fmt.Errorf("agent deployments are not readable")
		}
		d, err := s.deps.AgentDeployments.GetLatestByAgentID(ctx, id)
		if err != nil {
			return "", err
		}
		if d != nil {
			status, action = d.Status, d.Action
		}
	case kindWorkflow:
		if s.deps.WorkflowDeployments == nil {
			return "", fmt.Errorf("workflow deployments are not readable")
		}
		d, err := s.deps.WorkflowDeployments.GetLatestByWorkflowID(ctx, id)
		if err != nil {
			return "", err
		}
		if d != nil {
			status, action = d.Status, d.Action
		}
	default:
		return "", fmt.Errorf("no deployment record for component kind %q", componentKind)
	}
	return deploymentState(status, action), nil
}

func deploymentState(status, action string) string {
	switch {
	case action == "undeploy" && status == "failed":
		return deploymentUndeployFailed
	case (action == "deploy" || action == "update") && status == "success":
		return deploymentDeployed
	case (action == "deploy" || action == "update") && status == "failed":
		return deploymentDeployFailed
	default:
		return deploymentNotDeployed
	}
}

func mayBeRunning(state string) bool {
	return state != deploymentNotDeployed
}

func (s *Service) recordWritten(ctx context.Context, builderSessionID, actor string, operations []BuildOperationResult) bool {
	if builderSessionID == "" {
		return true
	}
	var written []store.BuilderSessionResource
	for _, op := range operations {
		if op.Status != statusSucceeded || op.ResourceID == "" || !isDroppableKind(op.Kind) {
			continue
		}
		if op.Action != actionCreate && op.Action != actionUpdate {
			continue
		}
		written = append(written, store.BuilderSessionResource{
			Actor: actor, SessionID: builderSessionID, Kind: op.Kind, ResourceID: op.ResourceID,
			CreatedBySession: op.Action == actionCreate,
		})
	}
	if len(written) == 0 {
		return true
	}
	if s.deps.Ledger == nil {
		s.logger.Warn("builder apply: no session record to write the resources to", "builderSessionId", builderSessionID, "resourceIds", resourceIDs(written))
		return false
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	if err := s.deps.Ledger.Record(recordCtx, written); err != nil {
		s.logger.Warn("builder apply: could not record the resources written", "builderSessionId", builderSessionID, "resourceIds", resourceIDs(written), "error", err)
		return false
	}
	return true
}

func resourceIDs(written []store.BuilderSessionResource) []string {
	ids := make([]string, len(written))
	for i, w := range written {
		ids[i] = w.Kind + "/" + w.ResourceID
	}
	return ids
}

const recordTimeout = 10 * time.Second

func (s *Service) forget(ctx context.Context, componentKind, id string) {
	if s.deps.Ledger == nil {
		return
	}
	if err := s.deps.Ledger.Forget(ctx, componentKind, id); err != nil {
		s.logger.Warn("builder session record: could not forget a removed resource", "resourceKind", componentKind, "error", err)
	}
}
