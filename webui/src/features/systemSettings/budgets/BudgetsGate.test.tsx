// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { BudgetsGate } from "./BudgetsGate";

const CHILD_TEXT = "budgets surface";

function renderGate({ held = [CAPABILITIES.readBudgets], capabilitiesLoaded = true }: { held?: Capability[]; capabilitiesLoaded?: boolean } = {}) {
    return render(
        <StoryProvider userContextValues={{ capabilitiesLoaded, userHasCapability: capability => held.includes(capability) }}>
            <MemoryRouter initialEntries={["/budgets"]}>
                <Routes>
                    <Route path="/budgets" element={<BudgetsGate />}>
                        <Route index element={<div>{CHILD_TEXT}</div>} />
                    </Route>
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("BudgetsGate", () => {
    it("renders the page for a viewer with budget:*:read", () => {
        renderGate();
        expect(screen.getByText(CHILD_TEXT)).toBeInTheDocument();
    });

    it("denies a viewer without budget:*:read", () => {
        renderGate({ held: [] });
        expect(screen.getByText("Access Denied")).toBeInTheDocument();
        expect(screen.getByText(/permission to view budgets/)).toBeInTheDocument();
        expect(screen.queryByText(CHILD_TEXT)).not.toBeInTheDocument();
    });

    it("renders nothing while scopes are loading, rather than flashing Access Denied", () => {
        renderGate({ held: [], capabilitiesLoaded: false });
        expect(screen.queryByText("Access Denied")).not.toBeInTheDocument();
        expect(screen.queryByText(CHILD_TEXT)).not.toBeInTheDocument();
    });
});
