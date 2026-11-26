// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { GatewayEditPage } from "@/features/entrypoints/GatewayEditPage";

import { mockGateways, mockGatewaySchemas } from "@/testing/data";
import { mockEntrypointV2, savedEntrypointV2, toEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";
import type { Gateway } from "@/types";

const handlersEdit = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-6", () => HttpResponse.json({ data: mockEntrypointV2("gateway-6") })),
    http.patch("*/api/v2/platform/entrypoints/gateway-6", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-6", await request.json()) }))
);
const handlersNew = withDefaults(http.post("*/api/v2/platform/entrypoints", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-new", await request.json()) })));

const handlersTestConnection = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test", () => {
        const testGateway: Gateway = {
            id: "gateway-test",
            name: "Test Connection Gateway",
            description: "Gateway with pre-filled test credentials",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "tcps://broker.example.test:55443",
                broker_vpn: "example-vpn",
                broker_username: "example-client",
                broker_password: "example-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: true,
                message: "Connection established and authenticated successfully",
            },
        });
    })
);

const handlersTestConnectionBadCredentials = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test-bad", () => {
        const testGateway: Gateway = {
            id: "gateway-test-bad",
            name: "Test Connection Gateway - Bad Credentials",
            description: "Gateway with bad test credentials",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "tcps://broker.example.test:55443",
                broker_vpn: "example-vpn",
                broker_username: "bad-username",
                broker_password: "bad-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test-bad", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test-bad", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: false,
                message: "Authentication failed - invalid username or password",
            },
        });
    })
);

const handlersTestConnectionBadVpn = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test-bad-vpn", () => {
        const testGateway: Gateway = {
            id: "gateway-test-bad-vpn",
            name: "Test Connection Gateway - Bad VPN",
            description: "Gateway with bad VPN name",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "tcps://broker.example.test:55443",
                broker_vpn: "bad-vpn",
                broker_username: "example-client",
                broker_password: "example-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test-bad-vpn", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test-bad-vpn", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: false,
                message: "Invalid Message VPN",
            },
        });
    })
);

const handlersTestConnectionBadUrl = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test-bad-url", () => {
        const testGateway: Gateway = {
            id: "gateway-test-bad-url",
            name: "Test Connection Gateway - Bad URL",
            description: "Gateway with bad URL",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "badurl",
                broker_vpn: "example-vpn",
                broker_username: "example-client",
                broker_password: "example-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test-bad-url", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test-bad-url", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: false,
                message: "Host resolution failed - check broker URL",
            },
        });
    })
);

const handlersTestConnectionTimeout = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test-timeout", () => {
        const testGateway: Gateway = {
            id: "gateway-test-timeout",
            name: "Test Connection Gateway - Timeout",
            description: "Gateway for connection timeout testing",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "tcps://unreachable-broker.com:55443",
                broker_vpn: "example-vpn",
                broker_username: "example-client",
                broker_password: "example-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test-timeout", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test-timeout", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: false,
                message: "Connection timeout - broker may be unreachable",
            },
        });
    })
);

const handlersTestConnectionRefused = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test-refused", () => {
        const testGateway: Gateway = {
            id: "gateway-test-refused",
            name: "Test Connection Gateway - Refused",
            description: "Gateway for connection refused testing",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "tcps://blocked-broker.com:55443",
                broker_vpn: "example-vpn",
                broker_username: "example-client",
                broker_password: "example-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test-refused", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test-refused", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: false,
                message: "Connection refused by broker",
            },
        });
    })
);

const handlersTestConnectionSSLError = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test-ssl", () => {
        const testGateway: Gateway = {
            id: "gateway-test-ssl",
            name: "Test Connection Gateway - SSL Error",
            description: "Gateway for SSL/TLS error testing",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "tcps://ssl-error-broker.com:55443",
                broker_vpn: "example-vpn",
                broker_username: "example-client",
                broker_password: "example-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test-ssl", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test-ssl", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: false,
                message: "TLS/SSL connection failed",
            },
        });
    })
);

const handlersTestConnectionFailed = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-test-failed", () => {
        const testGateway: Gateway = {
            id: "gateway-test-failed",
            name: "Test Connection Gateway - Failed",
            description: "Gateway for failed connection state testing",
            type: "event_mesh",
            namespace: "default",
            values: {
                broker_url: "tcps://failed-state-broker.com:55443",
                broker_vpn: "example-vpn",
                broker_username: "example-client",
                broker_password: "example-password",
                event_rules: [],
            },
            deploymentStatus: "not_deployed",
            createdBy: "test@example.com",
            updatedBy: "test@example.com",
            createdAt: "2024-01-01T00:00:00Z",
            updatedAt: "2024-01-01T00:00:00Z",
        };
        return HttpResponse.json({ data: toEntrypointV2(testGateway) });
    }),
    http.patch("*/api/v2/platform/entrypoints/gateway-test-failed", async ({ request }) => HttpResponse.json({ data: savedEntrypointV2("gateway-test-failed", await request.json()) })),
    http.put("*/api/v2/platform/entrypoints/testConnection", () => {
        return HttpResponse.json({
            data: {
                success: false,
                message: "Failed to establish connection to broker",
            },
        });
    })
);

const meta = {
    title: "Entrypoints/GatewayEditPage/Event Mesh",
    component: GatewayEditPage,
    parameters: {
        docs: {
            description: {
                component: "The gateway edit page component for creating and editing Event Mesh gateway configurations.",
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
                if (!hash.includes("?type=event_mesh")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=event_mesh" : hash + "?type=event_mesh";
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

        await canvas.findByText("Event Mesh");

        expect(canvas.getByText("Entrypoints")).toBeInTheDocument();

        expect(canvas.getByText("Type")).toBeInTheDocument();

        expect(canvas.getByText("Name")).toBeInTheDocument();
        expect(canvas.getByText("Description")).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Configuration")).toBeInTheDocument();

        await canvas.findByText("Data Plane Broker URL");
        expect(canvas.getByText("VPN Name")).toBeInTheDocument();
        expect(canvas.getByText("Username")).toBeInTheDocument();
        expect(canvas.getByText("Password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByText("Test Connection");
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeDisabled();

        expect(canvas.getByText("Event Rules")).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: /Add Rule/i })).toBeInTheDocument();
    },
};

export const TestConnectionSuccess: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnection,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("tcps://broker.example.test:55443");

        expect(canvas.getByDisplayValue("example-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-client")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const successBanner = await canvas.findByText("Connection established and authenticated successfully");
        expect(successBanner).toBeInTheDocument();
    },
};

export const TestConnectionBadUsernameOrPassword: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnectionBadCredentials,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test-bad", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("tcps://broker.example.test:55443");

        expect(canvas.getByDisplayValue("example-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("bad-username")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("bad-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const errorBanner = await canvas.findByText("Authentication failed - invalid username or password");
        expect(errorBanner).toBeInTheDocument();
    },
};

export const TestConnectionBadVpn: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnectionBadVpn,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test-bad-vpn", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("tcps://broker.example.test:55443");

        expect(canvas.getByDisplayValue("bad-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-client")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const errorBanner = await canvas.findByText("Invalid Message VPN");
        expect(errorBanner).toBeInTheDocument();
    },
};

export const TestConnectionBadURL: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnectionBadUrl,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test-bad-url", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("badurl");

        expect(canvas.getByDisplayValue("badurl")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-client")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const errorBanner = await canvas.findByText("Host resolution failed - check broker URL");
        expect(errorBanner).toBeInTheDocument();
    },
};

export const TestConnectionTimeout: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnectionTimeout,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test-timeout", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("tcps://unreachable-broker.com:55443");

        expect(canvas.getByDisplayValue("tcps://unreachable-broker.com:55443")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-client")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const errorBanner = await canvas.findByText("Connection timeout - broker may be unreachable");
        expect(errorBanner).toBeInTheDocument();
    },
};

export const TestConnectionRefused: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnectionRefused,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test-refused", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("tcps://blocked-broker.com:55443");

        expect(canvas.getByDisplayValue("tcps://blocked-broker.com:55443")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-client")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const errorBanner = await canvas.findByText("Connection refused by broker");
        expect(errorBanner).toBeInTheDocument();
    },
};

export const TestConnectionSSLTLSError: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnectionSSLError,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test-ssl", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("tcps://ssl-error-broker.com:55443");

        expect(canvas.getByDisplayValue("tcps://ssl-error-broker.com:55443")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-client")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const errorBanner = await canvas.findByText("TLS/SSL connection failed");
        expect(errorBanner).toBeInTheDocument();
    },
};

export const TestConnectionFailed: Story = {
    parameters: {
        msw: {
            handlers: handlersTestConnectionFailed,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-test-failed", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByDisplayValue("tcps://failed-state-broker.com:55443");

        expect(canvas.getByDisplayValue("tcps://failed-state-broker.com:55443")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-vpn")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-client")).toBeInTheDocument();
        expect(canvas.getByDisplayValue("example-password")).toBeInTheDocument();

        const testConnectionButton = await canvas.findByRole("button", { name: "Test Connection" });
        expect(testConnectionButton).toBeInTheDocument();
        expect(testConnectionButton).toBeEnabled();

        await testConnectionButton.click();

        const errorBanner = await canvas.findByText("Failed to establish connection to broker");
        expect(errorBanner).toBeInTheDocument();
    },
};

export const Edit: Story = {
    parameters: {
        msw: {
            handlers: handlersEdit,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-6", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        expect(canvas.getByText("Entrypoints")).toBeInTheDocument();

        const gateway = mockGateways.find(g => g.id === "gateway-6")!;
        const textBoxes = await canvas.findAllByRole("textbox");

        const nameInput = textBoxes[0] as HTMLInputElement;
        expect(nameInput.value).toBe(gateway.name);

        const descriptionInput = textBoxes[1] as HTMLInputElement;
        expect(descriptionInput.value).toBe(gateway.description);

        expect(canvas.getByText("Event Rules")).toBeInTheDocument();
        expect(canvas.getByText("order_processor")).toBeInTheDocument();
    },
};

export const AddEventRule: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=event_mesh")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=event_mesh" : hash + "?type=event_mesh";
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

        await canvas.findByText("Event Mesh");

        expect(canvas.getByText("Entrypoints")).toBeInTheDocument();

        const addRuleButton = await canvas.findByRole("button", { name: /Add Rule/i });
        await userEvent.click(addRuleButton);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();

        expect(within(dialog).getByText("Add Event Rule")).toBeInTheDocument();
    },
};

export const AddEventRuleWithValidation: Story = {
    parameters: {
        msw: {
            handlers: handlersNew,
        },
        chatContext: {
            useLoaderData: () => {
                const hash = window.location.hash;
                if (!hash.includes("?type=event_mesh")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=event_mesh" : hash + "?type=event_mesh";
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

        await canvas.findByText("Event Mesh");

        expect(canvas.getByText("Entrypoints")).toBeInTheDocument();

        const addRuleButton = await canvas.findByRole("button", { name: /Add Rule/i });
        await userEvent.click(addRuleButton);

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Add Event Rule")).toBeInTheDocument();

        await userEvent.click(await within(dialog).findByTestId("saveEventRule"));

        expect(await within(dialog).findByText("Name is required")).toBeInTheDocument();
    },
};

export const EditEventRule: Story = {
    parameters: {
        msw: {
            handlers: handlersEdit,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-6", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        const ruleNameButton = await canvas.findByRole("button", { name: "order_processor" });
        expect(ruleNameButton).toBeInTheDocument();

        await userEvent.click(ruleNameButton);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(within(dialog).getByText("Edit Event Rule")).toBeInTheDocument();

        expect(within(dialog).getByDisplayValue("order_processor")).toBeInTheDocument();
    },
};

export const CloneEventRule: Story = {
    parameters: {
        msw: {
            handlers: handlersEdit,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-6", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByText("order_processor");

        const cloneButtons = await canvas.findAllByRole("button", { name: /clone/i });
        expect(cloneButtons.length).toBeGreaterThan(0);
        await userEvent.click(cloneButtons[0]);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(within(dialog).getByText("Clone order_processor")).toBeInTheDocument();

        expect(within(dialog).getByDisplayValue(/order_processor/i)).toBeInTheDocument();
    },
};

export const DeleteEventRule: Story = {
    parameters: {
        msw: {
            handlers: handlersEdit,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-6", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        await canvas.findByText("order_processor");

        const deleteButtons = await canvas.findAllByRole("button", { name: /delete/i });
        expect(deleteButtons.length).toBeGreaterThan(0);
        await userEvent.click(deleteButtons[0]);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();

        expect(within(dialog).getByText(/order_processor/i)).toBeInTheDocument();
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
                if (!hash.includes("?type=event_mesh")) {
                    window.location.hash = hash.includes("?") ? hash + "&type=event_mesh" : hash + "?type=event_mesh";
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

        await canvas.findByText("Event Mesh");

        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        expect(await canvas.findByText("Name must be at least 3 characters")).toBeInTheDocument();
        expect(await canvas.findByText("Description must be at least 10 characters")).toBeInTheDocument();
    },
};

export const EditVisibleErrorShowsInlineNotBanner: Story = {
    parameters: {
        msw: {
            handlers: handlersEdit,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-6", isNew: false }),
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

        await canvas.findByText("Event Mesh");

        const textBoxes = await canvas.findAllByRole("textbox");
        const nameInput = textBoxes[0] as HTMLInputElement;
        await userEvent.clear(nameInput);
        await userEvent.click(canvas.getByTestId("saveGatewayButton"));

        await canvas.findByText("Name must be at least 3 characters");
        expect(canvas.queryByTestId("messageBanner")).not.toBeInTheDocument();
    },
};
