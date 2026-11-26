// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type redeployPayload struct {
	AgentIDs    []string `json:"agentIds"`
	WorkflowIDs []string `json:"workflowIds,omitempty"`
	Name        string   `json:"name,omitempty"`
	UserID      string   `json:"userId"`
}

type RedeployPublisher struct {
	deploySvc         *DeploymentService
	agentRepo         *store.AgentRepo
	workflowDeploySvc *WorkflowDeploymentService
	workflowRepo      *store.WorkflowRepo
	logger            *slog.Logger
}

func NewRedeployPublisher(deploySvc *DeploymentService, agentRepo *store.AgentRepo, logger *slog.Logger) *RedeployPublisher {
	return &RedeployPublisher{deploySvc: deploySvc, agentRepo: agentRepo, logger: logger}
}

func (p *RedeployPublisher) WithWorkflowRedeploy(svc *WorkflowDeploymentService, repo *store.WorkflowRepo) *RedeployPublisher {
	p.workflowDeploySvc = svc
	p.workflowRepo = repo
	return p
}

func (p *RedeployPublisher) PublishOutboxEvent(ctx context.Context, event store.OutboxEvent) error {
	var payload redeployPayload
	if err := json.Unmarshal([]byte(event.Payload), &payload); err != nil {
		return fmt.Errorf("outbox: invalid payload for event %s: %w", event.ID, err)
	}

	result := fanOutRedeploys(ctx, payload.AgentIDs,
		func(ctx context.Context, id string) string {
			s, _ := p.agentRepo.GetDeploymentStatus(ctx, id)
			return s
		},
		func(ctx context.Context, id string) error {
			resp, err := p.deploySvc.Create(ctx, CreateAgentDeploymentRequest{
				AgentID: id, Action: DeployActionUpdate,
			}, payload.UserID)
			if err != nil {
				return err
			}
			if resp.Status == DeployStatusFailed {
				return fmt.Errorf("agent redeploy failed: %s: %w", resp.ErrorMessage, errRedeployRecorded)
			}
			return nil
		},
	)

	for _, fr := range result.Errors {
		p.logger.Log(ctx, redeployFailureLevel(fr.Err), "outbox: redeploy failed", "agentID", fr.AgentID, "event", event.EventType, "error", fr.Err)
	}
	for _, agentID := range result.RedeployedAgents {
		p.logger.Debug("outbox: redeployed agent", "agentID", agentID, "event", event.EventType)
	}

	wfResult := p.redeployWorkflows(ctx, event, payload)

	retryErr := redeployEventError(result, wfResult)
	if retryErr == nil && len(result.Errors)+len(wfResult.Errors) > 0 {
		p.logger.Debug("outbox: redeploy failures absorbed without retry",
			"event", event.EventType,
			"attempted", result.Attempted+wfResult.Attempted,
			"succeeded", result.Succeeded+wfResult.Succeeded,
			"failed", len(result.Errors)+len(wfResult.Errors),
		)
	}
	return retryErr
}

func redeployEventError(agents, workflows fanOutResult) error {
	retryable := make([]error, 0, len(agents.Errors)+len(workflows.Errors))
	for _, fr := range agents.Errors {
		if errors.Is(fr.Err, errRedeployRecorded) {
			continue
		}
		retryable = append(retryable, fmt.Errorf("agent %s: %w", fr.AgentID, fr.Err))
	}
	for _, fr := range workflows.Errors {
		if errors.Is(fr.Err, errRedeployRecorded) {
			continue
		}
		retryable = append(retryable, fmt.Errorf("workflow %s: %w", fr.AgentID, fr.Err))
	}
	if len(retryable) == 0 || agents.Succeeded+workflows.Succeeded > 0 {
		return nil
	}
	return errors.Join(retryable...)
}

var errRedeployRecorded = errors.New("failure recorded on deployment row")

func (p *RedeployPublisher) redeployWorkflows(ctx context.Context, event store.OutboxEvent, payload redeployPayload) fanOutResult {
	if len(payload.WorkflowIDs) == 0 {
		return fanOutResult{}
	}
	if p.workflowDeploySvc == nil || p.workflowRepo == nil {
		p.logger.Debug("outbox: workflow redeploy not wired, skipping", "event", event.EventType, "count", len(payload.WorkflowIDs))
		return fanOutResult{}
	}
	wfResult := fanOutRedeploys(ctx, payload.WorkflowIDs,
		func(ctx context.Context, id string) string {
			s, _ := p.workflowRepo.GetDeploymentStatus(ctx, id)
			return s
		},
		func(ctx context.Context, id string) error {
			resp, err := p.workflowDeploySvc.CreateWhenFree(ctx, CreateWorkflowDeploymentRequest{
				WorkflowID: id, Action: DeployActionUpdate,
			}, payload.UserID)
			if err != nil {
				return err
			}
			if resp.Status == DeployStatusFailed {
				return fmt.Errorf("workflow redeploy failed: %s: %w", resp.ErrorMessage, errRedeployRecorded)
			}
			return nil
		},
	)
	for _, fr := range wfResult.Errors {
		p.logger.Log(ctx, redeployFailureLevel(fr.Err), "outbox: workflow redeploy failed", "workflowID", fr.AgentID, "event", event.EventType, "error", fr.Err)
	}
	for _, workflowID := range wfResult.RedeployedAgents {
		p.logger.Debug("outbox: redeployed workflow", "workflowID", workflowID, "event", event.EventType)
	}
	return wfResult
}

func redeployFailureLevel(err error) slog.Level {
	if errors.Is(err, ErrDiscoveryNotReady) || errors.Is(err, errNoLiveAWE) {
		return slog.LevelWarn
	}
	return slog.LevelError
}

type agentFailure struct {
	AgentID string
	Err     error
}

type fanOutResult struct {
	Attempted        int
	Succeeded        int
	RedeployedAgents []string
	Errors           []agentFailure
}

func fanOutRedeploys(
	ctx context.Context,
	agentIDs []string,
	statusFn func(context.Context, string) string,
	deployFn func(context.Context, string) error,
) fanOutResult {
	var r fanOutResult
	for _, agentID := range agentIDs {
		if statusFn(ctx, agentID) != DisplayStatusDeployed {
			continue
		}
		r.Attempted++
		if err := deployFn(ctx, agentID); err != nil {
			r.Errors = append(r.Errors, agentFailure{AgentID: agentID, Err: err})
			continue
		}
		r.Succeeded++
		r.RedeployedAgents = append(r.RedeployedAgents, agentID)
	}
	return r
}

var _ platpkg.OutboxPublisher = (*RedeployPublisher)(nil)
