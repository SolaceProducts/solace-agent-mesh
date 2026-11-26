// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useCapabilityBadge } from "@/api/capabilities";
import { useModelConfigs } from "@/api/models";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockModelConfigs, typesafeDecisionModelConfig } from "@/testing/data/models";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelsView } from "./ModelsView";

expect.extend(matchers);

vi.mock("@/api/models", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/models")>();
    const { mockModelConfigs: seed } = await import("@/testing/data/models");
    return {
        ...actual,
        useModelConfigs: vi.fn(() => ({ data: [seed[0]], isLoading: false, error: null })),
        useDeleteModel: () => ({ mutateAsync: vi.fn(), isPending: false }),
    };
});

vi.mock("@/api/capabilities", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/capabilities")>()),
    useCapabilityBadge: vi.fn(() => null),
}));

beforeEach(() => {
    vi.mocked(useCapabilityBadge).mockReturnValue(null);
    vi.mocked(useModelConfigs).mockReturnValue({ data: [mockModelConfigs[0]], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
});

function renderWithCapabilities(granted: string[]) {
    const held = new Set(granted);
    renderWithProviders(<ModelsView />, {
        userContextValues: { userHasCapability: (cap: string) => held.has(cap) },
    });
}

async function openRowMenu() {
    await userEvent.click(screen.getByRole("button", { name: "Actions" }));
}

describe("ModelsView row-menu affordance gating", () => {
    test("a read-only user sees only Open Details (no Edit/Delete)", async () => {
        renderWithCapabilities([CAPABILITIES.readModelConfig]);
        await openRowMenu();

        expect(await screen.findByText("Open Details")).toBeInTheDocument();
        expect(screen.queryByText("Edit")).not.toBeInTheDocument();
        expect(screen.queryByText("Delete")).not.toBeInTheDocument();
    });

    test("Edit appears only with updateModelConfig", async () => {
        renderWithCapabilities([CAPABILITIES.updateModelConfig]);
        await openRowMenu();

        expect(await screen.findByText("Edit")).toBeInTheDocument();
        expect(screen.queryByText("Delete")).not.toBeInTheDocument();
    });

    test("Delete appears only with deleteModelConfig", async () => {
        renderWithCapabilities([CAPABILITIES.deleteModelConfig]);
        await openRowMenu();

        expect(await screen.findByText("Delete")).toBeInTheDocument();
        expect(screen.queryByText("Edit")).not.toBeInTheDocument();
    });

    test("a write-capable user sees both Edit and Delete", async () => {
        renderWithCapabilities([CAPABILITIES.updateModelConfig, CAPABILITIES.deleteModelConfig]);
        await openRowMenu();

        expect(await screen.findByText("Edit")).toBeInTheDocument();
        expect(screen.getByText("Delete")).toBeInTheDocument();
    });
});

describe("ModelsView empty-state create gating", () => {
    beforeEach(() => {
        vi.mocked(useModelConfigs).mockReturnValue({ data: [], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
    });

    test("hides Add Model for a read-only user", () => {
        renderWithCapabilities([CAPABILITIES.readModelConfig]);
        expect(screen.queryByRole("button", { name: /Add Model/ })).not.toBeInTheDocument();
    });

    test("shows Add Model when the user can create", () => {
        renderWithCapabilities([CAPABILITIES.createModelConfig]);
        expect(screen.getByRole("button", { name: /Add Model/ })).toBeInTheDocument();
    });
});

describe("ModelsView decision models", () => {
    test("marks a decision model with a Decision badge and leaves chat models unmarked", () => {
        vi.mocked(useModelConfigs).mockReturnValue({ data: [mockModelConfigs[0], typesafeDecisionModelConfig], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
        renderWithCapabilities([CAPABILITIES.readModelConfig]);

        const badges = screen.getAllByTestId("decisionModelBadge");
        expect(badges).toHaveLength(1);
        expect(screen.getByTestId(`modelRow-${typesafeDecisionModelConfig.alias}`)).toContainElement(badges[0]);
    });

    test("labels a decision model with the decision models' maturity, and only it", () => {
        vi.mocked(useCapabilityBadge).mockImplementation(key => (key === "workflow_decision_node" ? "EARLY ACCESS" : null));
        vi.mocked(useModelConfigs).mockReturnValue({ data: [mockModelConfigs[0], typesafeDecisionModelConfig], isLoading: false, error: null } as unknown as ReturnType<typeof useModelConfigs>);
        renderWithCapabilities([CAPABILITIES.readModelConfig]);

        const labels = screen.getAllByText("EARLY ACCESS");
        expect(labels).toHaveLength(1);
        expect(screen.getByTestId(`modelRow-${typesafeDecisionModelConfig.alias}`)).toContainElement(labels[0]);
    });
});
