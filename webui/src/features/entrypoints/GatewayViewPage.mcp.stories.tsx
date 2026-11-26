// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { GatewayViewPage } from "@/features/entrypoints/GatewayViewPage";

import { mockGatewaySchemas } from "@/testing/data";
import { toEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";
import type { Gateway } from "@/types";

const mcpGateway: Gateway = {
    id: "gateway-mcp-1",
    name: "My MCP Gateway",
    description: "Exposes agents as MCP tools.",
    type: "mcp",
    namespace: "default",
    values: {
        enableAuth: "false",
        includeTools: [],
        excludeTools: [{ pattern: "SecretAgent__*" }, { pattern: "*__internal" }],
        corsAllowedOrigins: [{ origin: "http://localhost:6274" }],
    },
    deploymentStatus: "not_deployed",
    createdBy: "test@example.com",
    updatedBy: "test@example.com",
    createdAt: "2026-06-24T00:00:00Z",
    updatedAt: "2026-06-24T00:00:00Z",
};

const handlers = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-mcp-1", () => HttpResponse.json({ data: toEntrypointV2(mcpGateway) })),
    http.get("*/api/v2/platform/entrypointDeployments", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 0, count: 0, pageSize: 0, nextPage: null, totalPages: 0 } } }))
);

const meta = {
    title: "Entrypoints/GatewayViewPage/MCP",
    component: GatewayViewPage,
    parameters: {
        msw: { handlers },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-mcp-1" }),
            gatewaySchemas: mockGatewaySchemas,
            navigate: () => {},
            addNotification: () => {},
            gatewayDeployError: null,
            setGatewayDeployError: () => {},
            deployGateway: async () => {},
        },
    },
} satisfies Meta<typeof GatewayViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ShowsArrayListValues: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("My MCP Gateway");

        expect(await canvas.findByText("SecretAgent__*, *__internal")).toBeInTheDocument();
        expect(canvas.getByText("http://localhost:6274")).toBeInTheDocument();

        const includeRow = canvas.getByText("Include tools").closest("div")?.parentElement;
        expect(within(includeRow as HTMLElement).getByText("—")).toBeInTheDocument();
    },
};
