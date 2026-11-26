// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { mockCustomToolsets } from "@/testing/data";

import { ToolsetSidePanel } from "./ToolsetSidePanel";

const toolsetWithAgents = mockCustomToolsets[0];

const toolsetNoAgents = mockCustomToolsets[1];

const ControlledWrapper: React.FC<{
    toolset: (typeof mockCustomToolsets)[0];
    initialOpen?: boolean;
}> = ({ toolset, initialOpen = true }) => {
    const [isOpen, setIsOpen] = useState(initialOpen);

    if (!isOpen) {
        return (
            <div className="p-4">
                <button data-testid="reopenButton" onClick={() => setIsOpen(true)}>
                    Open Panel
                </button>
                <p data-testid="panelClosedMessage">Side panel is closed</p>
            </div>
        );
    }

    return <ToolsetSidePanel toolset={toolset} onClose={() => setIsOpen(false)} />;
};

const meta = {
    title: "Toolsets/ToolsetSidePanel",
    component: ToolsetSidePanel,
    parameters: {
        docs: {
            description: {
                component: "Side panel displaying toolset details, agent usage, and actions (Open Toolset, Add to Agent).",
            },
        },
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5 border-l">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof ToolsetSidePanel>;

export default meta;
type Story = StoryObj<typeof meta>;

const baseChatContext = {
    handleViewCustomToolset: fn(),
    handleEditCustomToolset: fn(),
    handleCreateCustomToolset: fn(),
    handleDeleteCustomToolset: fn(),
};

export const WithAgents: Story = {
    args: {
        toolset: toolsetWithAgents,
        onClose: fn(),
    },
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Customer_Data_Tools")).toBeInTheDocument();

        expect(canvas.getByText("Toolset Details")).toBeInTheDocument();

        expect(canvas.getByText("Agent Usage (2)")).toBeInTheDocument();

        expect(canvas.getByText("Support Agent")).toBeInTheDocument();
        expect(canvas.getByText("Sales Agent")).toBeInTheDocument();

        expect(canvas.queryByText("No agents are currently using this tool")).not.toBeInTheDocument();
    },
};

export const WithoutAgents: Story = {
    args: {
        toolset: toolsetNoAgents,
        onClose: fn(),
    },
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Inventory_Manager")).toBeInTheDocument();

        expect(canvas.getByText("Agent Usage (0)")).toBeInTheDocument();

        expect(canvas.getByText("No agents are currently using this tool")).toBeInTheDocument();
    },
};

export const OpenCloseBehaviour: StoryObj<{ toolset: typeof toolsetWithAgents; initialOpen: boolean }> = {
    render: args => <ControlledWrapper toolset={args.toolset} initialOpen={args.initialOpen} />,
    args: {
        toolset: toolsetWithAgents,
        initialOpen: true,
    },
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Customer_Data_Tools")).toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("sidePanelCloseButton"));

        expect(await canvas.findByTestId("panelClosedMessage")).toBeInTheDocument();
        expect(canvas.queryByText("Customer_Data_Tools")).not.toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("reopenButton"));
        expect(await canvas.findByText("Customer_Data_Tools")).toBeInTheDocument();
    },
};

export const OpenToolsetButton: Story = {
    args: {
        toolset: toolsetWithAgents,
        onClose: fn(),
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleViewCustomToolset: fn(),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);

        const openBtn = canvas.getByTestId("openToolsetButton");
        expect(openBtn).toBeInTheDocument();
        expect(openBtn).toHaveTextContent("Open Toolset");

        await userEvent.click(openBtn);

        expect(parameters.chatContext.handleViewCustomToolset).toHaveBeenCalledOnce();
        expect(parameters.chatContext.handleViewCustomToolset).toHaveBeenCalledWith(toolsetWithAgents);
    },
};

export const AddToAgentButton: Story = {
    args: {
        toolset: toolsetWithAgents,
        onClose: fn(),
    },
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const addBtn = canvas.getByTestId("addToAgentButton");
        expect(addBtn).toBeInTheDocument();
        expect(addBtn).toHaveTextContent("Add to Agent");

        await userEvent.click(addBtn);

        expect(await screen.findByText("Add Toolset to Agent")).toBeInTheDocument();
    },
};

export const PendingStatus: Story = {
    args: {
        toolset: mockCustomToolsets[2],
        onClose: fn(),
    },
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Report_Generator")).toBeInTheDocument();
        expect(canvas.queryByText("Pending")).not.toBeInTheDocument();
    },
};

export const FailedStatus: Story = {
    args: {
        toolset: mockCustomToolsets[3],
        onClose: fn(),
    },
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Legacy_API_Bridge")).toBeInTheDocument();
        expect(canvas.queryByText("Discovery Failed")).not.toBeInTheDocument();
        expect(canvas.getByTestId("messageBanner")).toBeInTheDocument();
        expect(canvas.getByText("Module 'legacy_bridge' not found")).toBeInTheDocument();
        expect(canvas.getByText("Import failed: missing dependency 'requests'")).toBeInTheDocument();
    },
};
