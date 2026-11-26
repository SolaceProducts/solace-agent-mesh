// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { workflowBody } from "@/utils/agentUtils";

import type { Workflow } from "@/types/workflow";

import type { AgentCardInfo, AgentExtension, AgentStatus } from "@/types";

const DEFAULT_WORKFLOW_VERSION = "1.0.0";

function deriveWorkflowStatus(w: Workflow): AgentStatus {
    if (w.deploymentStatus !== "deployed") {
        return w.deploymentStatus || "not_deployed";
    }
    if (w.runtimeStatus?.status === "running") return "active";
    if (w.runtimeStatus?.status === "degraded") return "degraded";
    if (w.runtimeStatus?.status === "disconnected") return "disconnected";
    return "deployed";
}

export function toAgentCardInfo(w: Workflow): AgentCardInfo {
    const workflowConfig = workflowBody(w.appConfig);
    const displayStatus = deriveWorkflowStatus(w);
    const runtimeProblems = displayStatus === "degraded" ? w.runtimeStatus?.problems : undefined;
    const extensions: AgentExtension[] = [{ uri: "https://solace.com/a2a/extensions/agent-type", params: { type: "workflow" } }];
    if (Array.isArray(workflowConfig.nodes)) {
        extensions.push({
            uri: "https://solace.com/a2a/extensions/sam/workflow-visualization",
            params: { workflow_config: workflowConfig },
        });
    }

    return {
        name: w.name,
        description: w.description,
        url: "",
        version: (workflowConfig.version as string) || DEFAULT_WORKFLOW_VERSION,
        protocolVersion: "",
        displayName: w.displayName || w.name,
        capabilities: { streaming: false, pushNotifications: false, stateTransitionHistory: false, extensions },
        defaultInputModes: [],
        defaultOutputModes: [],
        skills: [],
        peerAgents: [],
        tools: [],
        connectors: [],
        connectorObjects: [],
        type: "discovered",
        createdBy: w.createdBy,
        isWorkflow: true,
        isInternal: false,
        isEphemeral: false,
        status: displayStatus,
        runtimeProblems,
    };
}
