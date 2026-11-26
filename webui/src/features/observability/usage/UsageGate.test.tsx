// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { SCOPES, SUPERADMIN_SCOPE } from "@/utils/scopes";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { UsageGate } from "./UsageGate";

const CHILD_TEXT = "usage surface";

function renderGate({ scopes = [SUPERADMIN_SCOPE], capabilitiesLoaded = true }: { scopes?: string[]; capabilitiesLoaded?: boolean } = {}) {
    return render(
        <StoryProvider userContextValues={{ capabilitiesLoaded, scopes }}>
            <MemoryRouter initialEntries={["/usage"]}>
                <Routes>
                    <Route path="/usage" element={<UsageGate />}>
                        <Route index element={<div>{CHILD_TEXT}</div>} />
                    </Route>
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("UsageGate", () => {
    it("renders the page for a super admin", () => {
        renderGate();
        expect(screen.getByText(CHILD_TEXT)).toBeInTheDocument();
    });

    it("denies a viewer without *:*:read_usage", () => {
        renderGate({ scopes: [SCOPES.budget.read] });
        expect(screen.getByText("Access Denied")).toBeInTheDocument();
    });

    it("renders nothing while scopes are loading, rather than flashing Access Denied", () => {
        const { container } = renderGate({ capabilitiesLoaded: false });
        expect(container).toBeEmptyDOMElement();
    });
});
