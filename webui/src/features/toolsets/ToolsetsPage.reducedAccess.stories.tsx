// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { ToolsetsPage } from "@/features/toolsets/ToolsetsPage";

import { mockCustomToolsets } from "@/testing/data";
import { toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const toolsetListHandlers = withDefaults(
    http.get("*/api/v2/platform/toolsets", ({ request }) => {
        const type = new URL(request.url).searchParams.get("origin");
        return HttpResponse.json(paginatedResponse((type === "custom" ? mockCustomToolsets : []).map(toolsetToV2)));
    })
);

const readOnly = (capability: Capability) => capability === CAPABILITIES.readToolsets;

const openRowMenu = async (canvas: ReturnType<typeof within>, toolsetId: string) => {
    const row = await canvas.findByTestId(`toolsetRow-${toolsetId}`);
    await userEvent.click(within(row).getByTestId("toolsetMorePopoverTrigger"));
    return screen.findByRole("dialog");
};

const meta = {
    title: "Toolsets/ToolsetsPage/Reduced Access",
    component: ToolsetsPage,
    parameters: {
        docs: {
            description: {
                component: "The toolsets list as seen by a toolset:*:read holder — the page is reachable, its write affordances are not.",
            },
        },
    },
} satisfies Meta<typeof ToolsetsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CanRead: Story = {
    parameters: {
        msw: { handlers: toolsetListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: readOnly,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Toolsets")).toBeInTheDocument();
        const row = await canvas.findByTestId(`toolsetRow-${mockCustomToolsets[0].id}`);

        expect(canvas.queryByText("Create Toolset")).not.toBeInTheDocument();
        expect(canvas.getByTitle("Refresh Toolsets")).toBeInTheDocument();
        expect(within(row).queryByTestId("toolsetMorePopoverTrigger")).not.toBeInTheDocument();
    },
};

export const CanCreate: Story = {
    parameters: {
        msw: { handlers: toolsetListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readToolsets || capability === CAPABILITIES.createToolsets,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Create Toolset")).toBeInTheDocument();
    },
};

export const CanUpdateAndDelete: Story = {
    parameters: {
        msw: { handlers: toolsetListHandlers },
        userContext: {
            user: { capabilities: [] },
            userHasCapability: () => true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openRowMenu(canvas, mockCustomToolsets[0].id);
        expect(within(menu).getByText("Edit")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();
    },
};
