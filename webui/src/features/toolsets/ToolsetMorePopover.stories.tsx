// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { MorePopoverButton } from "@/common";
import { mockCustomToolsets } from "@/testing/data";

import { ToolsetMorePopover } from "./ToolsetMorePopover";

const toolsetWithAgents = mockCustomToolsets[0];

const toolsetNoAgents = mockCustomToolsets[1];

const meta = {
    title: "Toolsets/ToolsetMorePopover",
    component: ToolsetMorePopover,
    parameters: {
        layout: "centered",
        docs: {
            description: {
                component: "Popover with Edit and Delete menu actions for a custom toolset row. Delete opens a ConfirmationDialog that warns about attached agents.",
            },
        },
    },
} satisfies Meta<typeof ToolsetMorePopover>;

export default meta;
type Story = StoryObj<typeof meta>;

const baseChatContext = {
    handleEditCustomToolset: fn(),
    handleDeleteCustomToolset: fn(),
};

export const Default: Story = {
    args: {
        toolset: toolsetNoAgents,
        children: MorePopoverButton,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleEditCustomToolset: fn(),
            handleDeleteCustomToolset: fn(),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const triggerBtn = canvas.getByTestId("toolsetMorePopoverTrigger");
        expect(triggerBtn).toBeInTheDocument();

        await userEvent.click(triggerBtn);

        expect(await screen.findByText("Edit")).toBeInTheDocument();
        expect(await screen.findByText("Delete")).toBeInTheDocument();
    },
};

export const EditAction: Story = {
    args: {
        toolset: toolsetNoAgents,
        children: MorePopoverButton,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleEditCustomToolset: fn(),
            handleDeleteCustomToolset: fn(),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const handleEdit = parameters.chatContext.handleEditCustomToolset as ReturnType<typeof fn>;

        await userEvent.click(canvas.getByTestId("toolsetMorePopoverTrigger"));

        await userEvent.click(await screen.findByText("Edit"));

        expect(handleEdit).toHaveBeenCalledOnce();
        expect(handleEdit).toHaveBeenCalledWith(toolsetNoAgents);
    },
};

export const DeleteConfirmation: Story = {
    args: {
        toolset: toolsetNoAgents,
        children: MorePopoverButton,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleEditCustomToolset: fn(),
            handleDeleteCustomToolset: fn(),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(canvas.getByTestId("toolsetMorePopoverTrigger"));

        await userEvent.click(await screen.findByText("Delete"));

        expect(await screen.findByText(`Delete ${toolsetNoAgents.name}`)).toBeInTheDocument();

        expect(await screen.findByText(/permanently deleted/)).toBeInTheDocument();
    },
};

export const DeleteWithAgentWarning: Story = {
    args: {
        toolset: toolsetWithAgents,
        children: MorePopoverButton,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleEditCustomToolset: fn(),
            handleDeleteCustomToolset: fn(),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await userEvent.click(canvas.getByTestId("toolsetMorePopoverTrigger"));

        await userEvent.click(await screen.findByText("Delete"));

        expect(await screen.findByText(`Delete ${toolsetWithAgents.name}`)).toBeInTheDocument();

        expect(await screen.findByText(/2 agent\(s\)/)).toBeInTheDocument();
        expect(await screen.findByText(/Deleting it will remove the toolset from those agents/)).toBeInTheDocument();
    },
};

export const DeleteCancelled: Story = {
    args: {
        toolset: toolsetNoAgents,
        children: MorePopoverButton,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleEditCustomToolset: fn(),
            handleDeleteCustomToolset: fn(),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const handleDelete = parameters.chatContext.handleDeleteCustomToolset as ReturnType<typeof fn>;

        await userEvent.click(canvas.getByTestId("toolsetMorePopoverTrigger"));
        await userEvent.click(await screen.findByText("Delete"));

        expect(await screen.findByText(`Delete ${toolsetNoAgents.name}`)).toBeInTheDocument();

        await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));

        expect(handleDelete).not.toHaveBeenCalled();
    },
};

export const DeleteConfirmed: Story = {
    args: {
        toolset: toolsetNoAgents,
        children: MorePopoverButton,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleEditCustomToolset: fn(),
            handleDeleteCustomToolset: fn(),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const handleDelete = parameters.chatContext.handleDeleteCustomToolset as ReturnType<typeof fn>;

        await userEvent.click(canvas.getByTestId("toolsetMorePopoverTrigger"));
        await userEvent.click(await screen.findByText("Delete"));

        const confirmBtn = await screen.findByRole("button", { name: "Delete" });
        await userEvent.click(confirmBtn);

        expect(handleDelete).toHaveBeenCalledOnce();
        expect(handleDelete).toHaveBeenCalledWith(toolsetNoAgents);
    },
};
