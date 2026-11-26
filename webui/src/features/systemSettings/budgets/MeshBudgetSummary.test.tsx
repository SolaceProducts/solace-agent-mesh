// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { mockMeshBudget } from "@/testing/data";

import { MeshBudgetSummary } from "./MeshBudgetSummary";

function renderSummary(overrides: Partial<Parameters<typeof MeshBudgetSummary>[0]> = {}) {
    const props = { budget: mockMeshBudget, canEdit: true, onEdit: vi.fn(), ...overrides };
    render(<MeshBudgetSummary {...props} />);
    return props;
}

describe("MeshBudgetSummary", () => {
    test("a missing budget shows No limit set and no buttons", () => {
        renderSummary({ budget: null });
        expect(screen.getByText("No limit set")).toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    test("buttons are hidden without the matching permission", () => {
        renderSummary({ canEdit: false });
        expect(screen.queryByTestId("meshBudgetMoreTrigger")).not.toBeInTheDocument();
    });

    test("a load failure shows its message and no buttons", () => {
        renderSummary({ budget: null, error: new Error("Service unavailable") });
        expect(screen.getByText(/Couldn't load the Agent Mesh budget\. Service unavailable/)).toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
});
