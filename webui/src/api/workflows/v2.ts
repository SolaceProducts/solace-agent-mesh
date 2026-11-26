// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CreateWorkflowRequest, Workflow } from "@/types/workflow";

import { fetchAllPages, instantToIso, pageQuery, parseInstant, platformV2, postDeployment, type RuntimeStatusV2, toRuntimeStatus } from "../common";

import type { UpdateWorkflowPayload, WorkflowDeploymentRequest, WorkflowDeploymentResult } from "./service";

interface WorkflowV2 {
    id: string;
    name: string;
    description: string;
    appConfig: Record<string, unknown>;
    deploymentStatus: Workflow["deploymentStatus"];
    runtimeStatus: RuntimeStatusV2;
    createdBy: string;
    updatedBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

interface WorkflowDeploymentV2 {
    id: string;
    workflowId: string;
    status: WorkflowDeploymentResult["status"];
    action: WorkflowDeploymentResult["action"];
    failure: { reason: string; message: string; errorId: string | null } | null;
    createdBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

const epochMs = (instant: string | null) => parseInstant(instant)?.getTime() ?? 0;

function toWorkflow(w: WorkflowV2): Workflow {
    return {
        id: w.id,
        name: w.name,
        displayName: w.name,
        description: w.description,
        appConfig: w.appConfig,
        deploymentStatus: w.deploymentStatus,
        runtimeStatus: toRuntimeStatus(w.runtimeStatus),
        createdBy: w.createdBy,
        updatedBy: w.updatedBy,
        createdTime: epochMs(w.createdTime),
        updatedTime: epochMs(w.updatedTime),
    };
}

function toWorkflowDeploymentResult(d: WorkflowDeploymentV2): WorkflowDeploymentResult {
    return {
        id: d.id,
        workflowId: d.workflowId,
        status: d.status,
        action: d.action,
        errorMessage: d.failure?.message,
        createdBy: d.createdBy,
        createdAt: instantToIso(d.createdTime),
        updatedAt: instantToIso(d.updatedTime),
    };
}

export async function getAllWorkflowsV2(): Promise<Workflow[]> {
    const all = await fetchAllPages(params => platformV2.list<WorkflowV2>(`/workflows?${pageQuery(params)}`));
    return all.map(toWorkflow);
}

export async function createWorkflowV2({ name, description, appConfig }: CreateWorkflowRequest): Promise<{ id?: string } | null> {
    return toWorkflow(await platformV2.post<WorkflowV2>("/workflows", { name, description, appConfig }));
}

export async function updateWorkflowV2(id: string, { name, description, appConfig }: UpdateWorkflowPayload): Promise<Workflow> {
    return toWorkflow(await platformV2.put<WorkflowV2>(`/workflows/${id}`, { name, description, appConfig }));
}

export async function deleteWorkflowV2(id: string): Promise<void> {
    await platformV2.delete(`/workflows/${id}`);
}

export async function postWorkflowDeploymentV2({ workflowId, action }: WorkflowDeploymentRequest): Promise<WorkflowDeploymentResult> {
    return toWorkflowDeploymentResult(await postDeployment<WorkflowDeploymentV2>("/workflowDeployments", { workflowId, action }));
}
