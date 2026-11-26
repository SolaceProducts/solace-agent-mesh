// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, fn, userEvent, within } from "storybook/test";

import { mockCustomToolsets } from "@/testing/data";
import { toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { ToolsetViewPage } from "./ToolsetViewPage";

const toolsetWithAgents = mockCustomToolsets[0];
const toolsetNoAgents = mockCustomToolsets[1];
const toolsetNoZip = mockCustomToolsets[2];
const toolsetFailed = mockCustomToolsets[3];

const meta = {
    title: "Toolsets/ToolsetViewPage",
    component: ToolsetViewPage,
    parameters: {
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/toolsets", () => HttpResponse.json(paginatedResponse(mockCustomToolsets.map(toolsetToV2))))),
        },
        docs: {
            description: {
                component: "Full-page view for a single custom toolset — shows details tab (name, description, status, tools) and agent usage tab.",
            },
        },
    },
} satisfies Meta<typeof ToolsetViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: toolsetWithAgents.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect((await canvas.findAllByText("Customer_Data_Tools")).length).toBeGreaterThan(0);

        expect(canvas.getByRole("tab", { name: "Details" })).toBeInTheDocument();
        expect(canvas.getByRole("tab", { name: /Agent Usage/i })).toBeInTheDocument();

        expect(canvas.getByRole("tab", { name: "Details" })).toHaveAttribute("aria-selected", "true");

        expect(canvas.getByTestId("exportToolsetButton")).toBeInTheDocument();
        expect(canvas.getByTestId("editToolsetButton")).toBeInTheDocument();

        expect(canvas.getByRole("button", { name: "Close" })).toBeInTheDocument();
    },
};

export const DetailsTab: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: toolsetWithAgents.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Customer_Data_Tools");

        expect(canvas.getByText("Tools for querying and transforming customer records from internal CRM systems.")).toBeInTheDocument();

        expect(canvas.getByText("Ready")).toBeInTheDocument();

        expect(canvas.getByText("Tools")).toBeInTheDocument();

        expect(canvas.getByRole("tab", { name: "Details" })).toHaveAttribute("aria-selected", "true");
    },
};

export const AgentUsageTab: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: toolsetWithAgents.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Customer_Data_Tools");

        const agentUsageTab = canvas.getByRole("tab", { name: /Agent Usage \(2\)/i });
        expect(agentUsageTab).toBeInTheDocument();

        await userEvent.click(agentUsageTab);

        expect(await canvas.findByText("Support Agent")).toBeInTheDocument();
        expect(canvas.getByText("Sales Agent")).toBeInTheDocument();

        expect(canvas.queryByTestId("exportToolsetButton")).not.toBeInTheDocument();
        expect(canvas.queryByTestId("editToolsetButton")).not.toBeInTheDocument();
    },
};

export const ExportButtonDisabledWhenNoZip: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: toolsetNoZip.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Report_Generator");

        const exportBtn = canvas.getByTestId("exportToolsetButton");
        expect(exportBtn).toBeInTheDocument();
        expect(exportBtn).toBeDisabled();
    },
};

export const EditNavigation: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: toolsetWithAgents.id }),
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const handleEdit = parameters.chatContext.handleEditCustomToolset as ReturnType<typeof fn>;

        await canvas.findAllByText("Customer_Data_Tools");

        const editBtn = canvas.getByTestId("editToolsetButton");
        await userEvent.click(editBtn);

        expect(handleEdit).toHaveBeenCalledOnce();
        expect(handleEdit).toHaveBeenCalledWith(expect.objectContaining({ id: toolsetWithAgents.id, name: toolsetWithAgents.name }));
    },
};

export const Loading: Story = {
    parameters: {
        chatContext: {
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: "custom-toolset-1" }),
        },
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/toolsets", async () => {
                    await delay("infinite");
                    return HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 100, nextPage: null, totalPages: 1 } } });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loading Toolset...")).toBeInTheDocument();
    },
};

export const NotFound: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: "nonexistent-toolset-id" }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Toolset Not Found")).toBeInTheDocument();
    },
};

export const FailedStatus: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: toolsetFailed.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Legacy_API_Bridge");

        expect(canvas.getByText("Failed")).toBeInTheDocument();

        expect(canvas.getByTestId("messageBanner")).toBeInTheDocument();
        expect(canvas.getByText("Module 'legacy_bridge' not found")).toBeInTheDocument();
        expect(canvas.getByText("Import failed: missing dependency 'requests'")).toBeInTheDocument();

        expect(canvas.getByText(/Discovery failed — no tools available/)).toBeInTheDocument();
        expect(canvas.queryByText("No tools discovered yet")).not.toBeInTheDocument();
    },
};

export const AgentUsageEmptyState: Story = {
    parameters: {
        chatContext: {
            customToolsets: mockCustomToolsets,
            customToolsetsLoading: false,
            customToolsetsError: null,
            handleEditCustomToolset: fn(),
            handleViewCustomToolset: fn(),
            useLoaderData: () => ({ toolsetId: toolsetNoAgents.id }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Inventory_Manager");

        const agentUsageTab = canvas.getByRole("tab", { name: /Agent Usage \(0\)/i });
        await userEvent.click(agentUsageTab);

        expect(await canvas.findByText("No agents use this toolset")).toBeInTheDocument();
    },
};
