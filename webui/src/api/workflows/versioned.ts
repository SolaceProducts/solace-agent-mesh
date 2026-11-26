// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CreateWorkflowRequest, Workflow } from "@/types/workflow";

import type { UpdateWorkflowPayload, WorkflowDeploymentRequest, WorkflowDeploymentResult } from "./service";
import { createWorkflowV2, deleteWorkflowV2, getAllWorkflowsV2, postWorkflowDeploymentV2, updateWorkflowV2 } from "./v2";

export interface WorkflowsApi {
    getAll: () => Promise<Workflow[]>;
    create: (payload: CreateWorkflowRequest) => Promise<{ id?: string } | null>;
    update: (id: string, payload: UpdateWorkflowPayload) => Promise<Workflow>;
    delete: (id: string) => Promise<void>;
    deploy: (request: WorkflowDeploymentRequest) => Promise<WorkflowDeploymentResult>;
}

export const workflowsApi: WorkflowsApi = { getAll: getAllWorkflowsV2, create: createWorkflowV2, update: updateWorkflowV2, delete: deleteWorkflowV2, deploy: postWorkflowDeploymentV2 };
