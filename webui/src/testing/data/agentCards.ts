// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type AgentCard } from "@/types/a2a";

export const orchestratorAgent: AgentCard = {
    name: "Orchestrator",
    description: "The Orchestrator component. It manages tasks, and coordinating multi-agent workflows.",
    url: "solace:beta/a2a/v1/agent/request/Orchestrator",
    version: "1.0.0-alpha",
    capabilities: {
        streaming: true,
        pushNotifications: false,
        stateTransitionHistory: false,
        extensions: [
            {
                description: "A list of peer agents this agent is configured to communicate with.",
                params: {
                    peer_agent_names: ["agent_weatherExpert", "agent_comedyCompanion"],
                },
                uri: "https://solace.com/a2a/extensions/peer-agent-topology",
            },
            {
                description: "A UI-friendly display name for the agent.",
                params: {
                    display_name: "Orchestrator",
                },
                uri: "https://solace.com/a2a/extensions/display-name",
            },
            {
                description: "A list of tools available to the agent.",
                params: {
                    tools: [
                        {
                            description: "List artifacts",
                            id: "list_artifacts",
                            name: "list_artifacts",
                            tags: [],
                        },
                        {
                            description: "Create Plotly chart",
                            id: "create_chart_from_plotly_config",
                            name: "create_chart_from_plotly_config",
                            tags: [],
                        },
                        {
                            description: "Query data with SQL",
                            id: "query_data_with_sql",
                            name: "query_data_with_sql",
                            tags: [],
                        },
                    ],
                },
                uri: "https://solace.com/a2a/extensions/sam/tools",
            },
        ],
    },
    defaultInputModes: ["text"],
    defaultOutputModes: ["text", "file"],
    skills: [],
    protocolVersion: "0.3.0",
};

export const comedyCompanionAgent: AgentCard = {
    name: "agent_comedyCompanion",
    description: "A humorous agent that delivers a variety of jokes across different styles, themes, and formats to brighten your day and make you laugh.",
    url: "solace:beta/a2a/v1/agent/request/agent_comedyCompanion",
    version: "1.0.0-alpha",
    capabilities: {
        streaming: true,
        pushNotifications: false,
        stateTransitionHistory: false,
        extensions: [
            {
                description: "A UI-friendly display name for the agent.",
                params: {
                    display_name: "Comedy Companion",
                },
                uri: "https://solace.com/a2a/extensions/display-name",
            },
            {
                description: "A list of tools available to the agent.",
                params: {
                    tools: [],
                },
                uri: "https://solace.com/a2a/extensions/sam/tools",
            },
        ],
    },
    defaultInputModes: ["text"],
    defaultOutputModes: ["text"],
    skills: [],
    protocolVersion: "0.3.0",
};

export const weatherExpertAgent: AgentCard = {
    name: "agent_weatherExpert",
    description: "An agent that provides weather forecasts, current conditions, and weather-related information to help users plan activities and stay informed about meteorological conditions.",
    url: "solace:beta/a2a/v1/agent/request/agent_weatherExpert",
    version: "1.0.0-alpha",
    capabilities: {
        streaming: true,
        pushNotifications: false,
        stateTransitionHistory: false,
        extensions: [
            {
                description: "A UI-friendly display name for the agent.",
                params: {
                    display_name: "Weather Expert",
                },
                uri: "https://solace.com/a2a/extensions/display-name",
            },
            {
                description: "A list of tools available to the agent.",
                params: {
                    tools: [
                        {
                            description: "Make HTTP request",
                            id: "web_request",
                            name: "web_request",
                            tags: [],
                        },
                    ],
                },
                uri: "https://solace.com/a2a/extensions/sam/tools",
            },
        ],
    },
    defaultInputModes: ["text"],
    defaultOutputModes: ["text", "file"],
    skills: [
        {
            description: "Access the web to find information to complete user requests.",
            id: "web",
            name: "Web",
            tags: [],
        },
    ],
    protocolVersion: "0.3.0",
};

export const mockAgentCards: AgentCard[] = [orchestratorAgent, comedyCompanionAgent, weatherExpertAgent];

export const makeAgentCard = ({ name, isWorkflow = false, isInternal = false, inputSchema }: { name: string; isWorkflow?: boolean; isInternal?: boolean; inputSchema?: Record<string, unknown> }): AgentCard => {
    const extensions: Array<{ uri: string; params: Record<string, unknown> }> = [{ uri: "https://solace.com/a2a/extensions/display-name", params: { display_name: name } }];
    if (isWorkflow) {
        extensions.push({ uri: "https://solace.com/a2a/extensions/agent-type", params: { type: "workflow" } });
    }
    if (inputSchema) {
        extensions.push({ uri: "https://solace.com/a2a/extensions/sam/schemas", params: { input_schema: inputSchema } });
    }
    if (isInternal) {
        extensions.push({ uri: "https://solace.com/a2a/extensions/sam/internal", params: { internal: true } });
    }
    return {
        name,
        description: `${name} description`,
        url: `solace:beta/a2a/v1/agent/request/${name}`,
        version: "1.0.0",
        capabilities: { streaming: true, pushNotifications: false, stateTransitionHistory: false, extensions },
        defaultInputModes: ["text"],
        defaultOutputModes: ["text"],
        skills: [],
        protocolVersion: "0.3.0",
    };
};

export const createAgentCards = (count: number): AgentCard[] => {
    const agentCards: AgentCard[] = [];
    for (let i = 0; i < count; i++) {
        agentCards.push({
            name: `YamlAgent ${i}`,
            description: `This is mock yaml agent ${i}.`,
            url: `solace:beta/a2a/v1/agent/request/YamlAgent${i}`,
            version: "1.0.0-alpha",
            capabilities: {
                streaming: true,
                pushNotifications: false,
                stateTransitionHistory: false,
                extensions: [],
            },
            defaultInputModes: ["text"],
            defaultOutputModes: ["text"],
            skills: [],
            protocolVersion: "",
        });
    }
    return agentCards;
};
