// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Agent, AgentSkillFE } from "@/types";

import { mockConnectors } from "./connectors";

export const createAgent = (id: string, config: Partial<Agent> = {}): Agent => {
    const now = new Date().toISOString();
    return {
        id,
        instanceName: `agent_${id.replaceAll("-", "_")}`,
        name: config.name || `Agent ${id}`,
        description: config.description || `Description for ${id}`,
        inputModes: undefined,
        outputModes: undefined,
        skills: undefined,
        deploymentStatus: "not_deployed",
        createdBy: "Test User",
        updatedBy: "Test User",
        createdAt: now,
        updatedAt: now,
        ...config,
    };
};

export const createDeployedAgent = (id: string, baseConfig: Partial<Agent>, deployedDiff: Partial<Agent> = {}): Agent => {
    const base = createAgent(id, {
        deploymentStatus: "deployed",
        runtimeStatus: { status: "running" },
        syncStatus: "in_sync",
        ...baseConfig,
    });

    return {
        ...base,
        deployed: {
            ...base,
            deployedAt: new Date().toISOString(),
            deployedBy: "Test User",
            ...deployedDiff,
        },
    };
};

export const createSkill = (name: string, description: string): AgentSkillFE => ({
    name,
    description,
});

const COMMON_SKILLS = {
    conversation: createSkill("Conversation", "Engages in natural language conversation"),
    infoRetrieval: createSkill("Information Retrieval", "Retrieves information from various sources"),
    comedy: createSkill("Comedy", "Engages in natural comedy"),
};

export const mockAgents: Agent[] = [
    createDeployedAgent(
        "0",
        {
            name: "Agent 0",
            description: "Agent 0 description - deployed, in sync",
            toolsets: ["web"],
            connectors: ["mock-connector-1"],
            inputModes: ["text", "file"],
            outputModes: ["text", "file"],
            skills: [COMMON_SKILLS.conversation, COMMON_SKILLS.infoRetrieval],
        },
        {
            connectorSnapshots: [{ ...mockConnectors[0] }],
        }
    ),

    createDeployedAgent("1", {
        name: "Agent 1",
        description: "Agent 1 description - disconnected",
        runtimeStatus: undefined,
        toolsets: ["web"],
        inputModes: ["text", "file"],
        outputModes: ["text", "file"],
        skills: [COMMON_SKILLS.conversation, COMMON_SKILLS.infoRetrieval],
        systemPrompt: "You are Agent 1, a helpful assistant that can engage in conversation and retrieve information.",
    }),

    createDeployedAgent(
        "3",
        {
            name: "Agent 3",
            description: "Agent 3 description - deployed, out of sync",
            toolsets: ["web"],
            inputModes: ["text", "file"],
            outputModes: ["text", "file"],
            skills: [COMMON_SKILLS.comedy],
            syncStatus: "out_of_sync",
            systemPrompt: "You are Agent 3, a helpful assistant.",
        },
        {
            inputModes: ["text"],
        }
    ),

    createDeployedAgent(
        "4",
        {
            name: "Agent 4",
            description: "Agent 4 description - disconnected, out of sync",
            runtimeStatus: undefined,
            skills: [COMMON_SKILLS.conversation],
            syncStatus: "out_of_sync",
            inputModes: ["text", "file"],
        },
        {
            inputModes: ["text"],
        }
    ),

    createAgent("6", {
        name: "Agent 6",
        description: "Agent 6 description - deploy failed",
        deploymentStatus: "deploy_failed",
    }),

    createAgent("7", {
        name: "Agent 7",
        description: "Agent 7 description - deploy failed, out of sync",
        deploymentStatus: "deploy_failed",
        syncStatus: "out_of_sync",
        deployed: {
            id: "7",
            name: "Agent 7",
            description: "Agent 7 description - deployed",
            inputModes: ["text", "file"],
            outputModes: ["text"],
            skills: undefined,
        },
    }),

    createAgent("8", {
        name: "Agent 8",
        description: "Agent 8 description - deployed update, out of sync",
        deploymentStatus: "deployed",
        runtimeStatus: { status: "running" },
        syncStatus: "out_of_sync",
        inputModes: ["text", "file"],
        deployed: {
            id: "8",
            name: "Agent 8",
            description: "Agent 8 description - deployed",
            inputModes: ["text"],
            outputModes: ["text"],
            skills: undefined,
        },
    }),

    createAgent("9", {
        name: "Agent 9",
        description: "Agent 9 description - not deployed, first time",
        deploymentStatus: "not_deployed",
        inputModes: ["text"],
        outputModes: ["text"],
        systemPrompt: "You are Agent 9, a helpful assistant.",
    }),

    createAgent("10", {
        name: "Agent 10",
        description: "Agent 10 description - not deployed",
    }),

    createAgent("11", {
        name: "Agent 11",
        description: "Agent 11 description - was deployed, now not deployed",
        deploymentStatus: "not_deployed",
    }),

    createAgent("16", {
        name: "Agent 16",
        description: "Agent 16 description - deployed, in sync",
        deploymentStatus: "deployed",
        runtimeStatus: { status: "running" },
        syncStatus: "in_sync",
        deployed: {
            id: "16",
            name: "Agent 16",
            description: "Agent 16 description - deployed",
            inputModes: ["text"],
            outputModes: ["text"],
            skills: undefined,
        },
    }),

    createAgent("17", {
        name: "Agent 17",
        description: "Agent 17 description - deployed, out of sync",
        deploymentStatus: "deployed",
        runtimeStatus: { status: "running" },
        syncStatus: "out_of_sync",
        inputModes: ["text", "file"],
        deployed: {
            id: "17",
            name: "Agent 17",
            description: "Agent 17 description - deployed",
            inputModes: ["text"],
            outputModes: ["text"],
            skills: undefined,
        },
    }),

    createDeployedAgent(
        "18",
        {
            name: "Agent 18",
            description: "Agent 18 description - deployed, in sync",
            toolsets: ["web"],
            connectors: ["mock-connector-1"],
            inputModes: ["text"],
            outputModes: ["text"],
            skills: [COMMON_SKILLS.conversation],
        },
        {
            connectorSnapshots: [{ ...mockConnectors[0] }],
        }
    ),
];

export const mockAgentsWithDuplicates: Agent[] = [
    ...mockAgents,

    createAgent("18_duplicate", {
        name: "Agent 18",
        instanceName: "agent_18_duplicate",
        description: "Agent 18 duplicate - not deployed",
        inputModes: ["text"],
        outputModes: ["text"],
        skills: [COMMON_SKILLS.infoRetrieval],
    }),
];

export const createAgents = (count: number): Agent[] => {
    return Array.from({ length: count }, (_, i) =>
        createAgent(`mock-agent-id-${i}`, {
            name: `Agent ${i}`,
            description: `This is mock agent ${i}.`,
            toolsets: ["web"],
            inputModes: ["text"],
            outputModes: ["text"],
            systemPrompt: `System prompt for agent ${i}.`,
            skills: [],
        })
    );
};

export const mockAgentsMap = new Map(mockAgents.map(a => [a.id, a]));

export const getAgentById = (id: string): Agent | undefined => mockAgentsMap.get(id);

export const mockStandardAgents: Agent[] = [
    createAgent("std-1", {
        name: "Support Agent",
        description: "Handles customer support queries.",
        type: "standard",
    }),
    createAgent("std-2", {
        name: "Sales Agent",
        description: "Assists the sales team with product information.",
        type: "standard",
        systemPrompt: "You are a helpful sales assistant with access to customer data tools.",
    }),
    createAgent("std-3", {
        name: "Analytics Agent",
        description: "Runs data analysis and reporting tasks.",
        type: "standard",
    }),
];
