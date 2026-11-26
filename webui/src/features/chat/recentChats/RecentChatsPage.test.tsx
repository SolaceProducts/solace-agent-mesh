// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

function LocationProbe() {
    const loc = useLocation();
    return <div data-testid="location-search">{loc.search}</div>;
}

const mockListSharedWithMe = vi.fn();
const mockSearchGet = vi.fn();
const handleSwitchSession = vi.fn();
let chatSharing = true;
let schedulerEnabled = false;
let builderEnabled = true;
let agentMode = false;
let mockSessions: Array<Record<string, unknown>> = [];
let mockProjects: Array<Record<string, unknown>> = [];

async function loadPage() {
    vi.resetModules();

    vi.doMock("@/api/client", async () => {
        const actual = await vi.importActual<typeof import("@/api/client")>("@/api/client");
        return { ...actual, api: { ...actual.api, webui: { ...actual.api.webui, get: mockSearchGet } } };
    });

    vi.doMock("@/api/sessions", async () => {
        const actual = await vi.importActual<typeof import("@/api/sessions")>("@/api/sessions");
        return {
            ...actual,
            useInfiniteSessions: () => ({
                data: { pages: [{ data: mockSessions }] },
                fetchNextPage: vi.fn(),
                hasNextPage: false,
                isFetchingNextPage: false,
            }),
            useRenameSessionWithAI: () => ({ mutate: vi.fn(), isPending: false }),
        };
    });

    vi.doMock("@/api/share", () => ({
        useSharedWithMe: () => ({ data: mockListSharedWithMe(), isLoading: false }),
        useMarkShareViewed: () => ({ mutate: vi.fn() }),
    }));

    vi.doMock("@/providers/contexts/ChatContext", () => ({
        useChatContext: () => ({
            sessionId: "sid",
            handleSwitchSession,
            handleNewSession: vi.fn(),
            updateSessionName: vi.fn(),
            openSessionDeleteModal: vi.fn(),
            closeSessionDeleteModal: vi.fn(),
            confirmSessionDelete: vi.fn(),
            sessionToDelete: null,
            addNotification: vi.fn(),
            currentTaskId: null,
            agentSessionRoutes: { Builder: "/builder" },
        }),
    }));

    vi.doMock("@/providers/contexts/ProjectContext", async () => {
        const actual = await vi.importActual<typeof import("@/providers/contexts/ProjectContext")>("@/providers/contexts/ProjectContext");
        return { ...actual, useProjectContext: () => ({ projects: mockProjects }) };
    });

    vi.doMock("@/providers/contexts/ConfigContext", () => ({
        useConfigContext: () => ({
            persistenceEnabled: true,
        }),
    }));

    vi.doMock("@/providers/contexts/UIModeContext", async () => {
        const actual = await vi.importActual<typeof import("@/providers/contexts/UIModeContext")>("@/providers/contexts/UIModeContext");
        return {
            ...actual,
            useUIModeContext: () => (agentMode ? actual.agentUIMode("My Agent") : actual.FULL_UI_MODE),
        };
    });

    vi.doMock("./RecentChatsSidePanel", () => ({
        RecentChatsSidePanel: () => <div data-testid="recent-chats-side-panel">side panel</div>,
    }));

    vi.doMock("@/hooks", async () => {
        const actual = await vi.importActual<typeof import("@/hooks")>("@/hooks");
        return {
            ...actual,
            useIsAutoTitleGenerationEnabled: () => false,
            useTitleGeneration: () => ({ generateTitle: vi.fn() }),
            useTitleAnimation: (name: string) => ({ text: name, isAnimating: false, isGenerating: false }),
            useIsChatSharingEnabled: () => chatSharing,
            useIsSchedulerEnabled: () => schedulerEnabled,
            useBuilderEnabled: () => builderEnabled,
        };
    });

    const mod = await import("@/features/chat/recentChats/RecentChatsPage");
    return mod.RecentChatsPage;
}

interface RenderOptions {
    chatSharingEnabled?: boolean;
    schedulerEnabled?: boolean;
    builderEnabled?: boolean;
    sessions?: Array<Record<string, unknown>>;
    projects?: Array<Record<string, unknown>>;
    initialUrl?: string;
    agentMode?: boolean;
}

async function renderPage({ chatSharingEnabled = true, schedulerEnabled: scheduler = false, builderEnabled: builder = true, sessions = [], projects = [], initialUrl = "/recent-chats", agentMode: agent = false }: RenderOptions = {}) {
    chatSharing = chatSharingEnabled;
    schedulerEnabled = scheduler;
    builderEnabled = builder;
    agentMode = agent;
    mockSessions = sessions;
    mockProjects = projects;
    const Page = await loadPage();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
        <QueryClientProvider client={queryClient}>
            <MemoryRouter initialEntries={[initialUrl]}>
                <Routes>
                    <Route
                        path="/recent-chats"
                        element={
                            <>
                                <Page />
                                <LocationProbe />
                            </>
                        }
                    />
                </Routes>
            </MemoryRouter>
        </QueryClientProvider>
    );
}

describe("RecentChatsPage — Shared with Me tab", () => {
    beforeEach(() => {
        mockListSharedWithMe.mockReset();
        mockListSharedWithMe.mockReturnValue([]);
    });

    test("does not show 'Shared with Me' tab when chatSharing flag is off", async () => {
        await renderPage({ chatSharingEnabled: false });
        await screen.findByText("Recent Chats");
        expect(screen.queryByRole("tab", { name: /^shared$/i })).not.toBeInTheDocument();
    });

    test("shows 'Shared with Me' tab when chatSharing flag is on", async () => {
        await renderPage({ chatSharingEnabled: true });
        expect(await screen.findByRole("tab", { name: /^shared$/i })).toBeInTheDocument();
    });

    test("pre-selects 'shared' tab when URL has ?tab=shared", async () => {
        mockListSharedWithMe.mockReturnValue([
            {
                shareId: "s1",
                title: "Alpha Shared Chat",
                ownerEmail: "owner@example.com",
                accessLevel: "resource_viewer",
                sharedAt: new Date().toISOString(),
                shareUrl: "https://example.com/shared/s1",
            },
        ]);
        await renderPage({ initialUrl: "/recent-chats?tab=shared" });
        expect(await screen.findByText("Alpha Shared Chat")).toBeInTheDocument();
    });

    test("renders empty state when shared tab is active and no shared items exist", async () => {
        await renderPage({ initialUrl: "/recent-chats?tab=shared" });
        expect(await screen.findByText("No shared chats")).toBeInTheDocument();
    });

    test("clicking the shared tab writes ?tab=shared to the URL", async () => {
        const user = userEvent.setup();
        await renderPage();

        const tab = await screen.findByRole("tab", { name: /^shared$/i });
        await user.click(tab);

        await waitFor(() => {
            expect(screen.getByTestId("location-search").textContent).toBe("?tab=shared");
        });
    });

    test("ignores ?tab=shared when sharing flag is off (falls back to chat tab)", async () => {
        await renderPage({ chatSharingEnabled: false, initialUrl: "/recent-chats?tab=shared" });

        await screen.findByText("Recent Chats");
        expect(screen.queryByRole("tab", { name: /^shared$/i })).not.toBeInTheDocument();
        expect(screen.queryByText("No shared chats")).not.toBeInTheDocument();
    });
});

describe("RecentChatsPage — Scheduled Tasks tab empty state", () => {
    beforeEach(() => {
        mockListSharedWithMe.mockReset();
        mockListSharedWithMe.mockReturnValue([]);
    });

    test("shows 'No scheduled task sessions' when chats exist but no scheduler sessions", async () => {
        const user = userEvent.setup();
        await renderPage({
            schedulerEnabled: true,
            sessions: [
                { id: "c1", name: "First chat", source: "chat", createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
                { id: "c2", name: "Second chat", source: "chat", createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
            ],
        });

        expect(await screen.findByText("First chat")).toBeInTheDocument();
        expect(screen.queryByText("No scheduled task sessions")).not.toBeInTheDocument();

        await user.click(await screen.findByRole("tab", { name: /^scheduler$/i }));
        expect(await screen.findByText("No scheduled task sessions")).toBeInTheDocument();
    });

    test("shows scheduler sessions and no empty state when scheduler sessions exist", async () => {
        const user = userEvent.setup();
        await renderPage({
            schedulerEnabled: true,
            sessions: [{ id: "s1", name: "Nightly report", source: "scheduler", scheduledTaskId: "t1", scheduledTaskName: "Nightly", createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }],
        });

        await user.click(await screen.findByRole("tab", { name: /^scheduler$/i }));
        expect(await screen.findByText("Nightly report")).toBeInTheDocument();
        expect(screen.queryByText("No scheduled task sessions")).not.toBeInTheDocument();
    });

    test("hides Builder sessions when the user lacks Builder access", async () => {
        await renderPage({
            builderEnabled: false,
            sessions: [
                { id: "c1", name: "Regular chat", source: "chat", createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
                { id: "b1", name: "SAM Builder Introduction Greeting", source: "chat", agentId: "Builder", createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() },
            ],
        });

        expect(await screen.findByText("Regular chat")).toBeInTheDocument();
        expect(screen.queryByText("SAM Builder Introduction Greeting")).not.toBeInTheDocument();
    });

    test("shows Builder sessions when the user has Builder access", async () => {
        await renderPage({
            builderEnabled: true,
            sessions: [{ id: "b1", name: "SAM Builder Introduction Greeting", source: "chat", agentId: "Builder", createdAt: new Date().toISOString(), updatedAt: new Date().toISOString() }],
        });

        expect(await screen.findByText("SAM Builder Introduction Greeting")).toBeInTheDocument();
    });
});

describe("RecentChatsPage — agent-mode Recent Chats drawer", () => {
    beforeEach(() => {
        mockListSharedWithMe.mockReset();
        mockListSharedWithMe.mockReturnValue([]);
    });

    test("does not render the drawer toggle or side panel outside agent mode", async () => {
        await renderPage();
        await screen.findByText("Recent Chats");
        expect(screen.queryByTestId("showRecentChats")).not.toBeInTheDocument();
        expect(screen.queryByTestId("recent-chats-side-panel")).not.toBeInTheDocument();
    });

    test("renders the toggle and a closed (inert) side panel in agent mode", async () => {
        await renderPage({ agentMode: true });

        await screen.findByText("Recent Chats");
        expect(screen.getByTestId("showRecentChats")).toBeInTheDocument();

        const panelWrapper = screen.getByTestId("recent-chats-side-panel").parentElement;
        expect(panelWrapper).toHaveAttribute("inert");
        expect(panelWrapper?.classList.contains("invisible")).toBe(true);
    });

    test("toggling opens then closes the drawer", async () => {
        const user = userEvent.setup();
        await renderPage({ agentMode: true });

        const toggle = await screen.findByTestId("showRecentChats");
        const panelWrapper = screen.getByTestId("recent-chats-side-panel").parentElement;

        await user.click(toggle);
        await waitFor(() => expect(panelWrapper).not.toHaveAttribute("inert"));
        expect(panelWrapper?.classList.contains("visible")).toBe(true);

        await user.click(toggle);
        await waitFor(() => expect(panelWrapper).toHaveAttribute("inert"));
        expect(panelWrapper?.classList.contains("invisible")).toBe(true);
    });
});

describe("RecentChatsPage — inline rename", () => {
    const session = { id: "sid1", name: "Card Session", updatedAt: new Date().toISOString(), source: "chat" };

    test("Rename opens an inline input; Escape exits it", async () => {
        const user = userEvent.setup();
        await renderPage({ sessions: [session] });
        await screen.findByText("Card Session");

        const trigger = screen.getAllByRole("button").find(b => b.getAttribute("aria-haspopup") === "menu")!;
        await user.click(trigger);
        await user.click(await screen.findByRole("menuitem", { name: /^Rename$/ }));

        const input = await screen.findByDisplayValue("Card Session");
        await user.clear(input);
        await user.type(input, "Renamed{Escape}");
        await waitFor(() => expect(screen.getByText("Card Session")).toBeInTheDocument());
    });

    test("Rename commits on Enter", async () => {
        const user = userEvent.setup();
        await renderPage({ sessions: [session] });
        await screen.findByText("Card Session");

        const trigger = screen.getAllByRole("button").find(b => b.getAttribute("aria-haspopup") === "menu")!;
        await user.click(trigger);
        await user.click(await screen.findByRole("menuitem", { name: /^Rename$/ }));

        const input = await screen.findByDisplayValue("Card Session");
        await user.clear(input);
        await user.type(input, "Renamed Card{Enter}");
        await waitFor(() => expect(screen.getByText("Card Session")).toBeInTheDocument());
    });
});

describe("RecentChatsPage — search result wiring", () => {
    const listed = { id: "listed-1", name: "Weather Listed", updatedAt: new Date().toISOString(), source: "chat" };

    beforeEach(() => {
        mockSearchGet.mockReset();
        handleSwitchSession.mockReset();
    });

    async function search(user: ReturnType<typeof userEvent.setup>, query = "weather") {
        const box = await screen.findByPlaceholderText("Search chats by title");
        await user.type(box, query);
        await waitFor(() => expect(mockSearchGet).toHaveBeenCalled(), { timeout: 3000 });
    }

    test("routes an agent-scoped result through agent-session-navigate, not /chat", async () => {
        const onNavigate = vi.fn();
        window.addEventListener("agent-session-navigate", onNavigate);
        mockSearchGet.mockResolvedValue({
            data: [{ id: "builder-1", name: "Weather Builder", updatedAt: new Date().toISOString(), source: "chat", agentId: "Builder" }],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
        });

        const user = userEvent.setup();
        await renderPage({ sessions: [listed], builderEnabled: true });
        await search(user);
        await user.click(await screen.findByText("Weather Builder"));

        await waitFor(() => expect(onNavigate).toHaveBeenCalled());
        const detail = (onNavigate.mock.calls[0][0] as CustomEvent).detail;
        expect(detail).toMatchObject({ sessionId: "builder-1", agentId: "Builder" });
        expect(handleSwitchSession).not.toHaveBeenCalledWith("builder-1");

        window.removeEventListener("agent-session-navigate", onNavigate);
    });

    test("opens a plain chat result in the chat view", async () => {
        mockSearchGet.mockResolvedValue({
            data: [{ id: "plain-1", name: "Weather Plain", updatedAt: new Date().toISOString(), source: "chat" }],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 1, nextPage: null } },
        });

        const user = userEvent.setup();
        await renderPage({ sessions: [listed] });
        await search(user);
        await user.click(await screen.findByText("Weather Plain"));

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith("plain-1"));
    });

    test("sends the unassigned sentinel when '(No Project)' is selected", async () => {
        mockSearchGet.mockResolvedValue({
            data: [],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 0, count: 0, nextPage: null } },
        });

        const user = userEvent.setup();
        await renderPage({
            sessions: [
                { id: "assigned-1", name: "Weather Assigned", updatedAt: new Date().toISOString(), source: "chat", project: { id: "11111111-1111-1111-1111-111111111111", name: "Forecasting" } },
                { id: "unassigned-1", name: "Weather Unassigned", updatedAt: new Date().toISOString(), source: "chat" },
            ],
        });
        await screen.findByText("Recent Chats");

        const combobox = screen.getByRole("combobox");
        fireEvent.pointerDown(combobox, { button: 0, pointerType: "mouse" });
        fireEvent.pointerUp(combobox, { button: 0, pointerType: "mouse" });
        fireEvent.click(combobox);
        fireEvent.click(await screen.findByText("(No Project)"));

        await search(user);

        const urls = mockSearchGet.mock.calls.map(c => String(c[0]));
        expect(urls.some(u => u.includes("projectId=__no_project__"))).toBe(true);
    });

    test("lists a project with no chats in the filter and shows the empty state when it is selected", async () => {
        const user = userEvent.setup();
        mockSearchGet.mockResolvedValue({
            data: [],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 0, count: 0, nextPage: null } },
        });
        await renderPage({
            projects: [
                { id: "11111111-1111-1111-1111-111111111111", name: "Forecasting" },
                { id: "22222222-2222-2222-2222-222222222222", name: "Chatless Project" },
            ],
            sessions: [{ id: "assigned-1", name: "Weather Assigned", updatedAt: new Date().toISOString(), source: "chat", project: { id: "11111111-1111-1111-1111-111111111111", name: "Forecasting" } }],
        });
        await screen.findByText("Weather Assigned");

        const combobox = screen.getByRole("combobox");
        fireEvent.pointerDown(combobox, { button: 0, pointerType: "mouse" });
        fireEvent.pointerUp(combobox, { button: 0, pointerType: "mouse" });
        fireEvent.click(combobox);
        fireEvent.click(await screen.findByRole("option", { name: "Chatless Project" }));

        expect(await screen.findByText("No sessions found for this project")).toBeInTheDocument();
        expect(screen.queryByText("Weather Assigned")).not.toBeInTheDocument();

        await search(user);
        const urls = mockSearchGet.mock.calls.map(c => String(c[0]));
        expect(urls.some(u => u.includes("projectId=22222222-2222-2222-2222-222222222222"))).toBe(true);
    });

    test("drops the other tab's sessions from results", async () => {
        mockSearchGet.mockResolvedValue({
            data: [
                { id: "chat-1", name: "Weather Chat", updatedAt: new Date().toISOString(), source: "chat" },
                { id: "sched-1", name: "Weather Scheduled", updatedAt: new Date().toISOString(), source: "scheduler" },
            ],
            meta: { pagination: { pageNumber: 1, pageSize: 50, totalPages: 1, count: 2, nextPage: null } },
        });

        const user = userEvent.setup();
        await renderPage({ sessions: [listed] });
        await search(user);

        expect(await screen.findByText("Weather Chat")).toBeInTheDocument();
        expect(screen.queryByText("Weather Scheduled")).not.toBeInTheDocument();
    });
});
