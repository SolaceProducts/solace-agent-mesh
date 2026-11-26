// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import type { Workflow } from "@/types/workflow";

import { completeOrderWorkflow, completeOrderWorkflowConfig } from "@/testing/data";
import { workflowToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { WorkflowDetailPanel } from "./WorkflowDetailPanel";

const WORKFLOW_ID = "019f512b-c85e-76b2-bfed-7d01122848b3";

const platformWorkflow: Workflow = {
    id: WORKFLOW_ID,
    name: completeOrderWorkflow.name,
    displayName: completeOrderWorkflow.displayName ?? completeOrderWorkflow.name,
    description: completeOrderWorkflow.description ?? "",
    appConfig: {},
    deploymentStatus: "deployed",
    createdBy: "workflow-author@example.org",
    updatedBy: "workflow-editor@example.org",
    createdTime: 1710843000000,
    updatedTime: 1710849900000,
};

const meta = {
    title: "Workflow/WorkflowDetailPanel",
    component: WorkflowDetailPanel,
    parameters: {
        docs: {
            description: {
                component: "Side panel component for displaying detailed workflow information including description, version, and node count.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof WorkflowDetailPanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        workflow: completeOrderWorkflow,
        config: completeOrderWorkflowConfig,
        onClose: () => alert("Close clicked"),
        showOpenButton: false,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByRole("complementary", { name: "Workflow details panel" })).toBeInTheDocument();
        expect(await canvas.findByText("Complete Order Workflow")).toBeInTheDocument();
        expect(await canvas.findByText("Workflow Details")).toBeInTheDocument();
        expect(await canvas.findByText("Version")).toBeInTheDocument();
        expect(await canvas.findByText("Nodes")).toBeInTheDocument();
        expect(await canvas.findByRole("tab", { name: "Input" })).toBeInTheDocument();
        expect(await canvas.findByRole("tab", { name: "Output" })).toBeInTheDocument();
    },
};

export const WithMetadata: Story = {
    args: {
        workflow: completeOrderWorkflow,
        config: completeOrderWorkflowConfig,
        onClose: () => {},
        showOpenButton: false,
    },
    parameters: {
        featureContext: { rbac: true },
        msw: { handlers: withDefaults(http.get("*/api/v2/platform/workflows", () => HttpResponse.json(paginatedResponse([platformWorkflow].map(workflowToV2))))) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Created By:")).toBeInTheDocument();
        expect(await canvas.findByText("workflow-author@example.org")).toBeInTheDocument();
        expect(await canvas.findByText("Last Updated By:")).toBeInTheDocument();
        expect(await canvas.findByText("workflow-editor@example.org")).toBeInTheDocument();
        expect(await canvas.findByText(WORKFLOW_ID)).toBeInTheDocument();

        expect(await canvas.findByText("Invoke Scope:")).toBeInTheDocument();
        expect(await canvas.findByText(`workflow:${WORKFLOW_ID}:invoke`)).toBeInTheDocument();
    },
};

export const Output: Story = {
    args: {
        workflow: completeOrderWorkflow,
        config: completeOrderWorkflowConfig,
        onClose: () => alert("Close clicked"),
        showOpenButton: false,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByRole("complementary", { name: "Workflow details panel" })).toBeInTheDocument();
        expect(await canvas.findByText("Complete Order Workflow")).toBeInTheDocument();

        (await canvas.findByRole("tab", { name: "Output" })).click();
        expect(await canvas.findByText("Output Mapping")).toBeInTheDocument();
    },
};
