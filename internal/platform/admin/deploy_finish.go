// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func finishWith(l *deployLeader, agents *DeploymentService, workflows *WorkflowDeploymentService, entrypoints *GatewayDeploymentService) {
	if agents != nil {
		l.finishers[store.IntentKindAgent] = func(ctx context.Context, payload []byte) error {
			var intent agentIntent
			if err := json.Unmarshal(payload, &intent); err != nil {
				return err
			}
			d := intent.Deployment
			if recorded, err := agents.deploymentRepo.GetByID(ctx, d.ID); err != nil || recorded != nil {
				return err
			}
			agent, _, _, _, _, err := agents.agentRepo.GetByID(ctx, d.AgentID)
			if err != nil {
				return err
			}
			if agent == nil {
				return agents.finishUndeployOfDeleted(ctx, d, intent.Instance)
			}
			deployer, err := agents.deployers.LookupOrError(agent.Type)
			if err != nil {
				return err
			}
			_, err = agents.sendAndRecord(ctx, &d, deployer, agent, intent.Payloads)
			return err
		}
	}
	if workflows != nil {
		l.finishers[store.IntentKindWorkflow] = func(ctx context.Context, payload []byte) error {
			var intent workflowIntent
			if err := json.Unmarshal(payload, &intent); err != nil {
				return err
			}
			d := intent.Deployment
			if recorded, err := workflows.deploymentRepo.GetByID(ctx, d.ID); err != nil || recorded != nil {
				return err
			}
			workflow, err := workflows.workflowRepo.GetByID(ctx, d.WorkflowID)
			if err != nil {
				return err
			}
			if workflow == nil {
				return workflows.finishUndeployOfDeleted(ctx, d)
			}
			restored, deployErr := workflows.sendToAWE(ctx, workflow, &d, intent.YAML)
			_, err = workflows.recordDeployment(ctx, CreateWorkflowDeploymentRequest{WorkflowID: d.WorkflowID, Action: d.Action}, workflow, &d, restored, deployErr)
			return err
		}
	}
	if entrypoints != nil {
		l.finishers[store.IntentKindEntrypoint] = func(ctx context.Context, payload []byte) error {
			var intent entrypointIntent
			if err := json.Unmarshal(payload, &intent); err != nil {
				return err
			}
			d := intent.Deployment
			if recorded, err := entrypoints.deploymentRepo.GetByID(ctx, d.ID); err != nil || recorded != nil {
				return err
			}
			gw, err := entrypoints.gatewayRepo.GetByID(ctx, d.GatewayID)
			if err != nil || gw == nil {
				return err
			}
			return entrypoints.recordDeployment(ctx, &d, entrypoints.rollOutEntrypoint(ctx, &d, gatewayWireID(gw), intent.Payloads))
		}
	}
}

func (l *deployLeader) finishLeftOperations(ctx context.Context) {
	if l == nil || l.intents == nil {
		return
	}
	for _, kind := range requestKinds {
		if _, ok := l.finishers[kind]; ok && !l.finishKind(ctx, kind) {
			return
		}
	}
}

func (l *deployLeader) finishKind(ctx context.Context, kind string) bool {
	intents, err := l.intents.List(ctx, kind)
	if err != nil {
		l.logger.Warn("deployments in flight not read; the next leader term finishes them", "kind", kind, "error", err)
		return ctx.Err() == nil
	}
	newest := map[string]string{}
	for _, intent := range intents {
		newest[intent.ObjectID] = intent.ID
	}
	for _, intent := range intents {
		if newest[intent.ObjectID] != intent.ID {
			l.end(ctx, intent.ID)
			continue
		}
		if _, err := l.finishOne(ctx, kind, intent.ObjectID, intent.ID); err != nil {
			return false
		}
	}
	return true
}

func (l *deployLeader) finishOne(ctx context.Context, kind, objectID, id string) (bool, error) {
	finish, ok := l.finishers[kind]
	if !ok {
		return false, nil
	}
	release, err := l.lock(ctx, objectKey{kind, objectID})
	if err != nil {
		return false, err
	}
	defer release()
	current, err := l.intents.For(ctx, kind, objectID)
	if err != nil {
		l.logger.Warn("deployment in flight not read again; it is retried", "kind", kind, "deploymentID", id, "error", err)
		return false, nil
	}
	if current == nil || current.ID != id {
		l.end(ctx, id)
		return true, nil
	}
	l.logger.Info("finishing a deployment its leader did not record", "kind", kind, "deploymentID", id)
	if err := finish(ctx, []byte(current.Payload)); err != nil && !errors.Is(err, errRecorded) {
		l.logger.Warn("deployment in flight not finished; it is retried", "kind", kind, "deploymentID", id, "error", err)
		return false, nil
	}
	l.end(ctx, id)
	return true, nil
}
