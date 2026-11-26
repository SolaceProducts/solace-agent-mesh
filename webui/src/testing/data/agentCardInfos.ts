// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { transformAgentCard } from "@/api/agent-cards";
import { transformAgent } from "@/api/agents";
import { transformRemoteAgent } from "@/api/remote-agents";

import type { AgentCardInfo } from "@/types";

import { mockAgentCards } from "./agentCards";
import { mockAgents } from "./agents";
import { mockConnectors } from "./connectors";
import { mockRemoteAgents } from "./remoteAgents";
import { mockToolsets } from "./toolsets";

export const defaultAgentCard: AgentCardInfo = {
    name: "default-agent",
    description: "Default Agent",
    url: "default-url",
    version: "1",
    capabilities: { streaming: false, pushNotifications: false, stateTransitionHistory: false, extensions: [] },
    defaultInputModes: [],
    defaultOutputModes: [],
    skills: [],
    protocolVersion: "1",
    type: "discovered",
    displayName: "Default Agent",
    peerAgents: [],
    tools: [],
};

export const mockUserAgentCardInfos: AgentCardInfo[] = mockAgents.map(agent => transformAgent(agent, mockToolsets, mockConnectors));

export const mockDiscoveredAgentCardInfos: AgentCardInfo[] = mockAgentCards.map(card => transformAgentCard(card));

export const mockUserAgentCardInfosMap = new Map(mockAgents.map((a, i) => [a.id, mockUserAgentCardInfos[i]]));

export const getAgentCardById = (id: string): AgentCardInfo | undefined => mockUserAgentCardInfosMap.get(id);

export const createDiscoveredAgentCard = (id: string): AgentCardInfo => {
    const card = getAgentCardById(id);
    if (card) {
        return {
            ...defaultAgentCard,
            name: card.name,
            displayName: card.displayName,
        };
    }

    throw new Error("Unknown agent id: " + id);
};

export const createDiscoveredAgentCardInfos = (activeAgentIds: Set<string> = new Set(["agent_0", "agent_3", "agent_7", "agent_8", "agent_16", "agent_17"])): AgentCardInfo[] => {
    const activeUserCards = mockUserAgentCardInfos.filter(card => activeAgentIds.has(card.name)).map(card => createDiscoveredAgentCard(card.name.replace("agent_", "")));

    return [...mockDiscoveredAgentCardInfos, ...activeUserCards];
};

export const completeOrderWorkflow: AgentCardInfo = {
    name: "CompleteOrderWorkflow",
    displayName: "Complete Order Workflow",
    version: "1.0.0",
    description:
        "A comprehensive order processing workflow that validates orders, enriches customer data, calculates pricing with discounts, checks inventory availability, processes shipping, and finalizes orders.\n\nThis workflow demonstrates multiple agent coordination patterns including:\n- Conditional routing based on order value\n- Parallel processing with maps for order items\n- Retry loops for external service calls\n- Sequential agent orchestration",
    isWorkflow: true,
    capabilities: {
        extensions: [
            {
                uri: "https://solace.com/a2a/extensions/sam/workflow-visualization",
                params: {
                    workflow_config: {
                        description: "A comprehensive order processing workflow",
                        input_schema: {
                            type: "object",
                            properties: {
                                order_id: { type: "string", description: "Unique order identifier" },
                                customer_id: { type: "string", description: "Customer identifier" },
                            },
                        },
                        output_schema: {
                            type: "object",
                            properties: {
                                order_status: { type: "string" },
                                final_total: { type: "number" },
                            },
                        },
                        output_mapping: {
                            order_status: "{{finalize_order.output.status}}",
                            final_total: "{{calculate_price.output.total}}",
                        },
                        nodes: [
                            { id: "validate_order", type: "agent", agent_name: "validate_order" },
                            { id: "enrich_customer", type: "agent", agent_name: "enrich_customer" },
                            { id: "calculate_price", type: "agent", agent_name: "calculate_price" },
                            { id: "check_inventory", type: "agent", agent_name: "check_inventory" },
                            { id: "process_shipping", type: "agent", agent_name: "process_shipping" },
                            { id: "finalize_order", type: "agent", agent_name: "finalize_order" },
                        ],
                    },
                },
            },
        ],
    },
    defaultInputModes: [],
    defaultOutputModes: [],
    protocolVersion: "1.0",
    provider: { organization: "solace", url: "" },
    url: "",
    skills: [],
    status: "active",
};

export const simpleLoopWorkflow: AgentCardInfo = {
    name: "SimpleLoopWorkflow",
    displayName: "SimpleLoopWorkflow",
    version: "1.0.0",
    description: "A simple workflow demonstrating loop functionality",
    isWorkflow: true,
    capabilities: {},
    defaultInputModes: [],
    defaultOutputModes: [],
    protocolVersion: "1.0",
    provider: { organization: "solace", url: "" },
    url: "",
    skills: [],
};

export const customerEnrichmentWorkflow: AgentCardInfo = {
    name: "CustomerEnrichmentWorkflow",
    displayName: "Customer Enrichment",
    version: "2.1.0",
    description: "Enriches customer data with external sources",
    isWorkflow: true,
    capabilities: {},
    defaultInputModes: [],
    defaultOutputModes: [],
    protocolVersion: "1.0",
    provider: { organization: "solace", url: "" },
    url: "",
    skills: [],
};

export const mockWorkflows: AgentCardInfo[] = [completeOrderWorkflow, simpleLoopWorkflow, customerEnrichmentWorkflow];

export const mockRemoteAgentCards: AgentCardInfo[] = mockRemoteAgents.map(agent => transformRemoteAgent(agent));

function createMeshAgentCard(overrides: Pick<AgentCardInfo, "name" | "displayName" | "description" | "url" | "skills">): AgentCardInfo {
    return {
        version: "1.0.0",
        protocolVersion: "0.3.0",
        defaultInputModes: ["text"],
        defaultOutputModes: ["text"],
        capabilities: {
            extensions: [
                {
                    uri: "https://solace.com/a2a/extensions/display-name",
                    params: { display_name: overrides.displayName },
                },
            ],
            streaming: true,
            pushNotifications: false,
            stateTransitionHistory: false,
        },
        peerAgents: [],
        connectors: [],
        connectorObjects: [],
        tools: [],
        ...overrides,
    };
}

export const mockRemoteAgentCardsFromMesh: AgentCardInfo[] = [
    createMeshAgentCard({
        name: "remote_agent_019c0b0c_fa58_7132_8521_dcbce3cdcc3b",
        displayName: "WeatherAgent",
        description: "An agent that provides real-time weather information for cities around the world. Can get current conditions, forecasts, and compare weather between cities.",
        url: "solace:sam-platform/a2a/v1/agent/request/remote_agent_019c0b0c_fa58_7132_8521_dcbce3cdcc3b",
        skills: [
            { id: "get_current_weather", name: "get_current_weather", description: "Get the current weather for a city.", tags: [] },
            { id: "get_weather_forecast", name: "get_weather_forecast", description: "Get a weather forecast for a city.", tags: [] },
            { id: "compare_weather", name: "compare_weather", description: "Compare current weather between two cities.", tags: [] },
        ],
    }),
    createMeshAgentCard({
        name: "remote_agent_019c0adc_54d6_73b0_9763_47cb88b4df28",
        displayName: "AdditionAgent",
        description: "A simple agent that can add two numbers together",
        url: "solace:sam-platform/a2a/v1/agent/request/remote_agent_019c0adc_54d6_73b0_9763_47cb88b4df28",
        skills: [{ id: "add_numbers", name: "add_numbers", description: "Add two numbers together.", tags: [] }],
    }),
    createMeshAgentCard({
        name: "remote_agent_019c0b00_b9ed_7ae2_92c1_903d67ecfea8",
        displayName: "Division Agent",
        description: "A simple agent that can divide two numbers",
        url: "solace:sam-platform/a2a/v1/agent/request/remote_agent_019c0b00_b9ed_7ae2_92c1_903d67ecfea8",
        skills: [{ id: "divide_numbers", name: "divide_numbers", description: "Divide two numbers.", tags: [] }],
    }),
];
