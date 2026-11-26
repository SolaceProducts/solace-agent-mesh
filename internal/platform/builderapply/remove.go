// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var removalScopes = []string{rbac.ScopeAgentBuilderDelete, rbac.ScopeWorkflowBuilderDelete}

func deleteScopeForKind(componentKind string) (string, bool) {
	switch componentKind {
	case kindAgent:
		return rbac.ScopeAgentBuilderDelete, true
	case kindWorkflow:
		return rbac.ScopeWorkflowBuilderDelete, true
	}
	return "", false
}

func (s *Service) RemoveDropped(ctx context.Context, req RemoveDroppedRequest, identity *auth.Subject) *RemoveDroppedResponse {
	resp := &RemoveDroppedResponse{Results: make([]RemovalResult, 0, len(req.Resources))}
	actor := actorFrom(identity)
	created, recordErr := s.createdBySession(ctx, actor, req.BuilderSessionID)
	if recordErr != nil {
		s.logger.Warn("builder removal: could not read the session record", "error", recordErr)
	}

	seen := map[string]struct{}{}
	for _, target := range req.Resources {
		target.ID = strings.ToLower(target.ID)
		key := target.Kind + "/" + target.ID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		result := s.removeOne(ctx, target, identity, actor, created, recordErr)
		resp.Results = append(resp.Results, result)
		if result.Status == removalRemoved {
			s.logger.Info("builder removed a resource the plan dropped", "resourceKind", result.Kind, "resourceId", result.ID, "builderSessionId", req.BuilderSessionID)
		}
	}
	return resp
}

func (s *Service) createdBySession(ctx context.Context, actor, builderSessionID string) (map[string]struct{}, error) {
	if s.deps.Ledger == nil {
		return nil, errors.New("the Builder session record is not available")
	}
	written, err := s.deps.Ledger.ListForSession(ctx, actor, builderSessionID)
	if err != nil {
		return nil, err
	}
	created := map[string]struct{}{}
	for _, w := range written {
		if w.CreatedBySession {
			created[w.Kind+"/"+strings.ToLower(w.ResourceID)] = struct{}{}
		}
	}
	return created, nil
}

func (s *Service) removeOne(ctx context.Context, target RemovalTarget, identity *auth.Subject, actor string, created map[string]struct{}, recordErr error) RemovalResult {
	result := RemovalResult{Kind: target.Kind, ID: target.ID}
	refuse := func(message string) RemovalResult {
		result.Status = removalRefused
		result.Error = &OperationError{Message: message, ErrorID: idgen.New(), HTTPStatus: http.StatusForbidden}
		s.logger.Warn("builder removal refused", "resourceKind", target.Kind, "errorId", result.Error.ErrorID)
		return result
	}

	scope, known := deleteScopeForKind(target.Kind)
	if !known {
		return refuse("only agents and workflows can be removed from the Builder")
	}
	if identity == nil || !identity.HasScope(scope) {
		return refuse("Insufficient permissions: removing this requires " + scope)
	}
	if recordErr != nil {
		return s.failRemoval(result, &entplatform.UnavailableError{Message: "the Builder could not check which resources this session created, so nothing was removed; try again", Cause: recordErr})
	}
	if _, ok := created[target.Kind+"/"+target.ID]; !ok {
		return refuse("this " + target.Kind + " was not created by this Builder session, so the Builder does not remove it; remove it from the " + pageFor(target.Kind) + " page")
	}

	name, err := s.nameOf(ctx, target)
	result.Name = name
	if isNotFound(err) {
		s.forget(ctx, target.Kind, target.ID)
		result.Status = removalNotFound
		return result
	}
	if err != nil {
		return s.failRemoval(result, err)
	}

	if err := s.deleteAfterUndeploy(ctx, target, actor); err != nil {
		if isNotFound(err) {
			s.forget(ctx, target.Kind, target.ID)
			result.Status = removalNotFound
			return result
		}
		return s.failRemoval(result, err)
	}

	s.forget(ctx, target.Kind, target.ID)
	if target.Kind == kindAgent {
		s.analytics.AgentDeleted(ctx)
	}
	result.Status = removalRemoved
	return result
}

func pageFor(componentKind string) string {
	if componentKind == kindWorkflow {
		return "Workflows"
	}
	return "Agents"
}

func isNotFound(err error) bool {
	_, ok := errors.AsType[*entplatform.NotFoundError](err)
	return ok
}

func (s *Service) failRemoval(result RemovalResult, err error) RemovalResult {
	result.Status = removalFailed
	result.Error = s.operationError(err, BuildOperationResult{Kind: result.Kind, Name: result.Name, Action: "remove"}, false)
	return result
}

func (s *Service) nameOf(ctx context.Context, target RemovalTarget) (string, error) {
	switch target.Kind {
	case kindAgent:
		a, err := s.deps.Agents.GetByID(ctx, target.ID)
		if err != nil {
			return "", err
		}
		return a.Name, nil
	case kindWorkflow:
		w, err := s.deps.Workflows.GetByID(ctx, target.ID)
		if err != nil {
			return "", err
		}
		return w.Name, nil
	}
	return "", fmt.Errorf("no reader for component kind %q", target.Kind)
}

func (s *Service) deleteAfterUndeploy(ctx context.Context, target RemovalTarget, actor string) error {
	userID := cmpUser(entplatform.UserIDFromContext(ctx), actor)
	switch target.Kind {
	case kindAgent:
		if s.deps.AgentDeploy == nil {
			if err := s.refuseWhileRunning(ctx, target); err != nil {
				return err
			}
		}
		return s.deps.Agents.DeleteWithUndeploy(ctx, target.ID, userID, s.deps.AgentDeploy)
	case kindWorkflow:
		if s.deps.WorkflowDeploy == nil {
			if err := s.refuseWhileRunning(ctx, target); err != nil {
				return err
			}
		}
		return s.deps.Workflows.DeleteWithUndeploy(ctx, target.ID, userID, s.deps.WorkflowDeploy)
	}
	return fmt.Errorf("no deleter for component kind %q", target.Kind)
}

func cmpUser(fromContext, actor string) string {
	if fromContext != "" {
		return fromContext
	}
	return actor
}

func (s *Service) refuseWhileRunning(ctx context.Context, target RemovalTarget) error {
	state, err := s.deploymentOf(ctx, target.Kind, target.ID)
	if err != nil {
		return err
	}
	if mayBeRunning(state) {
		return &entplatform.UnavailableError{Message: "this " + target.Kind + " may still be running and this deployment cannot undeploy it, so it was not removed"}
	}
	return nil
}
