// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import type { NavigateFunction, useLoaderData } from "react-router-dom";

import { transformAgentCard } from "@/api/agent-cards";

import { BuilderProvider } from "@/providers/BuilderProvider";
import { ChatMessageDataProvider } from "@/providers/ChatMessageDataProvider";
import { ChatContext, type ChatContextValue, defaultChatContextValue } from "@/providers/contexts/ChatContext";

import { getAgentById, mockAgentCards, mockGatewaySchemas } from "../data";

const transformedMockAgents = mockAgentCards.map(transformAgentCard);

const agentNameDisplayNameMap = transformedMockAgents.reduce(
    (acc, agent) => {
        if (agent.name) {
            acc[agent.name] = agent.displayName || agent.name;
        }
        return acc;
    },
    {} as Record<string, string>
);

const defaultMockChatContext: Omit<ChatContextValue, "setIsSidePanelCollapsed"> = {
    ...defaultChatContextValue,

    agents: transformedMockAgents,
    agentNameDisplayNameMap,
    selectedAgentName: transformedMockAgents[0]?.name || "",
    currentUserEmail: "test@example.com",
    ragEnabled: true,
    platformConfigured: true,
    gatewaySchemas: mockGatewaySchemas,
    getGatewaySchema: (type: string) => mockGatewaySchemas.find(s => s.type === type),

    handleHeaderClick: () => alert("Header clicked"),
    handleViewAgent: () => alert("View agent clicked"),
    handleEditAgent: () => alert("Edit agent clicked"),
    handleCreateAgent: () => alert("Create agent clicked"),
    handleDeleteAgent: async () => {
        alert("Delete agent clicked");
        return true;
    },
    handleEditConnector: () => alert("Edit connector clicked"),
    handleDeleteConnector: async () => {
        alert("Delete connector clicked");
    },
    navigate: ((path: string) => alert(`Navigate clicked: ${path}`)) as unknown as NavigateFunction,
    useLoaderData: (() => ({ agentId: getAgentById("0")!.id, isNew: false })) as unknown as typeof useLoaderData,
};

interface MockChatProviderProps {
    children: React.ReactNode;
    mockValues?: Partial<ChatContextValue>;
}

export const MockChatProvider: React.FC<MockChatProviderProps> = ({ children, mockValues = {} }) => {
    const [isSidePanelCollapsed, setIsSidePanelCollapsed] = useState(mockValues.isSidePanelCollapsed ?? defaultMockChatContext.isSidePanelCollapsed);

    const contextValue: ChatContextValue = {
        ...defaultMockChatContext,
        ...mockValues,
        isSidePanelCollapsed,
        setIsSidePanelCollapsed,
    };

    return (
        <ChatContext.Provider value={contextValue}>
            <BuilderProvider>
                <ChatMessageDataProvider>{children}</ChatMessageDataProvider>
            </BuilderProvider>
        </ChatContext.Provider>
    );
};
