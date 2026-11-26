// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { SyncStatus } from "../types";

const DEPLOYMENT_ACTIONS = ["deploy", "undeploy", "update"] as const;
export type DeploymentAction = (typeof DEPLOYMENT_ACTIONS)[number];

const DEPLOYMENT_ACTION_LABELS: Record<string, string> = {
    deploy: "Deploy",
    undeploy: "Undeploy",
    update: "Deploy Updates",
};

export const getDeploymentActionLabel = (action: string): string => {
    return DEPLOYMENT_ACTION_LABELS[action] ?? action;
};

export const isDeployed = (syncStatus?: SyncStatus): boolean => {
    return syncStatus === "in_sync" || syncStatus === "out_of_sync";
};

export const isDeployFailed = (deploymentStatus?: string): boolean => {
    return deploymentStatus === "deploy_failed";
};

export interface DeployableEntity {
    deploymentStatus?: string;
    syncStatus?: string | null;
    deployed?: any;
}

export interface DeploymentActionInfo {
    action: DeploymentAction;
    label: string;
}

export const isEntityErrorState = (entity: DeployableEntity): boolean => {
    return isDeployFailed(entity.deploymentStatus);
};

export const getEntityDeploymentActions = (entity: DeployableEntity): DeploymentActionInfo[] => {
    if (!entity) return [];
    const { deploymentStatus, syncStatus, deployed } = entity;

    if (deploymentStatus === "deploy_failed") {
        return [{ action: "deploy", label: "Redeploy" }];
    }

    if (deployed) {
        const actions: DeploymentActionInfo[] = [];
        if (syncStatus === "out_of_sync") {
            actions.push({ action: "update", label: "Deploy Updates" });
        }
        actions.push({ action: "undeploy", label: "Undeploy" });
        return actions;
    }

    return [{ action: "deploy", label: "Deploy" }];
};

export const getPrimaryDeployLabel = (entity: DeployableEntity): string => {
    const actions = getEntityDeploymentActions(entity);
    return actions.length > 0 ? actions[0].label : "Deploy";
};

export const getPrimaryDeployAction = (entity: DeployableEntity): DeploymentAction => {
    const actions = getEntityDeploymentActions(entity);
    return actions.length > 0 ? actions[0].action : "deploy";
};

export const entityNeedsCleanupOnDelete = (entity: DeployableEntity): boolean => {
    return isDeployFailed(entity.deploymentStatus);
};

export const getAvailableDeploymentActions = (entity: DeployableEntity): DeploymentAction[] => {
    return getEntityDeploymentActions(entity).map(a => a.action);
};
