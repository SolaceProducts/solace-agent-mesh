// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

function LocationProbe() {
    const loc = useLocation();
    return (
        <>
            <div data-testid="location-pathname">{loc.pathname}</div>
            <div data-testid="location-search">{loc.search}</div>
        </>
    );
}

async function loadSidePanel(pinnedAgentName: string | null) {
    vi.resetModules();
    vi.doMock("@/providers/contexts", async () => {
        const actual = await vi.importActual<typeof import("@/providers/contexts")>("@/providers/contexts");
        return { ...actual, useUIModeContext: () => actual.agentUIMode(pinnedAgentName) };
    });
    vi.doMock("@/features/chat/recentChats/RecentChatsList", () => ({ RecentChatsList: () => null }));
    vi.doMock("@/features/settings/SettingsDialog", () => ({ SettingsDialog: () => null }));
    const mod = await import("@/features/chat/recentChats/RecentChatsSidePanel");
    return mod.RecentChatsSidePanel;
}

describe("RecentChatsSidePanel — View All", () => {
    test("enabled 'View All' navigates to the agent-scoped recent-chats route (agent name URL-encoded)", async () => {
        const user = userEvent.setup();
        const SidePanel = await loadSidePanel("My Agent");
        render(
            <MemoryRouter initialEntries={["/agent-mode/chat"]}>
                <SidePanel />
                <LocationProbe />
            </MemoryRouter>
        );

        await user.click(screen.getByRole("button", { name: /view all/i }));

        expect(screen.getByTestId("location-pathname").textContent).toBe("/agent-mode/recent-chats");
        expect(screen.getByTestId("location-search").textContent).toBe("?agent=My%20Agent");
    });

    test("viewAllDisabled renders a disabled 'View All' that does not navigate", async () => {
        const user = userEvent.setup();
        const SidePanel = await loadSidePanel("My Agent");
        render(
            <MemoryRouter initialEntries={["/agent-mode/chat"]}>
                <SidePanel viewAllDisabled />
                <LocationProbe />
            </MemoryRouter>
        );

        const viewAll = screen.getByRole("button", { name: /view all/i });
        expect(viewAll).toBeDisabled();

        await user.click(viewAll);

        expect(screen.getByTestId("location-pathname").textContent).toBe("/agent-mode/chat");
    });
});
