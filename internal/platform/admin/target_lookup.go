// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TargetLookup(db func() *store.DB, logger *slog.Logger) func(ctx context.Context, name string) control.Target {
	return func(ctx context.Context, name string) control.Target {
		target, err := LookupTarget(ctx, db(), name)
		if err != nil {
			logger.Warn("task target lookup failed; the task is sent to its name", "target", name, "error", err)
			return control.Target{State: control.TargetUnknown}
		}
		return target
	}
}

func LookupTarget(ctx context.Context, db *store.DB, name string) (control.Target, error) {
	if db == nil || name == "" {
		return control.Target{State: control.TargetUnknown}, nil
	}
	id, err := store.NewAgentRepo(db).IDByName(ctx, name)
	if err != nil {
		return control.Target{}, err
	}
	kind := a2a.KindAgent
	if id == "" {
		if id, err = store.NewWorkflowRepo(db).IDByName(ctx, name); err != nil {
			return control.Target{}, err
		}
		kind = a2a.KindWorkflow
	}
	if id == "" {
		return control.Target{State: control.TargetUnknown}, nil
	}
	cardName := a2a.InstanceName(kind, id)
	undeploying, err := undeployInFlight(ctx, db, kind, id)
	if err != nil {
		return control.Target{}, err
	}
	if undeploying {
		return control.Target{State: control.TargetNotDeployed, CardName: cardName}, nil
	}
	action, status, found, err := latestDeploymentRow(ctx, db, kind, id)
	if err != nil {
		return control.Target{}, err
	}
	return control.Target{State: deploymentTargetState(action, status, found), CardName: cardName}, nil
}

func deploymentTargetState(action, status string, found bool) control.TargetState {
	switch {
	case !found:
		return control.TargetNotDeployed
	case action == DeployActionUndeploy:
		if store.MayHaveLiveInstance(status, action) {
			return control.TargetDeployed
		}
		return control.TargetNotDeployed
	case store.MapDeploymentDisplayStatus(status, action) == DisplayStatusDeployed:
		return control.TargetDeployed
	default:
		return control.TargetNotDeployed
	}
}

func undeployInFlight(ctx context.Context, db *store.DB, kind a2a.ComponentKind, id string) (bool, error) {
	intentKind := store.IntentKindAgent
	if kind == a2a.KindWorkflow {
		intentKind = store.IntentKindWorkflow
	}
	intent, err := store.NewDeploymentIntentRepo(db).For(ctx, intentKind, id)
	if err != nil || intent == nil {
		return false, err
	}
	var action string
	if kind == a2a.KindWorkflow {
		var pending workflowIntent
		if json.Unmarshal([]byte(intent.Payload), &pending) != nil {
			return false, nil
		}
		action = pending.Deployment.Action
	} else {
		var pending agentIntent
		if json.Unmarshal([]byte(intent.Payload), &pending) != nil {
			return false, nil
		}
		action = pending.Deployment.Action
	}
	return action == DeployActionUndeploy, nil
}

func latestDeploymentRow(ctx context.Context, db *store.DB, kind a2a.ComponentKind, id string) (action, status string, found bool, err error) {
	if kind == a2a.KindWorkflow {
		wf, wfErr := store.NewWorkflowDeploymentRepo(db).GetLatestByWorkflowID(ctx, id)
		if wfErr != nil || wf == nil {
			return "", "", false, wfErr
		}
		return wf.Action, wf.Status, true, nil
	}
	dep, depErr := store.NewAgentDeploymentRepo(db).GetLatestByAgentID(ctx, id)
	if depErr != nil || dep == nil {
		return "", "", false, depErr
	}
	return dep.Action, dep.Status, true, nil
}
