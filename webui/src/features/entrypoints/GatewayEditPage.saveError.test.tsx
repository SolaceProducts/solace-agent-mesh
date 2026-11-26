// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { useLoaderData } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "@/api/common/v2";
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

const schemas: GatewaySchema[] = [{ type: "webhook", name: "Webhook", description: "Webhook entrypoint", schema: {}, yamlTemplatePath: "", requiredEnvVars: [] }];

const stored: Gateway = {
    id: "gw-1",
    name: "Orders Entrypoint",
    description: "Routes order events to agents.",
    type: "webhook",
    deploymentStatus: "not_deployed",
    values: {},
};

const renderPage = () => {
    vi.spyOn(gatewaysApi, "get").mockResolvedValue(stored);
    const held = new Set<string>([CAPABILITIES.updateGateways, CAPABILITIES.deployGateways]);
    renderWithProviders(<GatewayEditPage />, {
        chatContextValues: {
            gatewaySchemas: schemas,
            useLoaderData: (() => ({ gatewayId: "gw-1", isNew: false })) as unknown as typeof useLoaderData,
        },
        userContextValues: { userHasCapability: (capability: Capability) => held.has(capability) },
    });
};

describe("GatewayEditPage save errors for entrypoint types outside the form kit", () => {
    afterEach(() => vi.restoreAllMocks());

    test("a rejected Save opens the Entrypoint Save Failed dialog", async () => {
        vi.spyOn(gatewaysApi, "save").mockRejectedValue(new ApiV2Error(422, "Invalid entrypoint"));
        renderPage();

        await userEvent.click(await screen.findByTestId("saveGatewayButton"));
        expect(await screen.findByText("Entrypoint Save Failed")).toBeInTheDocument();
    });

    test("a rejected Save and Deploy opens the Entrypoint Save Failed dialog", async () => {
        vi.spyOn(gatewaysApi, "save").mockRejectedValue(new ApiV2Error(422, "Invalid entrypoint"));
        renderPage();

        await userEvent.click(await screen.findByTestId("saveAndDeployGatewayButton"));
        await userEvent.click(await screen.findByTestId("dialogConfirmButton"));

        expect(await screen.findByText("Entrypoint Save Failed")).toBeInTheDocument();
    });
});
