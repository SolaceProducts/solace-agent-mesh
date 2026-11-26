// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

const handleSwitchSession = vi.fn();
const markShareViewed = vi.fn();

type MockShared = { id: string; title: string; ownerEmail: string; accessLevel: string; sharedAt: string; shareUrl: string; sessionId?: string | null; viewedAt?: string | null };

let mockSessions: Array<{ id: string; name: string | null; updatedAt: string; agentId?: string }> = [];
let mockShared: MockShared[] = [];
let mockChatSharing = true;
let mockBuilderEnabled = true;
let mockLoading = false;
let mockError = false;
let mockAgentSessionRoutes: Record<string, string> = {};
let mockExcludeAgentIds: string[] = [];

function LocationProbe() {
    const loc = useLocation();
    return <div data-testid="location-path">{loc.pathname}</div>;
}

async function loadFlyout() {
    vi.resetModules();

    vi.doMock("@/api/sessions", () => ({
        useRecentSessions: () => ({ data: mockSessions, isLoading: mockLoading, isError: mockError }),
    }));

    vi.doMock("@/api/share", () => ({
        useSharedWithMe: () => ({ data: mockShared, isLoading: false }),
        useMarkShareViewed: () => ({ mutate: markShareViewed }),
    }));

    vi.doMock("@/hooks/useIsChatSharingEnabled", () => ({
        useIsChatSharingEnabled: () => mockChatSharing,
    }));

    vi.doMock("@/hooks/useBuilderEnabled", () => ({
        useBuilderEnabled: () => mockBuilderEnabled,
    }));

    vi.doMock("@/providers/contexts/ChatContext", () => ({
        useChatContext: () => ({
            handleSwitchSession,
            agentSessionRoutes: mockAgentSessionRoutes,
            sessionListExcludeAgentIds: mockExcludeAgentIds,
        }),
    }));

    vi.doMock("@/providers/contexts/ThemeContext", () => ({
        useThemeContext: () => ({
            currentTheme: "light",
            themes: [],
            setTheme: () => {},
            toggleTheme: () => {},
        }),
    }));

    vi.doMock("@/features/chat/SessionIcon", () => ({
        SessionIcon: () => <span data-testid="session-icon" />,
    }));

    const mod = await import("@/features/navigation/RecentChatsFlyout");
    return mod.RecentChatsFlyout;
}

async function renderFlyout() {
    const RecentChatsFlyout = await loadFlyout();
    return render(
        <MemoryRouter initialEntries={["/start"]}>
            <Routes>
                <Route
                    path="/start"
                    element={
                        <>
                            <RecentChatsFlyout />
                            <LocationProbe />
                        </>
                    }
                />
                <Route path="/chat" element={<LocationProbe />} />
                <Route path="/recent-chats" element={<LocationProbe />} />
            </Routes>
        </MemoryRouter>
    );
}

function makeSession(id: string, name: string | null, iso: string, agentId?: string) {
    return { id, name, updatedAt: iso, agentId };
}

describe("RecentChatsFlyout", () => {
    beforeEach(() => {
        handleSwitchSession.mockReset();
        markShareViewed.mockReset();
        mockSessions = [];
        mockShared = [];
        mockChatSharing = true;
        mockBuilderEnabled = true;
        mockLoading = false;
        mockError = false;
        mockAgentSessionRoutes = {};
        mockExcludeAgentIds = [];
    });

    test("renders the icon button with the Recent Chats label", async () => {
        await renderFlyout();
        const button = screen.getByRole("button", { name: /recent chats/i });
        expect(button).toBeInTheDocument();
    });

    test("clicking the icon navigates to /recent-chats", async () => {
        const user = userEvent.setup();
        await renderFlyout();

        await user.click(screen.getByRole("button", { name: /recent chats/i }));
        expect(await screen.findByText("/recent-chats", { selector: "[data-testid='location-path']" })).toBeInTheDocument();
    });

    test("hovering the icon opens a flyout that lists recent sessions", async () => {
        mockSessions = [makeSession("s1", "Session One", "2024-01-01T00:00:00Z"), makeSession("s2", "Session Two", "2024-01-02T00:00:00Z")];
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));

        await waitFor(() => {
            expect(screen.getByRole("button", { name: /session one/i })).toBeInTheDocument();
        });
        expect(screen.getByRole("button", { name: /session two/i })).toBeInTheDocument();
    });

    test("caps at 8 items even if more are returned", async () => {
        mockSessions = Array.from({ length: 12 }, (_, i) => makeSession(`s${i}`, `Session ${i}`, new Date(Date.now() - i * 1000).toISOString()));
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));

        await waitFor(() => {
            const allButtons = screen.getAllByRole("button");
            const sessionButtons = allButtons.filter(b => /^Session \d+$/.test(b.textContent ?? ""));
            expect(sessionButtons).toHaveLength(8);
        });
    });

    test("clicking a session navigates to /chat and switches session", async () => {
        mockSessions = [makeSession("s-target", "Pick Me", "2024-01-01T00:00:00Z")];
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));
        const item = await screen.findByRole("button", { name: /pick me/i });
        await user.click(item);

        expect(await screen.findByText("/chat", { selector: "[data-testid='location-path']" })).toBeInTheDocument();
        expect(handleSwitchSession).toHaveBeenCalledWith("s-target");
    });

    test("clicking a session whose agent has a custom route dispatches agent-session-navigate and does NOT call handleSwitchSession", async () => {
        mockAgentSessionRoutes = { "agent-builder": "/builder" };
        mockSessions = [makeSession("s-builder", "Builder Chat", "2024-01-01T00:00:00Z", "agent-builder")];
        const eventSpy = vi.fn();
        window.addEventListener("agent-session-navigate", eventSpy as EventListener);

        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));
        await user.click(await screen.findByRole("button", { name: /builder chat/i }));

        expect(eventSpy).toHaveBeenCalledTimes(1);
        const event = eventSpy.mock.calls[0][0] as CustomEvent<{ sessionId: string; route: string; agentId?: string; isEphemeralTest?: boolean }>;
        expect(event.detail).toEqual({ sessionId: "s-builder", route: "/builder", agentId: "agent-builder", isEphemeralTest: false });
        expect(handleSwitchSession).not.toHaveBeenCalled();

        window.removeEventListener("agent-session-navigate", eventSpy as EventListener);
    });

    test("filters out sessions whose agentId is in sessionListExcludeAgentIds", async () => {
        mockExcludeAgentIds = ["agent-x"];
        mockSessions = [makeSession("keep-1", "Keep Me", "2024-01-01T00:00:00Z"), makeSession("hide-1", "Hide Me", "2024-01-02T00:00:00Z", "agent-x")];
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));

        await waitFor(() => {
            expect(screen.getByRole("button", { name: /keep me/i })).toBeInTheDocument();
        });
        expect(screen.queryByRole("button", { name: /hide me/i })).not.toBeInTheDocument();
    });

    test("hides Builder sessions when the user lacks Builder access", async () => {
        mockBuilderEnabled = false;
        mockSessions = [makeSession("keep-1", "Keep Me", "2024-01-01T00:00:00Z"), makeSession("builder-1", "Builder Chat", "2024-01-02T00:00:00Z", "Builder")];
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));

        await waitFor(() => {
            expect(screen.getByRole("button", { name: /keep me/i })).toBeInTheDocument();
        });
        expect(screen.queryByRole("button", { name: /builder chat/i })).not.toBeInTheDocument();
    });

    test("shows Builder sessions when the user has Builder access", async () => {
        mockBuilderEnabled = true;
        mockSessions = [makeSession("builder-1", "Builder Chat", "2024-01-02T00:00:00Z", "Builder")];
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));

        await waitFor(() => {
            expect(screen.getByRole("button", { name: /builder chat/i })).toBeInTheDocument();
        });
    });

    test("shows a spinner while sessions are loading", async () => {
        mockLoading = true;
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));

        await waitFor(() => {
            expect(screen.queryByText(/no recent chats/i)).not.toBeInTheDocument();
        });
        expect(screen.queryAllByTestId("session-icon")).toHaveLength(0);
    });

    test("shows an error state when the sessions query fails", async () => {
        mockError = true;
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));
        expect(await screen.findByText(/could not load chats/i)).toBeInTheDocument();
    });

    test("shows empty state when there are no recent sessions", async () => {
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));
        expect(await screen.findByText(/no recent chats/i)).toBeInTheDocument();
    });

    test.each([
        ["null", null],
        ["empty string", ""],
        ["whitespace only", "   "],
    ])('renders "New Chat" when session.name is %s', async (_label, nameValue) => {
        mockSessions = [makeSession("s-blank", nameValue, "2024-01-01T00:00:00Z")];
        const user = userEvent.setup();
        await renderFlyout();

        await user.hover(screen.getByRole("button", { name: /recent chats/i }));
        await waitFor(() => {
            expect(screen.getByRole("button", { name: /new chat/i })).toBeInTheDocument();
        });
    });

    describe("chats shared with me", () => {
        const makeShared = (over: Partial<MockShared> = {}): MockShared => ({
            id: "sh-1",
            title: "Shared Chat",
            ownerEmail: "owner@example.com",
            accessLevel: "resource_viewer",
            sharedAt: "2024-01-05T00:00:00Z",
            shareUrl: "https://example.com/sh-1",
            ...over,
        });

        test("lists shared chats alongside the user's own sessions", async () => {
            mockSessions = [makeSession("s1", "My Session", "2024-01-01T00:00:00Z")];
            mockShared = [makeShared()];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));

            expect(await screen.findByRole("button", { name: /shared chat/i })).toBeInTheDocument();
            expect(screen.getByRole("button", { name: /my session/i })).toBeInTheDocument();
        });

        test("omits shared chats when chat sharing is disabled", async () => {
            mockChatSharing = false;
            mockSessions = [makeSession("s1", "My Session", "2024-01-01T00:00:00Z")];
            mockShared = [makeShared()];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));

            await waitFor(() => {
                expect(screen.getByRole("button", { name: /my session/i })).toBeInTheDocument();
            });
            expect(screen.queryByRole("button", { name: /shared chat/i })).not.toBeInTheDocument();
        });

        test("shows the unseen indicator for a share the recipient never opened", async () => {
            mockShared = [makeShared({ viewedAt: null })];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));

            expect(await screen.findByLabelText("Unseen updates")).toBeInTheDocument();
        });

        test("hides the unseen indicator once the share has been opened", async () => {
            mockShared = [makeShared({ sharedAt: "2024-01-05T00:00:00Z", viewedAt: "2024-01-06T00:00:00Z" })];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));

            await waitFor(() => {
                expect(screen.getByRole("button", { name: /shared chat/i })).toBeInTheDocument();
            });
            expect(screen.queryByLabelText("Unseen updates")).not.toBeInTheDocument();
        });

        test("a newly shared chat sorts above older owned sessions", async () => {
            mockSessions = [makeSession("s1", "Older Session", "2024-01-01T00:00:00Z")];
            mockShared = [makeShared({ title: "Newer Shared", sharedAt: "2024-02-01T00:00:00Z" })];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));
            await screen.findByRole("button", { name: /newer shared/i });

            const rows = screen.getAllByRole("listitem");
            expect(rows[0].textContent).toContain("Newer Shared");
        });

        test("caps the merged list at 8 rows", async () => {
            mockSessions = Array.from({ length: 6 }, (_, i) => makeSession(`s${i}`, `Session ${i}`, new Date(Date.UTC(2024, 0, 10 + i)).toISOString()));
            mockShared = Array.from({ length: 6 }, (_, i) => makeShared({ id: `sh-${i}`, title: `Shared ${i}`, sharedAt: new Date(Date.UTC(2024, 0, 1 + i)).toISOString() }));
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));
            await screen.findByRole("button", { name: /session 5/i });

            expect(screen.getAllByRole("listitem")).toHaveLength(8);
        });

        test("opening a viewer share marks it viewed and routes to the shared view", async () => {
            mockShared = [makeShared({ id: "sh-open" })];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));
            await user.click(await screen.findByRole("button", { name: /shared chat/i }));

            expect(markShareViewed).toHaveBeenCalledWith("sh-open");
            expect(handleSwitchSession).not.toHaveBeenCalled();
        });

        test("opening an editor share marks it viewed and switches to the live session", async () => {
            handleSwitchSession.mockResolvedValueOnce(undefined);
            mockShared = [makeShared({ id: "sh-edit", accessLevel: "resource_editor", sessionId: "sess-9" })];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));
            await user.click(await screen.findByRole("button", { name: /shared chat/i }));

            expect(markShareViewed).toHaveBeenCalledWith("sh-edit");
            expect(handleSwitchSession).toHaveBeenCalledWith("sess-9");
            expect(await screen.findByText("/chat", { selector: "[data-testid='location-path']" })).toBeInTheDocument();
        });

        test("a shared chat alone is enough to avoid the empty state", async () => {
            mockShared = [makeShared()];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));

            await waitFor(() => {
                expect(screen.getByRole("button", { name: /shared chat/i })).toBeInTheDocument();
            });
            expect(screen.queryByText(/no recent chats/i)).not.toBeInTheDocument();
        });
    });

    describe("error handling", () => {
        let consoleErrorSpy: ReturnType<typeof vi.spyOn>;
        beforeEach(() => {
            consoleErrorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        });
        afterEach(() => {
            consoleErrorSpy.mockRestore();
        });

        test("logs (and does not crash) when handleSwitchSession rejects", async () => {
            handleSwitchSession.mockRejectedValueOnce(new Error("session store offline"));
            mockSessions = [makeSession("s-fail", "Will Fail", "2024-01-01T00:00:00Z")];
            const user = userEvent.setup();
            await renderFlyout();

            await user.hover(screen.getByRole("button", { name: /recent chats/i }));
            await user.click(await screen.findByRole("button", { name: /will fail/i }));

            await waitFor(() => {
                expect(consoleErrorSpy).toHaveBeenCalled();
            });
            expect(handleSwitchSession).toHaveBeenCalledWith("s-fail");
        });
    });
});
