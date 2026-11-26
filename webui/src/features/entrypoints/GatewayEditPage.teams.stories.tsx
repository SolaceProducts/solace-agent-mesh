// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, waitFor, within } from "storybook/test";

import { GatewayEditPage } from "@/features/entrypoints/GatewayEditPage";

import { mockGatewaySchemas } from "@/testing/data";
import { mockEntrypointV2, savedEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";

const handlersEdit = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-2", () => HttpResponse.json({ data: mockEntrypointV2("gateway-2") })),
    http.patch("*/api/v2/platform/entrypoints/gateway-2", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-2", await request.json()) }))
);

const handlersNew = withDefaults(http.post("*/api/v2/platform/entrypoints", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-teams-new", await request.json()) })));

const meta = {
    title: "Entrypoints/GatewayEditPage/Teams",
    component: GatewayEditPage,
    parameters: {
        docs: {
            description: {
                component: "The gateway edit page component for creating and editing Teams gateway configurations in cloud deployments.",
            },
        },
    },
} satisfies Meta<typeof GatewayEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CreateNewTeamsGateway: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=teams")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=teams" : hash + "?type=teams";
                }
                return { isNew: true };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Microsoft Teams");

        const headers = canvas.getAllByText("Create Entrypoint");
        expect(headers.length).toBeGreaterThan(0);

        await waitFor(() => {
            expect(canvas.queryByTestId("teams-gateway-setup-banner")).toBeInTheDocument();
        });

        expect(canvas.getByRole("button", { name: "Create" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Create and Deploy" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Discard Changes" })).toBeInTheDocument();
    },
};

export const EditExistingTeamsGateway: Story = {
    parameters: {
        msw: {
            handlers: handlersEdit,
        },
        chatContext: {
            useLoaderData: () => {
                return { gatewayId: "gateway-2", isNew: false };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByDisplayValue("Teams Gateway - Deployed & Out of Sync");

        const editHeaders = canvas.getAllByText(/Edit Teams Gateway/i);
        expect(editHeaders.length).toBeGreaterThan(0);

        expect(canvas.getByRole("button", { name: "Save" })).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: "Save and Deploy" })).toBeInTheDocument();
    },
};

export const CreateButtonPresent: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=teams")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=teams" : hash + "?type=teams";
                }
                return { isNew: true };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Microsoft Teams");

        const createButton = canvas.getByRole("button", { name: "Create" });
        expect(createButton).toBeInTheDocument();
        expect(createButton).toBeEnabled();
    },
};

export const DiscardChanges: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=teams")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=teams" : hash + "?type=teams";
                }
                return { isNew: true };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Microsoft Teams");

        const discardButton = canvas.getByRole("button", { name: "Discard Changes" });
        await userEvent.click(discardButton);
    },
};

export const EditTeamsGatewayPreservesPublicUrl: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-2", () => HttpResponse.json({ data: mockEntrypointV2("gateway-2", { publicUrl: "https://teams-gateway.example.test" }) })),
                http.patch("*/api/v2/platform/entrypoints/gateway-2", async ({ request }) => {
                    const body = (await request.json()) as unknown;

                    if (typeof body === "object" && body !== null && "publicUrl" in body) {
                        expect(body.publicUrl).toBe("https://teams-gateway.example.test");
                    }

                    return HttpResponse.json({ data: savedEntrypointV2("gateway-2", body) });
                })
            ),
        },
        chatContext: {
            useLoaderData: () => {
                return { gatewayId: "gateway-2", isNew: false };
            },
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                console.log(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                console.log(`Notification: ${message}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByDisplayValue("Teams Gateway - Deployed & Out of Sync");

        const nameInput = canvas.getByDisplayValue("Teams Gateway - Deployed & Out of Sync");
        await userEvent.clear(nameInput);
        await userEvent.type(nameInput, "Teams Gateway - Updated Name");

        const saveButton = canvas.getByRole("button", { name: "Save" });
        await userEvent.click(saveButton);

        await waitFor(() => {});
    },
};
