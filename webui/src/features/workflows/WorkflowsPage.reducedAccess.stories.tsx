// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import type { Workflow } from "@/types/workflow";

import { WorkflowsPage } from "@/features/workflows/WorkflowsPage";

import { workflowToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const workflow: Workflow = {
    id: "11111111-1111-1111-1111-000000000001",
    name: "OrderFulfilment",
    displayName: "Order Fulfilment",
    description: "Routes an order through validation and shipping.",
    appConfig: {},
    deploymentStatus: "deployed",
    runtimeStatus: { status: "running" },
    createdBy: "alice@example.com",
    updatedBy: "alice@example.com",
    createdTime: 1772323200000,
    updatedTime: 1772323200000,
};

const handlers = withDefaults(http.get("*/api/v2/platform/workflows", () => HttpResponse.json(paginatedResponse([workflow].map(workflowToV2)))));

const meta = {
    title: "Workflow/WorkflowsPage/Reduced Access",
    component: WorkflowsPage,
    parameters: {
        docs: {
            description: {
                component: "The workflows list as seen by a workflow_builder:*:read holder — the page is reachable, its write affordances are not.",
            },
        },
        msw: { handlers },
    },
} satisfies Meta<typeof WorkflowsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CanRead: Story = {
    parameters: {
        userContext: {
            user: { capabilities: [CAPABILITIES.readWorkflows] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readWorkflows,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByTestId(`workflow-name-${workflow.name}`)).toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("More"));
        const menu = within(document.body);
        expect(await menu.findByText("Open Workflow")).toBeInTheDocument();
        expect(menu.queryByText("Edit")).not.toBeInTheDocument();
        expect(menu.queryByText("Delete")).not.toBeInTheDocument();
    },
};

export const CanUpdate: Story = {
    parameters: {
        userContext: {
            user: { capabilities: [CAPABILITIES.readWorkflows, CAPABILITIES.updateWorkflows] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readWorkflows || capability === CAPABILITIES.updateWorkflows,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByTestId(`workflow-name-${workflow.name}`)).toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("More"));
        const menu = within(document.body);
        expect(await menu.findByText("Edit")).toBeInTheDocument();
        expect(menu.getByText("Edit with AI")).toBeInTheDocument();
        expect(menu.queryByText("Delete")).not.toBeInTheDocument();
    },
};

export const CanDelete: Story = {
    parameters: {
        userContext: {
            user: { capabilities: [CAPABILITIES.readWorkflows, CAPABILITIES.deleteWorkflows] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readWorkflows || capability === CAPABILITIES.deleteWorkflows,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByTestId(`workflow-name-${workflow.name}`)).toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("More"));
        const menu = within(document.body);
        expect(await menu.findByText("Delete")).toBeInTheDocument();
        expect(menu.queryByText("Edit")).not.toBeInTheDocument();
        expect(menu.queryByText("Edit with AI")).not.toBeInTheDocument();
    },
};
