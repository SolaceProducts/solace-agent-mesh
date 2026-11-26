// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import type { useLoaderData } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { gatewaysApi } from "@/api/gateways";

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

const mockGet = vi.spyOn(gatewaysApi, "get");

const eventMeshSchemas: GatewaySchema[] = [{ type: "event_mesh", name: "Event Mesh", description: "Event mesh gateway", schema: {}, yamlTemplatePath: "", requiredEnvVars: [] }];

const renderPage = (gateway: Gateway) => {
    mockGet.mockResolvedValue(gateway);
    renderWithProviders(<GatewayViewPage />, {
        chatContextValues: {
            gatewaySchemas: eventMeshSchemas,
            useLoaderData: (() => ({ gatewayId: gateway.id })) as unknown as typeof useLoaderData,
        },
    });
};

describe("GatewayViewPage consume-only notice", () => {
    afterEach(() => vi.clearAllMocks());

    test("renders the notice card for an event-mesh entrypoint with existingQueueName", async () => {
        renderPage({
            id: "gw-consume",
            name: "Trades Consumer",
            description: "",
            type: "event_mesh",
            values: {
                event_rules: [{ name: "trades", existingQueueName: "preprov/q/trades" }],
            },
        });

        await expect(screen.findByTestId("consumeOnlyEntrypointNotice")).resolves.toBeInTheDocument();
        expect(screen.getByRole("button", { name: /go to entrypoints/i })).toBeInTheDocument();
    });

    test("renders the normal view when no rule carries existingQueueName", async () => {
        renderPage({
            id: "gw-normal",
            name: "Normal Entrypoint",
            description: "",
            type: "event_mesh",
            values: {
                event_rules: [{ name: "r1", subscriptions: [{ topic: "a/b" }] }],
            },
        });

        await expect(screen.findByRole("tab", { name: "Configuration" })).resolves.toBeInTheDocument();
        expect(screen.queryByTestId("consumeOnlyEntrypointNotice")).not.toBeInTheDocument();
    });
});
