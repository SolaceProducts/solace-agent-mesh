// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { createCustomToolsets, mockCustomToolsets } from "@/testing/data";
import { toolsetToV2 } from "@/testing/data/platformWireV2";
import { getPaginatedMeta, paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Toolset } from "@/types";

import { ToolsetsView } from "./ToolsetsView";

const createHandlers = (customList: Toolset[] = mockCustomToolsets, builtinList: Toolset[] = []) =>
    withDefaults(
        http.get("*/api/v2/platform/toolsets", ({ request }) => {
            const type = new URL(request.url).searchParams.get("origin");
            const list = type === "custom" ? customList : builtinList;
            return HttpResponse.json(paginatedResponse(list.map(toolsetToV2)));
        })
    );

const baseChatContext = {
    handleViewCustomToolset: () => {},
    handleCreateCustomToolset: () => {},
    handleEditCustomToolset: () => {},
    handleDeleteCustomToolset: () => {},
    newlyCreatedToolsetId: null,
    setNewlyCreatedToolsetId: () => {},
};

const meta = {
    title: "Toolsets/ToolsetsView",
    component: ToolsetsView,
    parameters: {
        docs: {
            description: {
                component: "List view for toolsets — table with search, sort, pagination, side panel on row click, and URL-driven type/status filters.",
            },
        },
        msw: { handlers: createHandlers() },
    },
} satisfies Meta<typeof ToolsetsView>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        for (const toolset of mockCustomToolsets) {
            expect(await canvas.findByText(toolset.name)).toBeInTheDocument();
        }

        expect(canvas.getByRole("columnheader", { name: /Name/i })).toBeInTheDocument();
        expect(canvas.getByRole("columnheader", { name: /Agent Usage/i })).toBeInTheDocument();
        expect(canvas.getByRole("columnheader", { name: /Created By/i })).toBeInTheDocument();
    },
};

export const Empty: Story = {
    parameters: {
        chatContext: baseChatContext,
        msw: { handlers: createHandlers([], []) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Extend your agents with toolsets")).toBeInTheDocument();
        expect(await canvas.findByRole("button", { name: "Create Toolset" })).toBeInTheDocument();
    },
};

export const EmptyStateCreateButton: Story = {
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleCreateCustomToolset: fn(),
        },
        msw: { handlers: createHandlers([], []) },
    },
    play: async ({ canvasElement, parameters }) => {
        const canvas = within(canvasElement);
        const handleCreate = parameters.chatContext.handleCreateCustomToolset as ReturnType<typeof fn>;

        const createBtn = await canvas.findByRole("button", { name: "Create Toolset" });
        await userEvent.click(createBtn);

        expect(handleCreate).toHaveBeenCalledOnce();
    },
};

export const Loading: Story = {
    parameters: {
        chatContext: baseChatContext,
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/toolsets", async () => {
                    await delay("infinite");
                    return HttpResponse.json({ data: [], meta: getPaginatedMeta(0) });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loading Toolsets...")).toBeInTheDocument();
    },
};

export const Error: Story = {
    parameters: {
        chatContext: baseChatContext,
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ message: "Failed to connect to platform service", errorId: "stub" }, { status: 500 }))),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText(/Error loading toolsets/)).toBeInTheDocument();
    },
};

export const SearchFiltering: Story = {
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Inventory_Manager");

        const searchInput = canvas.getByTestId("toolsetsSearchInput");
        await userEvent.type(searchInput, "Inventory");

        expect(await canvas.findByText("Inventory_Manager")).toBeInTheDocument();
        expect(canvas.queryByText("Customer_Data_Tools")).not.toBeInTheDocument();
    },
};

export const SearchNoResults: Story = {
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Customer_Data_Tools");

        const searchInput = canvas.getByTestId("toolsetsSearchInput");
        await userEvent.type(searchInput, "nonexistent-xyz");

        expect(await canvas.findByText("No toolsets match your filters.")).toBeInTheDocument();
        expect(await canvas.findByRole("button", { name: "Clear filters" })).toBeInTheDocument();
    },
};

export const SidePanelOpen: Story = {
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("toolsetRow-custom-toolset-1");
        await userEvent.click(canvas.getByTestId("toolsetRow-custom-toolset-1"));

        const panel = await screen.findByText("Toolset Details");
        expect(panel).toBeInTheDocument();
    },
};

export const SortColumns: Story = {
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Customer_Data_Tools");

        const rows = () => canvas.getAllByRole("row").slice(1);
        const firstName = () => within(rows()[0]).getAllByRole("cell")[0].textContent;

        expect(firstName()).toContain("Customer_Data_Tools");

        const nameHeader = canvas.getByRole("columnheader", { name: /Name/i });
        await userEvent.click(nameHeader);
        expect(firstName()).toContain("Report_Generator");

        await userEvent.click(nameHeader);
        expect(firstName()).toContain("Customer_Data_Tools");

        await userEvent.click(canvas.getByRole("columnheader", { name: /Created By/i }));
        expect(firstName()).toContain("Customer_Data_Tools");

        await userEvent.click(canvas.getByRole("columnheader", { name: /Agent Usage/i }));
        expect(canvas.getByTestId("agentCountCell-custom-toolset-2").textContent).toBe("0");
    },
};

export const Pagination: Story = {
    parameters: {
        chatContext: baseChatContext,
        msw: { handlers: createHandlers(createCustomToolsets(25), []) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Toolset_00")).toBeInTheDocument();
        expect(canvas.queryByText("Toolset_24")).not.toBeInTheDocument();

        while (!canvas.queryByText("Toolset_24")) {
            await userEvent.click(canvas.getByLabelText("Go to next page"));
        }

        expect(await canvas.findByText("Toolset_24")).toBeInTheDocument();
        expect(canvas.queryByText("Toolset_00")).not.toBeInTheDocument();
    },
};

export const AgentUsageCount: Story = {
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("agentCountCell-custom-toolset-1");
        expect(canvas.getByTestId("agentCountCell-custom-toolset-1").textContent).toBe("2");
        expect(canvas.getByTestId("agentCountCell-custom-toolset-2").textContent).toBe("0");
    },
};

export const FilteredByCustom: Story = {
    parameters: {
        chatContext: baseChatContext,
        routerValues: { initialPath: "/?type=custom" },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Customer_Data_Tools");

        const typeFilter = canvas.getByTestId("toolsetsTypeFilter");
        expect(typeFilter).toBeInTheDocument();
        expect(typeFilter.textContent).toContain("Custom");
    },
};

export const FilteredByFailed: Story = {
    parameters: {
        chatContext: baseChatContext,
        routerValues: { initialPath: "/?discoveryStatus=failed" },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Legacy_API_Bridge")).toBeInTheDocument();
        expect(canvas.queryByText("Customer_Data_Tools")).not.toBeInTheDocument();

        const statusFilter = canvas.getByTestId("toolsetsStatusFilter");
        expect(statusFilter.textContent).toContain("Failed");

        expect(canvas.getByTestId("toolsetsClearFilters")).toBeInTheDocument();
    },
};

export const MixedStatuses: Story = {
    parameters: {
        chatContext: baseChatContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Customer_Data_Tools");

        const rows = canvas.getAllByRole("row").slice(1);
        expect(rows.length).toBeGreaterThan(0);
    },
};

export const EmptyAfterFilter: Story = {
    parameters: {
        chatContext: baseChatContext,
        msw: {
            handlers: createHandlers(
                mockCustomToolsets.filter(t => t.discoveryStatus !== "failed"),
                []
            ),
        },
        routerValues: { initialPath: "/?discoveryStatus=failed" },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("No toolsets match your filters.")).toBeInTheDocument();
        const clearButtons = await canvas.findAllByRole("button", { name: /Clear filters/i });
        expect(clearButtons.length).toBeGreaterThanOrEqual(1);
    },
};
