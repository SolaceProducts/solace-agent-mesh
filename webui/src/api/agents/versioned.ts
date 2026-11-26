// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Agent } from "@/types";

import type { CreateAgentRequest, SavedAgent } from "./service";
import { createAgentV2, deleteAgentV2, getAgentV2, getAllAgentsV2, getConnectorAgentsV2, updateAgentV2 } from "./v2";

export interface AgentsApi {
    getAll: () => Promise<Agent[]>;
    get: (id: string) => Promise<Agent>;
    create: (payload: CreateAgentRequest) => Promise<SavedAgent>;
    update: (id: string, payload: CreateAgentRequest) => Promise<SavedAgent>;
    delete: (id: string) => Promise<void>;
    getByConnector: (connectorId: string) => Promise<Agent[]>;
}

export const agentsApi: AgentsApi = { getAll: getAllAgentsV2, get: getAgentV2, create: createAgentV2, update: updateAgentV2, delete: deleteAgentV2, getByConnector: getConnectorAgentsV2 };
