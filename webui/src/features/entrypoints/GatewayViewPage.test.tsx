// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { fireEvent, screen } from "@testing-library/react";
import type { useLoaderData } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { gatewaysApi } from "@/api/gateways";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Gateway, GatewaySchema } from "@/types";

import { GatewayViewPage } from "./GatewayViewPage";

expect.extend(matchers);

vi.mock("./hooks", async importOriginal => {
    const actual = await importOriginal<typeof import("./hooks")>();
    return {
        ...actual,
        useGatewayDeployment: () => ({ deploy: vi.fn(), error: null, setError: vi.fn(), isLoading: false }),
        useGatewayNetworkingDetails: () => ({ networkingDetails: null, isLoading: false, error: null, refetch: vi.fn() }),
    };
});

vi.mock("@/api/capabilities", async importActual => ({
    ...(await importActual<typeof import("@/api/capabilities")>()),
    useCapabilities: () => ({ data: [] }),
    findGatewayBadge: () => null,
}));

const realGet = gatewaysApi.get;
const mockGet = vi.spyOn(gatewaysApi, "get");

const nonWebhookSchemas: GatewaySchema[] = [{ type: "slack", name: "Slack", description: "Slack gateway", schema: {}, yamlTemplatePath: "", requiredEnvVars: [] }];

const mockGateway: Gateway = {
    id: "gw-no-schema",
    name: "My Webhook Gateway",
    description: "Gateway whose type the deployment does not serve a schema for",
    type: "webhook",
    values: {},
};

const googleChatSchemas: GatewaySchema[] = [{ type: "google_chat", name: "Google Chat", description: "Google Chat gateway", schema: {}, yamlTemplatePath: "", requiredEnvVars: [] }];

const renderGoogleChat = (values: Record<string, unknown>) => {
    mockGet.mockResolvedValue({ id: "gw-chat", name: "HR Chat", description: "", type: "google_chat", values });
    renderWithProviders(<GatewayViewPage />, {
        chatContextValues: {
            gatewaySchemas: googleChatSchemas,
            useLoaderData: (() => ({ gatewayId: "gw-chat" })) as unknown as typeof useLoaderData,
        },
    });
};

describe("GatewayViewPage network tab for Google Chat", () => {
    test.each([
        ["https delivery", { delivery: "https" }],
        ["the default delivery", {}],
    ])("shows the Network tab for %s", async (_name, values) => {
        renderGoogleChat(values);
        await expect(screen.findByRole("tab", { name: "Network" })).resolves.toBeInTheDocument();
    });

    test.each([
        ["Pub/Sub delivery", { delivery: "pubsub" }],
        ["Pub/Sub delivery in another case", { delivery: " PubSub" }],
        ["a row stored with only a subscription", { pubsub_subscription: "projects/p/subscriptions/s" }],
    ])("hides the Network tab for %s", async (_name, values) => {
        renderGoogleChat(values);
        await screen.findByRole("tab", { name: "Configuration" });
        expect(screen.queryByRole("tab", { name: "Network" })).not.toBeInTheDocument();
    });
});

describe("GatewayViewPage no-schema fallback", () => {
    beforeEach(() => {
        mockGet.mockResolvedValue(mockGateway);
    });

    test("renders the unavailable-type EmptyState when the gateway type has no matching schema", async () => {
        renderWithProviders(<GatewayViewPage />, {
            chatContextValues: {
                gatewaySchemas: nonWebhookSchemas,
                useLoaderData: (() => ({ gatewayId: "gw-no-schema" })) as unknown as typeof useLoaderData,
            },
        });

        await expect(screen.findByText("This entrypoint type is not available on this deployment.")).resolves.toBeInTheDocument();
    });
});

describe("GatewayViewPage on /api/v2", () => {
    afterEach(() => vi.unstubAllGlobals());

    test("loads the entrypoint from /api/v2", async () => {
        const entrypoint = {
            id: "gw-no-schema",
            type: "entrypoint",
            entrypointType: "webhook",
            slug: "hook",
            name: "My Webhook Gateway",
            description: "",
            values: {},
            deploymentStatus: "not_deployed",
            syncStatus: null,
            deployed: null,
            runtimeStatus: { status: "unknown", problems: [] },
            createdBy: "u",
            updatedBy: "u",
            createdTime: null,
            updatedTime: null,
        };
        const fetchMock = vi.fn<typeof fetch>(async () => new Response(JSON.stringify({ data: entrypoint })));
        vi.stubGlobal("fetch", fetchMock);
        mockGet.mockImplementation(realGet);

        renderWithProviders(<GatewayViewPage />, {
            chatContextValues: {
                gatewaySchemas: nonWebhookSchemas,
                useLoaderData: (() => ({ gatewayId: "gw-no-schema" })) as unknown as typeof useLoaderData,
            },
        });

        await expect(screen.findByText("This entrypoint type is not available on this deployment.")).resolves.toBeInTheDocument();
        expect(String(fetchMock.mock.calls[0][0])).toMatch(/\/api\/v2\/platform\/entrypoints\/gw-no-schema$/);
    });
});

describe("GatewayViewPage Deployment History tab", () => {
    const slackGateway: Gateway = { id: "gw-slack", name: "My Slack Gateway", description: "Slack gateway", type: "slack", values: {} };
    const getDeployments = vi.spyOn(gatewaysApi, "getDeployments");

    beforeEach(() => {
        mockGet.mockResolvedValue(slackGateway);
        getDeployments.mockResolvedValue([]);
    });
    afterEach(() => vi.clearAllMocks());

    const renderPage = (userHasCapability?: (capability: string) => boolean) =>
        renderWithProviders(<GatewayViewPage />, {
            userContextValues: userHasCapability ? { userHasCapability } : undefined,
            chatContextValues: {
                gatewaySchemas: nonWebhookSchemas,
                useLoaderData: (() => ({ gatewayId: "gw-slack" })) as unknown as typeof useLoaderData,
            },
        });

    test("is hidden without deployment:_:read", async () => {
        renderPage(capability => capability !== CAPABILITIES.readGatewayDeployments);

        await expect(screen.findByRole("tab", { name: "Configuration" })).resolves.toBeInTheDocument();
        expect(screen.queryByRole("tab", { name: "Deployment History" })).not.toBeInTheDocument();
    });

    test("loads the history when shown", async () => {
        renderPage();

        fireEvent.click(await screen.findByRole("tab", { name: "Deployment History" }));

        await vi.waitFor(() => expect(getDeployments).toHaveBeenCalledWith("gw-slack"));
    });
});
