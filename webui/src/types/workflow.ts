// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { DeploymentStatus, RuntimeStatus } from "./models";

export interface Workflow {
    id: string;
    name: string;
    displayName: string;
    description: string;
    appConfig: Record<string, unknown>;
    deploymentStatus: DeploymentStatus | "";
    runtimeStatus?: RuntimeStatus;
    createdBy: string;
    updatedBy: string;
    createdTime: number;
    updatedTime: number;
}

export interface CreateWorkflowRequest {
    name: string;
    displayName: string;
    description: string;
    appConfig: Record<string, unknown>;
}

export interface UpdateWorkflowRequest {
    name: string;
    description: string;
    appConfig: Record<string, unknown>;
}

export interface CreateWorkflowDeploymentRequest {
    workflowId: string;
    action: "deploy" | "undeploy" | "update";
    testMode?: boolean;
}

export interface WorkflowDeploymentResponse {
    id: string;
    workflowId: string;
    status: string;
    action: string;
    errorMessage?: string;
    createdBy: string;
    createdTime: number;
    updatedTime: number;
}
