// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { RemoteAgent } from "@/types";

import type { AgentDeploymentResult } from "../agent-deployments/types";

import type { FetchAgentCardRequest } from "./service";
import type { AgentCardResponse, CreateRemoteAgentRequest, RemoteAgentResponse } from "./types";
import { createRemoteAgentV2, deleteRemoteAgentV2, deployRemoteAgentV2, fetchAgentCardV2, getAllRemoteAgentsV2, getRemoteAgentV2, updateRemoteAgentV2 } from "./v2";

export interface RemoteAgentsApi {
    getAll: () => Promise<RemoteAgent[]>;
    get: (agentId: string, includeCard?: boolean) => Promise<RemoteAgentResponse>;
    create: (request: CreateRemoteAgentRequest) => Promise<RemoteAgentResponse>;
    update: (agentId: string, request: CreateRemoteAgentRequest) => Promise<RemoteAgentResponse>;
    delete: (agentId: string) => Promise<void>;
    deploy: (agentId: string, action?: "deploy" | "undeploy") => Promise<AgentDeploymentResult>;
    fetchCard: (request: FetchAgentCardRequest) => Promise<AgentCardResponse>;
}

export const remoteAgentsApi: RemoteAgentsApi = {
    getAll: getAllRemoteAgentsV2,
    get: getRemoteAgentV2,
    create: createRemoteAgentV2,
    update: updateRemoteAgentV2,
    delete: deleteRemoteAgentV2,
    deploy: deployRemoteAgentV2,
    fetchCard: fetchAgentCardV2,
};
