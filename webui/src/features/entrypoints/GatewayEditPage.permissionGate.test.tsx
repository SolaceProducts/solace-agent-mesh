// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen, waitFor } from "@testing-library/react";
import { type useLoaderData, useLocation } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { gatewaysApi } from "@/api/gateways";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { Gateway, GatewaySchema } from "@/types";

import { GatewayEditPage } from "./GatewayEditPage";

expect.extend(matchers);

vi.mock("@/api/capabilities", async importActual => ({
    ...(await importActual<typeof import("@/api/capabilities")>()),
    useCapabilities: () => ({ data: [] }),
    findGatewayBadge: () => null,
}));

vi.mock("@/common/dialogs", async importOriginal => ({
    ...(await importOriginal<typeof import("@/common/dialogs")>()),
    useNavigationBlocker: () => ({ NavigationBlocker: () => null, allowNavigation: (fn: () => void) => fn(), setBlockingEnabled: () => {} }),
}));

const mockGet = vi.spyOn(gatewaysApi, "get");

const webhookSchemas: GatewaySchema[] = [{ type: "webhook", name: "Webhook", description: "Webhook gateway", schema: {}, yamlTemplatePath: "", requiredEnvVars: [] }];

const existingGateway: Gateway = { id: "gw-1", name: "Existing Entrypoint", description: "", type: "webhook", values: {} };

const LocationProbe = () => <div data-testid="pathname">{useLocation().pathname}</div>;

const renderPageAs = (granted: Capability[], isNew: boolean) => {
    const held = new Set<string>(granted);
    mockGet.mockResolvedValue(existingGateway);
    window.location.hash = isNew ? "#/entrypoints/new/edit?type=webhook" : "#/entrypoints/gw-1/edit";
    renderWithProviders(
        <>
            <GatewayEditPage />
            <LocationProbe />
        </>,
        {
            initialPath: isNew ? "/entrypoints/new/edit" : "/entrypoints/gw-1/edit",
            chatContextValues: {
                gatewaySchemas: webhookSchemas,
                useLoaderData: (() => ({ gatewayId: isNew ? undefined : "gw-1", isNew })) as unknown as typeof useLoaderData,
            },
            userContextValues: { userHasCapability: (capability: Capability) => held.has(capability) },
        }
    );
};

describe("GatewayEditPage permission gate", () => {
    afterEach(() => vi.clearAllMocks());

    test("a create-only user opening the edit URL is redirected to the entrypoint list", async () => {
        renderPageAs([CAPABILITIES.createGateways], false);

        await waitFor(() => expect(screen.getByTestId("pathname")).toHaveTextContent(/^\/entrypoints$/));
        expect(screen.queryByTestId("saveGatewayButton")).not.toBeInTheDocument();
    });

    test("an update-only user opening the create URL is redirected to the entrypoint list", async () => {
        renderPageAs([CAPABILITIES.updateGateways], true);

        await waitFor(() => expect(screen.getByTestId("pathname")).toHaveTextContent(/^\/entrypoints$/));
        expect(screen.queryByTestId("saveGatewayButton")).not.toBeInTheDocument();
    });

    test("an update user still gets the edit form", async () => {
        renderPageAs([CAPABILITIES.updateGateways], false);

        await expect(screen.findByTestId("saveGatewayButton")).resolves.toBeInTheDocument();
        expect(screen.getByTestId("pathname")).toHaveTextContent(/^\/entrypoints\/gw-1\/edit$/);
    });

    test("a create user still gets the create form", async () => {
        renderPageAs([CAPABILITIES.createGateways], true);

        await expect(screen.findByTestId("saveGatewayButton")).resolves.toBeInTheDocument();
        expect(screen.getByTestId("pathname")).toHaveTextContent(/^\/entrypoints\/new\/edit$/);
    });
});
