// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { GatewayEditPage } from "@/features/entrypoints/GatewayEditPage";

import { mockGateways, mockGatewaySchemas } from "@/testing/data";
import { mockEntrypointV2, savedEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";

const handlersEdit = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-1", () => HttpResponse.json({ data: mockEntrypointV2("gateway-1") })),
    http.patch("*/api/v2/platform/entrypoints/gateway-1", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-1", await request.json()) }))
);

const handlersNew = withDefaults(http.post("*/api/v2/platform/entrypoints", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-new", await request.json()) })));

const meta = {
    title: "Entrypoints/GatewayEditPage/Slack",
    component: GatewayEditPage,
    parameters: {
        docs: {
            description: {
                component: "The gateway edit page component for creating and editing Slack gateway configurations.",
            },
        },
    },
} satisfies Meta<typeof GatewayEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=slack")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=slack" : hash + "?type=slack";
                }
                return { isNew: true };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                alert(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Slack");

        expect(canvas.getByText("Entrypoints")).toBeInTheDocument();

        expect(canvas.getByText("Type")).toBeInTheDocument();
        expect(canvas.getByText("Slack")).toBeInTheDocument();

        expect(canvas.getByText("Name")).toBeInTheDocument();
        expect(canvas.getByText("Description")).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Configuration")).toBeInTheDocument();
        expect(canvas.getByText("Bot Token")).toBeInTheDocument();
        expect(canvas.getByText("App-Level Token")).toBeInTheDocument();
    },
};

export const ValidationEmptySubmit: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=slack")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=slack" : hash + "?type=slack";
                }
                return { isNew: true };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                alert(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Slack");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        expect(await canvas.findByText("Name must be at least 3 characters")).toBeInTheDocument();
        expect(await canvas.findByText("Description must be at least 10 characters")).toBeInTheDocument();
        const passwordErrors = await canvas.findAllByText("Bot Token is required");
        expect(passwordErrors).toHaveLength(1);
        expect(await canvas.findByText("App-Level Token is required")).toBeInTheDocument();
    },
};

export const ValidationShortName: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=slack")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=slack" : hash + "?type=slack";
                }
                return { isNew: true };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                alert(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Slack");

        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[0], "ab");
        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        expect(await canvas.findByText("Name must be at least 3 characters")).toBeInTheDocument();
    },
};

export const ValidationShortDescription: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=slack")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=slack" : hash + "?type=slack";
                }
                return { isNew: true };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                alert(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Slack");

        const textboxes = await canvas.findAllByRole("textbox");
        await userEvent.type(textboxes[1], "too short");
        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        expect(await canvas.findByText("Description must be at least 10 characters")).toBeInTheDocument();
    },
};

export const Edit: Story = {
    parameters: {
        msw: {
            handlers: handlersEdit,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-1", isNew: false }),
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                alert(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Slack")).toBeInTheDocument();

        const gateway = mockGateways.find(g => g.id === "gateway-1")!;
        const textBoxes = await canvas.findAllByRole("textbox");

        const nameInput = textBoxes[0] as HTMLInputElement;
        expect(nameInput).toBeInTheDocument();
        expect(nameInput.value).toBe(gateway.name);

        const descriptionInput = textBoxes[1] as HTMLInputElement;
        expect(descriptionInput).toBeInTheDocument();
        expect(descriptionInput.value).toBe(gateway.description);
    },
};
