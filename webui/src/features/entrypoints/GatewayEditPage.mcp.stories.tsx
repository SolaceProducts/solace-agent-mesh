// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { GatewayEditPage } from "@/features/entrypoints/GatewayEditPage";

import { mockGatewaySchemas } from "@/testing/data";
import { savedEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";

let lastCreateBody: Record<string, unknown> | null = null;

const handlersNew = withDefaults(
    http.post("*/api/v2/platform/entrypoints", async ({ request }) => {
        const body = await request.json();
        lastCreateBody = typeof body === "object" && body !== null ? (body as Record<string, unknown>) : null;
        return HttpResponse.json({ data: savedEntrypointV2("gateway-new", lastCreateBody) });
    })
);

const newMcpContext = {
    useLoaderData: () => {
        const hash = window.location.hash;
        if (!hash.includes("?type=mcp")) {
            window.location.hash = hash.includes("?") ? hash + "&type=mcp" : hash + "?type=mcp";
        }
        return { isNew: true };
    },
    gatewaySchemas: mockGatewaySchemas,
    navigate: () => {},
    addNotification: () => {},
};

const meta = {
    title: "Entrypoints/GatewayEditPage/MCP",
    component: GatewayEditPage,
} satisfies Meta<typeof GatewayEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ShowsSlugField: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");
        expect(canvas.getByText("Name")).toBeInTheDocument();
        expect(canvas.getByText("Description")).toBeInTheDocument();
        expect(canvas.getByText("Custom endpoint (optional)")).toBeInTheDocument();
        const banner = await canvas.findByTestId("entrypointPrerequisitesWarning");
        expect(banner).toHaveTextContent("allowlist the per-entrypoint redirect URI");
    },
};

export const CreateWithSlug: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");
        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "My MCP Gateway");
        await userEvent.type(textboxes[1], "A gateway exposing agents as MCP tools.");
        await userEvent.type(textboxes[2], "my-mcp-gw");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({ entrypointType: "mcp", slug: "my-mcp-gw" });
    },
};

export const RejectsInvalidSlug: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");
        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "My MCP Gateway");
        await userEvent.type(textboxes[1], "A gateway exposing agents as MCP tools.");
        await userEvent.type(textboxes[2], "Bad_Slug");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        expect(await canvas.findByText(/Custom endpoint must be 3–63 characters/)).toBeInTheDocument();
        expect(lastCreateBody).toBeNull();
    },
};

export const SaveableWithEmptyLists: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");

        expect(canvas.getByText("Include tools")).toBeInTheDocument();
        expect(canvas.getByText("CORS allowed origins")).toBeInTheDocument();

        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "My MCP Gateway");
        await userEvent.type(textboxes[1], "A gateway exposing agents as MCP tools.");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({ values: { includeTools: [], corsAllowedOrigins: [] } });
    },
};

export const AddToolPatternRoundTrips: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");

        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "My MCP Gateway");
        await userEvent.type(textboxes[1], "A gateway exposing agents as MCP tools.");

        const addButtons = canvas.getAllByRole("button", { name: /Add pattern/i });
        await userEvent.click(addButtons[0]);

        const patternInput = await canvas.findByLabelText("Pattern 1");
        await userEvent.type(patternInput, "MyAgent__*");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({ values: { includeTools: [{ pattern: "MyAgent__*" }], excludeTools: [] } });
    },
};

export const RemoveMiddleRowKeepsOthers: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");

        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "My MCP Gateway");
        await userEvent.type(textboxes[1], "A gateway exposing agents as MCP tools.");

        for (let i = 0; i < 3; i++) {
            await userEvent.click(canvas.getAllByRole("button", { name: /Add pattern/i })[0]);
        }
        await userEvent.type(await canvas.findByLabelText("Pattern 1"), "first");
        await userEvent.type(await canvas.findByLabelText("Pattern 2"), "middle");
        await userEvent.type(await canvas.findByLabelText("Pattern 3"), "last");

        await userEvent.click(canvas.getByLabelText("Remove Pattern 2"));

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({ values: { includeTools: [{ pattern: "first" }, { pattern: "last" }] } });
    },
};

export const RedirectUrisShownUnderAuth: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");

        expect(canvas.getByText("Allowed MCP-client redirect URIs")).toBeInTheDocument();

        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "My MCP Gateway");
        await userEvent.type(textboxes[1], "A gateway exposing agents as MCP tools.");

        await userEvent.click(canvas.getByRole("button", { name: /Add redirect uri/i }));
        await userEvent.type(await canvas.findByLabelText("Redirect URI 1"), "http://127.0.0.1");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await waitFor(() => expect(lastCreateBody).not.toBeNull());
        expect(lastCreateBody).toMatchObject({ values: { allowedRedirectUris: [{ uri: "http://127.0.0.1" }] } });
    },
};

export const HiddenStaleFieldShowsBanner: Story = {
    parameters: {
        msw: { handlers: handlersNew },
        chatContext: newMcpContext,
    },
    play: async ({ canvasElement }) => {
        lastCreateBody = null;
        const canvas = within(canvasElement);

        await canvas.findByText("MCP");

        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "My MCP Gateway");
        await userEvent.type(textboxes[1], "A gateway exposing agents as MCP tools.");

        await userEvent.click(canvas.getByRole("button", { name: /Add redirect uri/i }));
        await userEvent.click(canvas.getByRole("combobox"));
        await userEvent.click(await screen.findByRole("option", { name: /Disabled \(default identity\)/i }));
        expect(canvas.queryByText("Allowed MCP-client redirect URIs")).not.toBeInTheDocument();

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        const banner = await canvas.findByTestId("messageBanner");
        expect(banner).toHaveTextContent("Cannot save");
        expect(banner).toHaveTextContent("Allowed MCP-client redirect URIs");
        expect(lastCreateBody).toBeNull();

        await userEvent.click(within(banner).getByRole("button", { name: /dismiss/i }));
        expect(canvas.queryByTestId("messageBanner")).not.toBeInTheDocument();
    },
};
