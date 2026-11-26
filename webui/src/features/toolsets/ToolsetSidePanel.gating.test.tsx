// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockCustomToolsets } from "@/testing/data/toolsets";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ToolsetSidePanel } from "./ToolsetSidePanel";

expect.extend(matchers);

const renderWithCapabilities = (granted: string[]) => {
    const held = new Set(granted);
    renderWithProviders(<ToolsetSidePanel toolset={mockCustomToolsets[0]} />, {
        userContextValues: { userHasCapability: (cap: string) => held.has(cap) },
    });
};

describe("ToolsetSidePanel Add-to-Agent gating", () => {
    test("hides Add to Agent when the user cannot update agents", () => {
        renderWithCapabilities([CAPABILITIES.readToolsets]);
        expect(screen.queryByTestId("addToAgentButton")).not.toBeInTheDocument();
        expect(screen.getByTestId("openToolsetButton")).toBeInTheDocument();
    });

    test("shows Add to Agent when the user can update agents", () => {
        renderWithCapabilities([CAPABILITIES.updateAgents]);
        expect(screen.getByTestId("addToAgentButton")).toBeInTheDocument();
    });

    test("does not auto-open the dialog without update-agents, even when initialOpenAddToAgent is set", () => {
        const held = new Set<string>([CAPABILITIES.createToolsets]);
        renderWithProviders(<ToolsetSidePanel toolset={mockCustomToolsets[0]} initialOpenAddToAgent />, {
            userContextValues: { userHasCapability: (cap: string) => held.has(cap) },
        });
        expect(screen.queryByText("Add Toolset to Agent")).not.toBeInTheDocument();
    });

    test("auto-opens the dialog when initialOpenAddToAgent is set and the user can update agents", () => {
        const held = new Set<string>([CAPABILITIES.updateAgents]);
        renderWithProviders(<ToolsetSidePanel toolset={mockCustomToolsets[0]} initialOpenAddToAgent />, {
            userContextValues: { userHasCapability: (cap: string) => held.has(cap) },
        });
        expect(screen.getByText("Add Toolset to Agent")).toBeInTheDocument();
    });
});
