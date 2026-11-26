// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CreateWorkflowRequest } from "@/types/workflow";

export type { CreateWorkflowRequest };

export interface UpdateWorkflowPayload {
    name: string;
    description: string;
    appConfig: Record<string, unknown>;
}

export type WorkflowDeploymentAction = "deploy" | "undeploy" | "update";

export interface WorkflowDeploymentRequest {
    workflowId: string;
    action: WorkflowDeploymentAction;
}

export interface WorkflowDeploymentResult {
    id: string;
    workflowId: string;
    status: "success" | "failed";
    action: WorkflowDeploymentAction;
    errorMessage?: string;
    [k: string]: unknown;
}
