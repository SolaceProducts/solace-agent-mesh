// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { instantToIso, postDeployment } from "../common";

import type { AgentDeploymentRequest, AgentDeploymentResult } from "./types";

interface AgentDeploymentV2 {
    id: string;
    agentId: string;
    status: AgentDeploymentResult["status"];
    action: AgentDeploymentResult["action"];
    failure: { reason: string; message: string; errorId: string | null } | null;
    createdBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

function toAgentDeploymentResult(d: AgentDeploymentV2): AgentDeploymentResult {
    return {
        id: d.id,
        agentId: d.agentId,
        status: d.status,
        action: d.action,
        errorMessage: d.failure?.message,
        createdBy: d.createdBy,
        createdAt: instantToIso(d.createdTime),
        updatedAt: instantToIso(d.updatedTime),
    };
}

export async function postAgentDeploymentV2({ agentId, action, skipIfCurrent }: AgentDeploymentRequest): Promise<AgentDeploymentResult> {
    return toAgentDeploymentResult(await postDeployment<AgentDeploymentV2>("/agentDeployments", { agentId, action, skipIfCurrent }));
}
