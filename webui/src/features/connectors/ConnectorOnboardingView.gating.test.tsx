// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ConnectorOnboardingView } from "./ConnectorOnboardingView";

expect.extend(matchers);

const DESCRIPTION = "Set up integrations to databases, APIs, or SaaS tools.";

const renderWithCapabilities = (granted: string[]) => {
    const held = new Set(granted);
    renderWithProviders(<ConnectorOnboardingView title="Connectors" description={DESCRIPTION} />, {
        userContextValues: { userHasCapability: (cap: string) => held.has(cap) },
    });
};

describe("ConnectorOnboardingView create gating", () => {
    test("hides the create call-to-action from a user who can only read connectors", () => {
        renderWithCapabilities([CAPABILITIES.readConnectors]);

        expect(screen.getByText(DESCRIPTION)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "See Available Connectors" })).not.toBeInTheDocument();
    });

    test("shows the create call-to-action to a user who can create connectors", () => {
        renderWithCapabilities([CAPABILITIES.readConnectors, CAPABILITIES.createConnectors]);

        expect(screen.getByRole("button", { name: "See Available Connectors" })).toBeInTheDocument();
    });
});
