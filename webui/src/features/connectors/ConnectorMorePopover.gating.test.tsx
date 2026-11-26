// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockConnectors } from "@/testing/data/connectors";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ConnectorMorePopover } from "./ConnectorMorePopover";

expect.extend(matchers);

const renderWithCapabilities = (granted: string[]) => {
    const held = new Set(granted);
    renderWithProviders(
        <ConnectorMorePopover connector={mockConnectors[0]}>
            <button data-testid="moreTrigger">More</button>
        </ConnectorMorePopover>,
        { userContextValues: { userHasCapability: (cap: string) => held.has(cap) } }
    );
};

describe("ConnectorMorePopover zero-item gating", () => {
    test("renders nothing when the user can neither update nor delete", () => {
        renderWithCapabilities([CAPABILITIES.readConnectors]);
        expect(screen.queryByTestId("moreTrigger")).not.toBeInTheDocument();
    });

    test("renders the trigger when the user can update", () => {
        renderWithCapabilities([CAPABILITIES.updateConnectors]);
        expect(screen.getByTestId("moreTrigger")).toBeInTheDocument();
    });

    test("renders the trigger when the user can delete", () => {
        renderWithCapabilities([CAPABILITIES.deleteConnectors]);
        expect(screen.getByTestId("moreTrigger")).toBeInTheDocument();
    });
});

describe("ConnectorMorePopover per-verb menu items", () => {
    const openMenu = async () => {
        await userEvent.click(screen.getByTestId("moreTrigger"));
        return screen.findByRole("dialog");
    };

    test("update alone exposes Edit without Delete", async () => {
        renderWithCapabilities([CAPABILITIES.updateConnectors]);
        const menu = await openMenu();

        expect(await screen.findByText("Edit")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Delete");
    });

    test("delete alone exposes Delete without Edit", async () => {
        renderWithCapabilities([CAPABILITIES.deleteConnectors]);
        const menu = await openMenu();

        expect(await screen.findByText("Delete")).toBeInTheDocument();
        expect(menu).not.toHaveTextContent("Edit");
    });
});
