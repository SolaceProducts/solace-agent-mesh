// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { mockGateways } from "@/testing/data";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { GatewayMorePopover } from "./GatewayMorePopover";

expect.extend(matchers);

const outOfSyncGateway = mockGateways[1];

const openMenuWith = async (granted: Capability[]) => {
    const held = new Set<string>(granted);
    renderWithProviders(
        <GatewayMorePopover gateway={outOfSyncGateway} onEdit={() => {}} onOpenGateway={() => {}} onUpdate={() => {}}>
            <button data-testid="moreTrigger">More</button>
        </GatewayMorePopover>,
        { userContextValues: { userHasCapability: (capability: Capability) => held.has(capability) } }
    );
    await userEvent.click(screen.getByTestId("moreTrigger"));
    return screen.findByRole("dialog");
};

describe("GatewayMorePopover per-verb menu items", () => {
    test("a reader gets Open Entrypoint and nothing else", async () => {
        const menu = await openMenuWith([CAPABILITIES.readGateways]);

        expect(await screen.findByText("Open Entrypoint")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Edit");
        expect(menu).not.toHaveTextContent("Delete");
        expect(menu).not.toHaveTextContent("Deploy Updates");
        expect(menu).not.toHaveTextContent("Undeploy");
    });

    test("update alone exposes Edit only", async () => {
        const menu = await openMenuWith([CAPABILITIES.updateGateways]);

        expect(await screen.findByText("Edit")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Delete");
        expect(menu).not.toHaveTextContent("Undeploy");
    });

    test("delete alone exposes Delete only", async () => {
        const menu = await openMenuWith([CAPABILITIES.deleteGateways]);

        expect(await screen.findByText("Delete")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Edit");
        expect(menu).not.toHaveTextContent("Undeploy");
    });

    test("deploy alone exposes the deployment actions only", async () => {
        const menu = await openMenuWith([CAPABILITIES.deployGateways]);

        expect(await screen.findByText("Deploy Updates")).toBeInTheDocument();
        expect(screen.getByText("Undeploy")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Edit");
        expect(menu).not.toHaveTextContent("Delete");
    });
});
