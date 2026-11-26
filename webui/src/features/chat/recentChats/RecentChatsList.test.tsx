// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

const handleSwitchSession = vi.fn();
const updateSessionName = vi.fn(() => Promise.resolve());
const markShareViewed = vi.fn();

let mockSessions: Array<{ id: string; name: string; updatedAt: string; agentId?: string }> = [];
let mockShared: Array<{ id: string; title: string; ownerEmail: string; accessLevel: string; sharedAt: string; shareUrl: string; sessionId?: string | null; viewedAt?: string | null }> = [];
let chatSharing = true;
let builderAccess = true;

function LocationProbe() {
    const loc = useLocation();
    return <div data-testid="location-path">{loc.pathname}</div>;
}

async function loadList() {
    vi.resetModules();

    vi.doMock("@/api/sessions", () => ({
        useRecentSessions: () => ({ data: mockSessions, isLoading: false }),
        useMarkSessionViewed: () => ({ mutate: vi.fn() }),
        useRenameSessionWithAI: () => ({ mutate: vi.fn(), isPending: false }),
    }));

    vi.doMock("@/api/share", () => ({
        useSharedWithMe: () => ({ data: mockShared, isLoading: false }),
        useMarkShareViewed: () => ({ mutate: markShareViewed }),
    }));

    vi.doMock("@/providers/contexts/ChatContext", () => ({
        useChatContext: () => ({
            sessionId: "current-session",
            handleSwitchSession,
            currentTaskId: null,
            updateSessionName,
            openSessionDeleteModal: vi.fn(),
            addNotification: vi.fn(),
        }),
    }));

    vi.doMock("@/providers/contexts/ConfigContext", () => ({
        useConfigContext: () => ({
            persistenceEnabled: true,
        }),
    }));

    vi.doMock("@/providers/contexts/UIModeContext", async () => {
        const actual = await vi.importActual<typeof import("@/providers/contexts/UIModeContext")>("@/providers/contexts/UIModeContext");
        return {
            ...actual,
            useUIModeContext: () => ({ allowedSessionActions: ["rename", "renameWithAI", "move", "share", "delete"] }),
        };
    });

    vi.doMock("@/hooks", async () => {
        const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
        return {
            ...actual,
            useIsAutoTitleGenerationEnabled: () => false,
            useIsChatSharingEnabled: () => chatSharing,
            useBuilderEnabled: () => builderAccess,
            useTitleAnimation: (name: string) => ({ text: name, isAnimating: false, isGenerating: false }),
        };
    });

    const mod = await import("@/features/chat/recentChats/RecentChatsList");
    return mod.RecentChatsList;
}

async function renderList() {
    const RecentChatsList = await loadList();
    return render(
        <MemoryRouter initialEntries={["/start"]}>
            <Routes>
                <Route
                    path="/start"
                    element={
                        <>
                            <RecentChatsList />
                            <LocationProbe />
                        </>
                    }
                />
                <Route path="/chat" element={<LocationProbe />} />
                <Route path="/shared-chat/:shareId" element={<LocationProbe />} />
            </Routes>
        </MemoryRouter>
    );
}

function makeSession(id: string, name: string, iso: string) {
    return { id, name, updatedAt: iso };
}

function makeShared(shareId: string, title: string, sharedAt: string, extra: Partial<{ accessLevel: string; sessionId: string | null; viewedAt: string | null }> = {}) {
    return {
        id: shareId,
        title,
        ownerEmail: "owner@example.com",
        accessLevel: "resource_viewer",
        sharedAt,
        shareUrl: `https://example.com/shared/${shareId}`,
        sessionId: null,
        ...extra,
    };
}

describe("RecentChatsList — merged shared entries", () => {
    beforeEach(() => {
        handleSwitchSession.mockReset();
        updateSessionName.mockReset();
        markShareViewed.mockReset();
        mockSessions = [];
        mockShared = [];
        chatSharing = true;
        builderAccess = true;
    });

    const sharedRowBars = (title: string | RegExp) => {
        const row = screen.getByRole("button", { name: title });
        return row.querySelectorAll('[aria-label="Unseen updates"]');
    };

    test("marks a never-opened shared entry as unseen", async () => {
        mockShared = [makeShared("share-1", "Unread Shared", new Date().toISOString(), { viewedAt: null })];
        await renderList();

        await screen.findByText("Unread Shared");
        expect(sharedRowBars(/unread shared/i)).toHaveLength(1);
    });

    test("shows no unseen bar once the shared entry has been opened", async () => {
        const sharedAt = new Date(Date.now() - 10_000).toISOString();
        mockShared = [makeShared("share-1", "Read Shared", sharedAt, { viewedAt: new Date().toISOString() })];
        await renderList();

        await screen.findByText("Read Shared");
        expect(sharedRowBars(/read shared/i)).toHaveLength(0);
    });

    test("Rename switches the row to an inline input and commits on Enter", async () => {
        mockSessions = [makeSession("s1", "Session One", new Date().toISOString())];
        await renderList();
        await screen.findByText("Session One");

        const trigger = screen.getAllByRole("button").find(b => b.getAttribute("aria-haspopup") === "menu")!;
        await userEvent.click(trigger);
        await userEvent.click(await screen.findByRole("menuitem", { name: /^Rename$/ }));

        const input = await screen.findByRole("textbox");
        await userEvent.clear(input);
        await userEvent.type(input, "Renamed Session");
        await userEvent.keyboard("{Enter}");

        expect(updateSessionName).toHaveBeenCalledWith("s1", "Renamed Session");
    });

    test("Escape exits the inline rename without persisting", async () => {
        mockSessions = [makeSession("s1", "Session One", new Date().toISOString())];
        await renderList();
        await screen.findByText("Session One");

        const trigger = screen.getAllByRole("button").find(b => b.getAttribute("aria-haspopup") === "menu")!;
        await userEvent.click(trigger);
        await userEvent.click(await screen.findByRole("menuitem", { name: /^Rename$/ }));

        const input = await screen.findByRole("textbox");
        await userEvent.clear(input);
        await userEvent.type(input, "Should Not Persist");
        await userEvent.keyboard("{Escape}");

        expect(updateSessionName).not.toHaveBeenCalled();
        expect(await screen.findByText("Session One")).toBeInTheDocument();
    });

    test("shows 'No recent chats' empty state when both lists are empty", async () => {
        await renderList();
        expect(await screen.findByText(/no recent chats/i)).toBeInTheDocument();
    });

    test("renders shared chat titles alongside recent sessions", async () => {
        mockSessions = [makeSession("s1", "Session One", "2024-01-01T00:00:00Z")];
        mockShared = [makeShared("share-1", "Shared Alpha", new Date().toISOString())];
        await renderList();

        expect(await screen.findByText("Shared Alpha")).toBeInTheDocument();
        expect(screen.getByText("Session One")).toBeInTheDocument();
    });

    test("renders an action menu for session rows but not shared entries", async () => {
        mockSessions = [makeSession("s1", "Session One", new Date().toISOString())];
        mockShared = [makeShared("share-1", "Shared Alpha", new Date().toISOString())];
        await renderList();

        await screen.findByText("Session One");
        const menuTriggers = screen.getAllByRole("button").filter(b => b.getAttribute("aria-haspopup") === "menu");
        expect(menuTriggers).toHaveLength(1);
    });

    test("sorts entries by timestamp (most recent first)", async () => {
        const now = Date.now();
        mockSessions = [makeSession("s-old", "Old Session", new Date(now - 10_000_000).toISOString()), makeSession("s-new", "New Session", new Date(now - 1_000).toISOString())];
        mockShared = [makeShared("share-mid", "Mid Shared", new Date(now - 5_000).toISOString())];

        await renderList();

        const buttons = await screen.findAllByRole("button");
        const labels = buttons.map(b => b.textContent?.trim()).filter(Boolean);
        const newIdx = labels.findIndex(l => l === "New Session");
        const midIdx = labels.findIndex(l => l === "Mid Shared");
        const oldIdx = labels.findIndex(l => l === "Old Session");
        expect(newIdx).toBeLessThan(midIdx);
        expect(midIdx).toBeLessThan(oldIdx);
    });

    test("hides Builder sessions when the user lacks Builder access", async () => {
        builderAccess = false;
        mockSessions = [makeSession("s1", "Regular Chat", "2024-01-01T00:00:00Z"), { ...makeSession("b1", "SAM Builder Introduction Greeting", "2024-01-02T00:00:00Z"), agentId: "Builder" }];
        await renderList();

        expect(await screen.findByText("Regular Chat")).toBeInTheDocument();
        expect(screen.queryByText("SAM Builder Introduction Greeting")).not.toBeInTheDocument();
    });

    test("shows Builder sessions when the user has Builder access", async () => {
        builderAccess = true;
        mockSessions = [{ ...makeSession("b1", "SAM Builder Introduction Greeting", "2024-01-02T00:00:00Z"), agentId: "Builder" }];
        await renderList();

        expect(await screen.findByText("SAM Builder Introduction Greeting")).toBeInTheDocument();
    });

    test("omits shared items when chatSharing flag is off", async () => {
        chatSharing = false;
        mockSessions = [makeSession("s1", "Session One", "2024-01-01T00:00:00Z")];
        mockShared = [makeShared("share-1", "Shared Alpha", new Date().toISOString())];

        await renderList();
        expect(await screen.findByText("Session One")).toBeInTheDocument();
        expect(screen.queryByText("Shared Alpha")).not.toBeInTheDocument();
    });

    test("shared viewer entry navigates to /shared-chat/:shareId", async () => {
        mockShared = [makeShared("share-42", "Viewer Chat", new Date().toISOString())];
        const user = userEvent.setup();
        await renderList();

        await user.click(await screen.findByRole("button", { name: /viewer chat/i }));
        expect(await screen.findByText("/shared-chat/share-42", { selector: "[data-testid='location-path']" })).toBeInTheDocument();
        expect(markShareViewed).toHaveBeenCalledWith("share-42");
    });

    test("shared editor entry (with sessionId) switches to /chat and opens that session", async () => {
        mockShared = [makeShared("share-7", "Editor Chat", new Date().toISOString(), { accessLevel: "resource_editor", sessionId: "sess-7" })];
        const user = userEvent.setup();
        await renderList();

        await user.click(await screen.findByRole("button", { name: /editor chat/i }));
        expect(await screen.findByText("/chat", { selector: "[data-testid='location-path']" })).toBeInTheDocument();
        expect(handleSwitchSession).toHaveBeenCalledWith("sess-7");
        expect(markShareViewed).toHaveBeenCalledWith("share-7");
    });

    test("respects maxItems cap across combined list", async () => {
        mockSessions = Array.from({ length: 10 }, (_, i) => makeSession(`s${i}`, `Session ${i}`, new Date(Date.now() - i * 1000).toISOString()));
        mockShared = Array.from({ length: 10 }, (_, i) => makeShared(`sh${i}`, `Shared ${i}`, new Date(Date.now() - i * 500).toISOString()));
        const RecentChatsList = await loadList();
        render(
            <MemoryRouter>
                <RecentChatsList maxItems={5} />
            </MemoryRouter>
        );

        const buttons = await screen.findAllByRole("button");
        const rows = buttons.filter(b => b.getAttribute("aria-haspopup") !== "menu");
        expect(rows).toHaveLength(5);
    });
});
