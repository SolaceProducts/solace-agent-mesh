// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentDeploymentRequest, AgentDeploymentResult } from "./types";
import { postAgentDeploymentV2 } from "./v2";

export interface AgentDeploymentsApi {
    deploy: (request: AgentDeploymentRequest) => Promise<AgentDeploymentResult>;
}

export const agentDeploymentsApi: AgentDeploymentsApi = { deploy: postAgentDeploymentV2 };
