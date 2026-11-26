// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type AgentDeploymentAction = "deploy" | "undeploy" | "update";

export interface AgentDeploymentRequest {
    agentId: string;
    action: AgentDeploymentAction;
    skipIfCurrent?: boolean;
}

export interface AgentDeploymentResult {
    id: string;
    agentId: string;
    status: "success" | "failed";
    action: AgentDeploymentAction;
    errorMessage?: string;
    [k: string]: unknown;
}
