// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { FeedbackGate } from "./FeedbackGate";

const { mockWhenProviderReady } = vi.hoisted(() => ({ mockWhenProviderReady: vi.fn(() => true) }));

vi.mock("@openfeature/react-sdk", async importOriginal => ({
    ...(await importOriginal<typeof import("@openfeature/react-sdk")>()),
    useWhenProviderReady: mockWhenProviderReady,
}));

const CHILD_TEXT = "feedback surface";

interface GateOptions {
    flagEnabled?: boolean;
    capabilitiesLoaded?: boolean;
    hasReach?: boolean;
    flagsReady?: boolean;
}

function renderGate({ flagEnabled = true, capabilitiesLoaded = true, hasReach = true, flagsReady = true }: GateOptions = {}) {
    mockWhenProviderReady.mockReturnValue(flagsReady);

    return render(
        <StoryProvider userContextValues={{ capabilitiesLoaded, userHasCapability: () => hasReach }} flagValues={{ feedback_management: flagEnabled }}>
            <MemoryRouter initialEntries={["/feedback"]}>
                <Routes>
                    <Route path="/feedback" element={<FeedbackGate />}>
                        <Route index element={<div>{CHILD_TEXT}</div>} />
                    </Route>
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("FeedbackGate", () => {
    beforeEach(() => {
        mockWhenProviderReady.mockReturnValue(true);
    });

    it("renders the child route when the flag is on and the viewer holds feedback reach", () => {
        renderGate();

        expect(screen.getByText(CHILD_TEXT)).toBeInTheDocument();
        expect(screen.queryByText("Access Denied")).not.toBeInTheDocument();
    });

    it("renders Access Denied when the feature flag is off", () => {
        renderGate({ flagEnabled: false });

        expect(screen.getByText("Access Denied")).toBeInTheDocument();
        expect(screen.queryByText(CHILD_TEXT)).not.toBeInTheDocument();
    });

    it("renders Access Denied when the viewer holds no feedback reach", () => {
        renderGate({ hasReach: false });

        expect(screen.getByText("Access Denied")).toBeInTheDocument();
        expect(screen.queryByText(CHILD_TEXT)).not.toBeInTheDocument();
    });

    it("renders neither the child nor Access Denied while capabilities are still loading", () => {
        renderGate({ capabilitiesLoaded: false });

        expect(screen.queryByText(CHILD_TEXT)).not.toBeInTheDocument();
        expect(screen.queryByText("Access Denied")).not.toBeInTheDocument();
    });

    it("renders neither the child nor Access Denied while the flag provider is still resolving", () => {
        renderGate({ flagsReady: false, flagEnabled: false });

        expect(screen.queryByText(CHILD_TEXT)).not.toBeInTheDocument();
        expect(screen.queryByText("Access Denied")).not.toBeInTheDocument();
    });
});
