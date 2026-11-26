// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { waitFor } from "@testing-library/react";
import { useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { agentUIMode, FULL_UI_MODE, type UIMode } from "@/providers/contexts";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { UIModeGuard } from "./UIModeGuard";

function LocationProbe() {
    const loc = useLocation();
    return <div data-testid="loc">{loc.pathname + loc.search}</div>;
}

function render(uiMode: UIMode, initialPath: string) {
    const { getByTestId } = renderWithProviders(
        <>
            <UIModeGuard />
            <LocationProbe />
        </>,
        { uiModeContextValues: uiMode, initialPath }
    );
    return () => getByTestId("loc").textContent;
}

describe("UIModeGuard", () => {
    it("does not confine the full UI", async () => {
        const loc = render(FULL_UI_MODE, "/chat");
        await waitFor(() => expect(loc()).toBe("/chat"));
    });

    it("redirects off-surface navigation back to the pinned agent-mode route", async () => {
        const loc = render(agentUIMode("Foo"), "/chat");
        await waitFor(() => expect(loc()).toBe("/agent-mode/chat?agent=Foo"));
    });

    it("leaves an on-surface route with the agent param alone", async () => {
        const loc = render(agentUIMode("Foo"), "/agent-mode/chat?agent=Foo");
        await waitFor(() => expect(loc()).toBe("/agent-mode/chat?agent=Foo"));
    });

    it("re-asserts a dropped agent param while on-surface", async () => {
        const loc = render(agentUIMode("Foo"), "/agent-mode/chat");
        await waitFor(() => expect(loc()).toBe("/agent-mode/chat?agent=Foo"));
    });

    it("does not loop when agent mode has no pinned agent", async () => {
        const loc = render(agentUIMode(null), "/agent-mode/chat");
        await waitFor(() => expect(loc()).toBe("/agent-mode/chat"));
    });
});
