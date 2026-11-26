// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, test, vi } from "vitest";

import { api } from "@/api/client";
import { IDLE_TASKS_POLL_MS, RUNNING_TASKS_POLL_MS } from "@/api/tasks/hooks";

import { queryClient } from "@/providers/query/QueryClient";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { A2AEventSSEPayload, TaskFE } from "@/types";

import { ActivitiesPage } from "./ActivitiesPage";
import { eventTreeIsComplete, mergeDedupeEvents, REHYDRATE_DELAY_MS, REHYDRATE_MAX_ATTEMPTS } from "./taskEventMerge";

const navigateSpy = vi.fn();
const { mockUseAgentCards } = vi.hoisted(() => ({ mockUseAgentCards: vi.fn(() => ({ data: [] as unknown[] })) }));
vi.mock("@/api/agent-cards/hooks", () => ({ useAgentCards: mockUseAgentCards }));
vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return {
        ...actual,
        useNavigate: () => {
            const realNavigate = actual.useNavigate();
            return (...args: Parameters<ReturnType<typeof actual.useNavigate>>) => {
                navigateSpy(...args);
                return realNavigate(...args);
            };
        },
    };
});

const mockBulkArtifact = (filename: string, sessionId: string) => ({
    filename,
    size: 1024,
    mimeType: "text/markdown",
    lastModified: "2026-01-01T00:00:00Z",
    uri: `artifact://${sessionId}/${filename}`,
    sessionId,
    sessionName: "Chat 1",
    projectId: null,
    projectName: null,
    source: "generated",
    tags: null,
});

function routeGet(apiGetSpy: ReturnType<typeof vi.spyOn>, opts: { artifacts?: unknown[]; tasks?: unknown[] } = {}) {
    apiGetSpy.mockImplementation((endpoint: unknown) => {
        const url = String(endpoint);
        if (url.includes("/api/v1/tasks")) {
            return Promise.resolve({ data: opts.tasks ?? [] });
        }
        if (url.includes("/api/v1/artifacts/all")) {
            return Promise.resolve({ artifacts: opts.artifacts ?? [], nextPage: null });
        }
        return Promise.resolve({ data: [] });
    });
}

const activitiesPageTree = (chatContext = {}, providers: Pick<React.ComponentProps<typeof StoryProvider>, "userContextValues" | "flagValues"> = {}) => (
    <MemoryRouter>
        <StoryProvider chatContextValues={{ sessionId: "test", ...chatContext }} taskContextValues={{ monitoredTasks: {}, monitoredTaskOrder: [] }} {...providers}>
            <ActivitiesPage />
        </StoryProvider>
    </MemoryRouter>
);

function renderActivitiesPage(chatContext = {}, providers: Pick<React.ComponentProps<typeof StoryProvider>, "userContextValues" | "flagValues"> = {}) {
    return render(activitiesPageTree(chatContext, providers));
}

const tasksRequestUrls = (apiGetSpy: ReturnType<typeof vi.spyOn>): string[] => apiGetSpy.mock.calls.map((call: unknown[]) => String(call[0])).filter((url: string) => url.includes("/api/v1/tasks?"));

const sessionRowOf = (text: string): HTMLElement => {
    const row = screen.getAllByTestId("session-list-item").find(el => el.textContent?.includes(text));
    if (!row) throw new Error(`no session row for ${text}`);
    return row;
};

const taskRowOf = (text: string): HTMLElement => {
    const row = screen.getAllByTestId("task-list-item").find(el => el.textContent?.includes(text));
    if (!row) throw new Error(`no task row for ${text}`);
    return row;
};

const openTask = async (user: ReturnType<typeof userEvent.setup>, taskText: string, sessionText = taskText) => {
    await screen.findByText(sessionText);
    const session = sessionRowOf(sessionText);
    if (session.dataset.expanded !== "true") {
        await user.click(within(session).getByRole("button", { expanded: false }));
    }
    await user.click(within(taskRowOf(taskText)).getByRole("button"));
    return screen.findByTestId("task-detail-panel");
};

describe("ActivitiesPage artifact fetch", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        queryClient.clear();
    });

    test("fetches artifacts from /api/v1/artifacts/all on mount", async () => {
        routeGet(apiGetSpy, { artifacts: [mockBulkArtifact("report.md", "session-1")] });

        renderActivitiesPage();

        await waitFor(() => {
            expect(apiGetSpy).toHaveBeenCalledWith(expect.stringContaining("/api/v1/artifacts/all"));
        });
    });

    test("silently handles fetch errors", async () => {
        apiGetSpy.mockRejectedValue(new Error("Network error"));

        renderActivitiesPage();

        await waitFor(() => {
            expect(apiGetSpy).toHaveBeenCalled();
        });
        expect(screen.getByText("Activities")).toBeInTheDocument();
    });
});

describe("ActivitiesPage task-logger list", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    const taskRow = (id: string, requestText: string, status = "completed") => ({
        id,
        initialRequestText: requestText,
        startTime: "2026-01-01T00:00:00Z",
        lastActivityTime: "2026-01-01T00:00:05Z",
        status,
    });

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        queryClient.clear();
    });

    test("renders the task list from GET /api/v1/tasks", async () => {
        routeGet(apiGetSpy, { tasks: [taskRow("task-rest-1", "First REST task"), taskRow("task-rest-2", "Second REST task")] });

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: {}, monitoredTaskOrder: [] },
        });

        await waitFor(() => {
            expect(apiGetSpy).toHaveBeenCalledWith(expect.stringContaining("/api/v1/tasks?pageSize="));
        });
        await screen.findByText("First REST task");
        expect(screen.getByText("Second REST task")).toBeInTheDocument();
    });

    test("expands a session and selects a task from the keyboard with Enter", async () => {
        routeGet(apiGetSpy, {
            tasks: [
                { ...taskRow("task-rest-1", "First REST task"), sessionId: "sess-a" },
                { ...taskRow("task-rest-2", "Second REST task"), sessionId: "sess-a" },
            ],
        });

        renderActivitiesPage();

        const session = await screen.findByTestId("session-list-item");
        within(session).getByRole("button", { expanded: false }).focus();
        await userEvent.keyboard("{Enter}");

        within(taskRowOf("Second REST task")).getByRole("button").focus();
        await userEvent.keyboard("{Enter}");

        const panel = await screen.findByTestId("task-detail-panel");
        expect(within(panel).getByTestId("task-heading")).toHaveTextContent("Second REST task");
        expect(taskRowOf("Second REST task")).toHaveClass("bg-(--primary-w10)");
        expect(taskRowOf("First REST task")).not.toHaveClass("bg-(--primary-w10)");
    });

    test("shows the eager eventCount from the list before any row is hydrated", async () => {
        routeGet(apiGetSpy, {
            tasks: [
                { ...taskRow("task-rest-1", "First REST task"), eventCount: 3 },
                { ...taskRow("task-rest-2", "Second REST task"), eventCount: 12 },
            ],
        });

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: {}, monitoredTaskOrder: [] },
        });

        await screen.findByText("Second REST task");
        expect(screen.getByText("Events: 3")).toBeInTheDocument();
        expect(screen.getByText("Events: 12")).toBeInTheDocument();
    });

    test("names the user each listed task ran as", async () => {
        routeGet(apiGetSpy, {
            tasks: [{ ...taskRow("task-mine", "My task"), userId: "me@example.com" }, { ...taskRow("task-hook", "Webhook run"), userId: "system:default" }, taskRow("task-old", "Row from an older entrypoint")],
        });

        renderActivitiesPage();

        await screen.findByText("Webhook run");
        expect(within(sessionRowOf("My task")).getByTestId("session-list-item-user")).toHaveTextContent("me@example.com");
        expect(within(sessionRowOf("Webhook run")).getByTestId("session-list-item-user")).toHaveTextContent("system:default");
        expect(within(sessionRowOf("Row from an older entrypoint")).queryByTestId("session-list-item-user")).toBeNull();
    });

    test("keeps the fresh list's user on a row already hydrated in the task store", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("task-1", "Task one"), userId: "system:default" }] });

        const stored: TaskFE = {
            taskId: "task-1",
            initialRequestText: "Task one",
            events: [],
            firstSeen: new Date("2026-01-01T00:00:00Z"),
            lastUpdated: new Date("2026-01-01T00:00:05Z"),
            needsHistoricalHydration: false,
        };

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "task-1": stored }, monitoredTaskOrder: ["task-1"] },
        });

        await screen.findByText("Task one");
        expect(screen.getByTestId("session-list-item-user")).toHaveTextContent("system:default");
    });

    test("names the workflow and what started each run from the fresh list", async () => {
        routeGet(apiGetSpy, {
            tasks: [
                {
                    ...taskRow("task-1", "Task one"),
                    subject: { id: "workflow_01a1", type: "workflow", name: "Dependency update" },
                    trigger: { kind: "event", channel: "webhook", entrypointName: "github-deps", event: "check_suite.completed" },
                },
                taskRow("task-old", "Row from before triggers were recorded"),
            ],
        });

        const stored: TaskFE = {
            taskId: "task-1",
            initialRequestText: "Task one",
            events: [],
            firstSeen: new Date("2026-01-01T00:00:00Z"),
            lastUpdated: new Date("2026-01-01T00:00:05Z"),
            needsHistoricalHydration: false,
        };

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "task-1": stored }, monitoredTaskOrder: ["task-1"] },
        });

        await screen.findByText("Row from before triggers were recorded");
        const user = userEvent.setup();
        for (const text of ["Task one", "Row from before triggers were recorded"]) {
            await user.click(within(sessionRowOf(text)).getByRole("button", { expanded: false }));
        }
        const rowOf = taskRowOf;
        expect(within(rowOf("Task one")).getByTestId("task-list-item-origin")).toHaveTextContent("Dependency update · webhook · github-deps · check_suite.completed");
        expect(within(rowOf("Row from before triggers were recorded")).queryByTestId("task-list-item-origin")).toBeNull();
    });

    test("a hydrated sub-task stays nested and names only its agent, not the root's trigger", async () => {
        const trigger = { kind: "event", channel: "webhook", entrypointName: "github-deps", event: "check_suite.completed" };
        routeGet(apiGetSpy, {
            tasks: [
                { ...taskRow("task-root", "Root run"), subject: { id: "workflow_01a1", type: "workflow", name: "Dependency update" }, trigger },
                { ...taskRow("task-child", "Child run"), parentTaskId: "task-root", subject: { id: "reviewer", type: "agent", name: "Reviewer" }, trigger },
            ],
        });

        const stored: TaskFE = {
            taskId: "task-child",
            initialRequestText: "Child run",
            events: [],
            firstSeen: new Date("2026-01-01T00:00:00Z"),
            lastUpdated: new Date("2026-01-01T00:00:05Z"),
            needsHistoricalHydration: false,
        };

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "task-child": stored }, monitoredTaskOrder: ["task-child"] },
        });

        await screen.findAllByText("Root run");
        const user = userEvent.setup();
        await user.click(within(sessionRowOf("Root run")).getByRole("button", { expanded: false }));
        await user.click(screen.getByTitle("Expand sub-tasks"));

        const childRow = screen.getAllByTestId("task-list-item").find(el => el.textContent?.includes("Child run"))!;
        const origin = within(childRow).getByTestId("task-list-item-origin");
        expect(origin).toHaveTextContent("Reviewer");
        expect(origin).not.toHaveTextContent("webhook");
    });

    test("renders the fresh list eventCount even when the row is already in the task store", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("task-1", "Task one"), eventCount: 9 }] });

        const stored: TaskFE = {
            taskId: "task-1",
            initialRequestText: "Task one",
            events: [],
            eventCount: 3,
            firstSeen: new Date("2026-01-01T00:00:00Z"),
            lastUpdated: new Date("2026-01-01T00:00:05Z"),
            needsHistoricalHydration: false,
        };

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "task-1": stored }, monitoredTaskOrder: ["task-1"] },
        });

        await screen.findByText("Task one");
        expect(screen.getByText("Events: 9")).toBeInTheDocument();
        expect(screen.queryByText("Events: 3")).not.toBeInTheDocument();
    });

    test("Refresh button refetches the task list", async () => {
        routeGet(apiGetSpy, { tasks: [taskRow("task-rest-1", "First REST task")] });
        const user = userEvent.setup();

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: {}, monitoredTaskOrder: [] },
        });

        await screen.findByText("First REST task");

        const tasksCallsBefore = apiGetSpy.mock.calls.filter(([url]: [unknown]) => String(url).includes("/api/v1/tasks")).length;
        expect(tasksCallsBefore).toBeGreaterThanOrEqual(1);

        await user.click(screen.getByTestId("refresh-tasks"));

        await waitFor(() => {
            const tasksCallsAfter = apiGetSpy.mock.calls.filter(([url]: [unknown]) => String(url).includes("/api/v1/tasks")).length;
            expect(tasksCallsAfter).toBeGreaterThan(tasksCallsBefore);
        });
    });

    test("shows an error state when the task list request fails", async () => {
        apiGetSpy.mockImplementation((endpoint: unknown) => {
            const url = String(endpoint);
            if (url.includes("/api/v1/tasks")) {
                return Promise.reject(new Error("Task list unavailable"));
            }
            if (url.includes("/api/v1/artifacts/all")) {
                return Promise.resolve({ artifacts: [], nextPage: null });
            }
            return Promise.resolve({ data: [] });
        });

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: {}, monitoredTaskOrder: [] },
        });

        await screen.findByText("Something went wrong");
        expect(screen.getByText("Task list unavailable")).toBeInTheDocument();
    });
});

describe("ActivitiesPage session list and task details panel", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    const taskRow = (id: string, requestText: string, extra: Record<string, unknown> = {}) => ({
        id,
        initialRequestText: requestText,
        startTime: "2026-01-01T00:00:00Z",
        lastActivityTime: "2026-01-01T00:00:05Z",
        status: "completed",
        endTime: "2026-01-01T00:00:05Z",
        ...extra,
    });

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        queryClient.clear();
    });

    test("groups the turns of one chat session under a single session row that expands to its tasks", async () => {
        routeGet(apiGetSpy, {
            tasks: [
                { ...taskRow("turn-2", "Second turn", { lastActivityTime: "2026-01-01T00:01:00Z" }), sessionId: "sess-a", eventCount: 4 },
                { ...taskRow("other", "Other session"), sessionId: "sess-b", eventCount: 2 },
                { ...taskRow("turn-1", "First turn"), sessionId: "sess-a", eventCount: 6 },
            ],
        });
        const user = userEvent.setup();

        renderActivitiesPage();

        await screen.findByText("Second turn");
        expect(screen.getAllByTestId("session-list-item")).toHaveLength(2);
        expect(screen.queryAllByTestId("task-list-item")).toHaveLength(0);
        expect(screen.queryByText("First turn")).not.toBeInTheDocument();

        const session = sessionRowOf("Second turn");
        expect(within(session).getByTestId("session-list-item-turns")).toHaveTextContent("2 turns");
        expect(within(session).getByText("Events: 10")).toBeInTheDocument();
        expect(within(sessionRowOf("Other session")).getByTestId("session-list-item-turns")).toHaveTextContent("1 turn");

        await user.click(within(session).getByRole("button", { expanded: false }));
        const rows = within(session).getAllByTestId("task-list-item");
        expect(rows.map(row => row.textContent)).toEqual([expect.stringContaining("Second turn"), expect.stringContaining("First turn")]);
        expect(screen.queryByTestId("task-detail-panel")).not.toBeInTheDocument();
    });

    test("titles a session row with the chat session's name when the list carries it, else with the latest turn", async () => {
        routeGet(apiGetSpy, {
            tasks: [
                { ...taskRow("named-2", "Second named turn"), sessionId: "sess-named", sessionName: "Weather in Ottawa" },
                { ...taskRow("named-1", "First named turn"), sessionId: "sess-named", sessionName: "Weather in Ottawa" },
                { ...taskRow("anon-1", "Untitled turn"), sessionId: "sess-anon" },
            ],
        });

        renderActivitiesPage();

        await screen.findByText("Weather in Ottawa");
        expect(apiGetSpy).toHaveBeenCalledWith(expect.stringContaining("expand=sessionName"));
        const titles = screen.getAllByTestId("session-list-item-title").map(el => el.textContent);
        expect(titles).toEqual(["Weather in Ottawa", "Untitled turn"]);
        expect(within(sessionRowOf("Weather in Ottawa")).getByRole("button")).toHaveAttribute("title", expect.stringContaining("Latest turn: Second named turn"));
    });

    test("opens the task details in a full-width sheet on a mobile viewport", async () => {
        const desktopWidth = globalThis.innerWidth;
        globalThis.innerWidth = 500;
        try {
            routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
            const user = userEvent.setup();

            renderActivitiesPage();

            const panel = await openTask(user, "Inspect me");
            expect(panel.closest('[role="dialog"]')).not.toBeNull();
            await user.click(within(panel).getByTestId("close-task-details"));
            await waitFor(() => expect(screen.queryByTestId("task-detail-panel")).not.toBeInTheDocument());
        } finally {
            globalThis.innerWidth = desktopWidth;
        }
    });

    test("re-reads the list when the page is opened again, so a chat started meanwhile is listed", async () => {
        routeGet(apiGetSpy, { tasks: [taskRow("t-old", "Before the chat")] });

        const { unmount } = renderActivitiesPage();
        await screen.findByText("Before the chat");
        expect(tasksRequestUrls(apiGetSpy).length).toBe(1);
        unmount();

        routeGet(apiGetSpy, { tasks: [taskRow("t-new", "The new chat"), taskRow("t-old", "Before the chat")] });
        renderActivitiesPage();

        await screen.findByText("The new chat");
        expect(tasksRequestUrls(apiGetSpy).length).toBe(2);
    });

    test("searches on the server once typing settles and opens the sessions it finds", async () => {
        routeGet(apiGetSpy, {
            tasks: [
                { ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" },
                { ...taskRow("t-2", "Leave me"), sessionId: "sess-b" },
            ],
        });
        const user = userEvent.setup();

        renderActivitiesPage();

        await screen.findByText("Inspect me");
        const input = screen.getByTestId("task-search-input");
        expect(input).toHaveAttribute("placeholder", "Search by session, task or user ID");

        const hit = [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }];
        const session = [
            { ...taskRow("t-1b", "Second turn"), sessionId: "sess-a" },
            { ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" },
        ];
        apiGetSpy.mockImplementation((endpoint: unknown) => {
            const url = String(endpoint);
            if (url.includes("sessionIds=sess-a")) return Promise.resolve({ data: session });
            if (url.includes("search=t-1")) return Promise.resolve({ data: hit });
            return Promise.resolve(url.includes("/api/v1/tasks") ? { data: [] } : { artifacts: [], nextPage: null });
        });
        await user.type(input, " t-1 ");
        await waitFor(() => expect(tasksRequestUrls(apiGetSpy).at(-1)).toContain("search=t-1"));
        expect(tasksRequestUrls(apiGetSpy).filter((url: string) => url.includes("search=")).length).toBe(1);
        await waitFor(() => expect(screen.getAllByText("Second turn")).toHaveLength(2));
        expect(tasksRequestUrls(apiGetSpy).at(-1)).toContain("sessionIds=sess-a");
        expect(screen.queryByText("Leave me")).not.toBeInTheDocument();
        expect(sessionRowOf("Second turn")).toHaveAttribute("data-expanded", "true");
        expect(screen.getAllByTestId("task-list-item")).toHaveLength(2);

        await user.clear(input);
        await user.type(input, "nothing");
        await screen.findByText("No matching sessions");
        expect(screen.getByTestId("task-search-input")).toBeInTheDocument();
    });

    test("hides the owner checkbox from a user who can only see their own tasks", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:*:invoke"], hasScope: () => false } });

        await screen.findByText("Inspect me");
        expect(screen.queryByTestId("task-owner-filter")).not.toBeInTheDocument();
        expect(tasksRequestUrls(apiGetSpy)[0]).not.toContain("userId=");
    });

    test("lets a trace reader narrow the list to their own tasks", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const user = userEvent.setup();

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:01a1-abc:read_trace"], hasScope: () => false } });

        await screen.findByText("Inspect me");
        const checkbox = screen.getByRole("checkbox", { name: "Show my sessions only" });
        expect(checkbox).toBeChecked();
        expect(tasksRequestUrls(apiGetSpy)).toHaveLength(1);
        expect(tasksRequestUrls(apiGetSpy)[0]).toContain("userId=sam_dev_user");

        await user.click(checkbox);
        expect(checkbox).not.toBeChecked();
        await waitFor(() => expect(tasksRequestUrls(apiGetSpy).at(-1)).not.toContain("userId="));
    });

    test("shows the owner checkbox to a holder of a verb wildcard on an agent, as the server resolves it", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:01a1-abc:*"], hasScope: () => false } });

        await screen.findByText("Inspect me");
        expect(screen.getByRole("checkbox", { name: "Show my sessions only" })).toBeChecked();
    });

    test("waits for the scopes before the first request, then sends it owner-scoped", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const traceReader = { username: "sam_dev_user", scopes: ["agent:01a1-abc:read_trace"], hasScope: () => false };

        const { rerender } = renderActivitiesPage({}, { userContextValues: { ...traceReader, capabilitiesLoaded: false } });

        await waitFor(() => expect(apiGetSpy).toHaveBeenCalledWith(expect.stringContaining("/api/v1/artifacts/all")));
        expect(screen.getByText("Loading tasks...")).toBeInTheDocument();
        expect(tasksRequestUrls(apiGetSpy)).toHaveLength(0);

        rerender(activitiesPageTree({}, { userContextValues: { ...traceReader, capabilitiesLoaded: true } }));

        await screen.findByText("Inspect me");
        expect(tasksRequestUrls(apiGetSpy)).toHaveLength(1);
        expect(tasksRequestUrls(apiGetSpy)[0]).toContain("userId=sam_dev_user");
    });

    test("keeps the owner filter on the session fetch a search triggers", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const user = userEvent.setup();

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:01a1-abc:read_trace"], hasScope: () => false } });

        await screen.findByText("Inspect me");
        await user.type(screen.getByTestId("task-search-input"), "t-1");
        await waitFor(() => expect(tasksRequestUrls(apiGetSpy).at(-1)).toContain("sessionIds=sess-a"));
        const sessionFetch = tasksRequestUrls(apiGetSpy).at(-1) ?? "";
        expect(sessionFetch).toContain("userId=sam_dev_user");
        expect(tasksRequestUrls(apiGetSpy).find((url: string) => url.includes("search=t-1"))).toContain("userId=sam_dev_user");
    });

    test("tells a trace reader with no tasks of their own which filter hides the rest", async () => {
        routeGet(apiGetSpy, { tasks: [] });

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:01a1-abc:read_trace"], hasScope: () => false } });

        await screen.findByText("No sessions of yours yet");
        expect(screen.getByText(`Clear "Show my sessions only" to see everyone's`)).toBeInTheDocument();
    });

    test("gives a task that ran outside any session its own session row", async () => {
        routeGet(apiGetSpy, { tasks: [taskRow("hook-1", "Webhook run"), taskRow("hook-2", "Another webhook run")] });

        renderActivitiesPage();

        await screen.findByText("Webhook run");
        expect(screen.getAllByTestId("session-list-item")).toHaveLength(2);
    });

    const hydratedEmptyStub = (taskId: string, requestText: string): TaskFE => ({
        taskId,
        initialRequestText: requestText,
        events: [],
        firstSeen: new Date("2026-01-01T00:00:00Z"),
        lastUpdated: new Date("2026-01-01T00:00:05Z"),
        needsHistoricalHydration: false,
    });

    test("opens the details panel on a task row click and closes it from the header", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const user = userEvent.setup();

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "t-1": hydratedEmptyStub("t-1", "Inspect me") }, monitoredTaskOrder: ["t-1"] },
        });

        await screen.findByText("Inspect me");
        expect(screen.queryByTestId("task-detail-panel")).not.toBeInTheDocument();

        const panel = await openTask(user, "Inspect me");
        expect(within(panel).getByTestId("task-heading")).toHaveTextContent("Inspect me");
        expect(within(panel).getByTestId("task-id")).toHaveTextContent("t-1");
        expect(within(panel).getByTestId("step-view-panel")).toBeInTheDocument();

        await user.click(within(panel).getByTestId("close-task-details"));
        expect(screen.queryByTestId("task-detail-panel")).not.toBeInTheDocument();
    });

    test("closes the details panel on Escape", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const user = userEvent.setup();

        renderActivitiesPage();

        await openTask(user, "Inspect me");
        await user.keyboard("{Escape}");
        expect(screen.queryByTestId("task-detail-panel")).not.toBeInTheDocument();
    });

    test("keeps the details panel open when Escape is pressed in the search box", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const user = userEvent.setup();

        renderActivitiesPage();

        await openTask(user, "Inspect me");
        fireEvent.keyDown(screen.getByTestId("task-search-input"), { key: "Escape" });
        expect(screen.getByTestId("task-detail-panel")).toBeInTheDocument();
    });

    test("keeps the details panel open when Escape is pressed inside the Performance Insights dialog", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const user = userEvent.setup();

        renderActivitiesPage();

        const panel = await openTask(user, "Inspect me");
        await user.click(within(panel).getByTestId("view-performance-insights"));
        const modal = await screen.findByRole("dialog", { name: /Performance Insights/i });
        expect(modal).toBeInTheDocument();

        await user.keyboard("{Escape}");
        await waitFor(() => expect(screen.queryByRole("dialog", { name: /Performance Insights/i })).not.toBeInTheDocument());
        expect(screen.getByTestId("task-detail-panel")).toBeInTheDocument();
    });

    test("switches the details panel between the step view and the raw event view", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const user = userEvent.setup();

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "t-1": hydratedEmptyStub("t-1", "Inspect me") }, monitoredTaskOrder: ["t-1"] },
        });

        const panel = await openTask(user, "Inspect me");
        await user.click(within(panel).getByTestId("toggle-event-view"));
        expect(within(panel).getByTestId("raw-event-view-panel")).toBeInTheDocument();
        expect(within(panel).queryByTestId("step-view-panel")).not.toBeInTheDocument();

        await user.click(within(panel).getByTestId("toggle-right-panel"));
        expect(within(panel).queryByTestId("event-panel-content")).not.toBeInTheDocument();
    });

    test("hydrates a task's persisted events when its details panel opens", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("t-1", "Inspect me"), sessionId: "sess-a" }] });
        const loadSpy = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        const user = userEvent.setup();

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: {}, monitoredTaskOrder: [], loadTaskFromBackend: loadSpy },
        });

        await screen.findByText("Inspect me");
        expect(loadSpy).not.toHaveBeenCalled();
        await openTask(user, "Inspect me");
        await waitFor(() => expect(loadSpy).toHaveBeenCalledWith("t-1"));
    });
});

describe("ActivitiesPage live activity for a running task", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    class MockEventSource {
        static instances: MockEventSource[] = [];
        url: string;
        private listeners: Record<string, ((e: MessageEvent) => void)[]> = {};
        constructor(url: string) {
            this.url = url;
            MockEventSource.instances.push(this);
        }
        addEventListener(type: string, cb: (e: MessageEvent) => void) {
            (this.listeners[type] ??= []).push(cb);
        }
        removeEventListener(type: string, cb: (e: MessageEvent) => void) {
            this.listeners[type] = (this.listeners[type] ?? []).filter(l => l !== cb);
        }
        close() {}
        emit(type: string, data: string) {
            (this.listeners[type] ?? []).forEach(cb => cb({ data } as MessageEvent));
        }
        static latest() {
            return MockEventSource.instances[MockEventSource.instances.length - 1];
        }
    }

    const activityFrame = (taskId: string) =>
        JSON.stringify({
            event_type: "a2a_message",
            timestamp: "2026-01-01T00:00:06.000Z",
            solace_topic: `ns/a2a/v1/gateway/status/gw/${taskId}`,
            direction: "status_update",
            source_entity: "RunnerAgent",
            target_entity: "unknown",
            task_id: taskId,
            payload_summary: {},
            full_payload: { result: { status: { state: "working" } } },
        });

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
        MockEventSource.instances = [];
        vi.stubGlobal("EventSource", MockEventSource);
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        vi.unstubAllGlobals();
        queryClient.clear();
    });

    test("opens a per-task activity_event subscription for a task with no endTime, since the backend never writes a 'running' status", async () => {
        routeGet(apiGetSpy, {
            tasks: [{ id: "run-1", initialRequestText: "Running task", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z" }],
        });

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: {}, monitoredTaskOrder: [] },
        });

        await openTask(userEvent.setup(), "Running task");
        await waitFor(() => {
            expect(MockEventSource.instances.some(i => i.url.includes("/api/v1/sse/subscribe/run-1"))).toBe(true);
        });

        act(() => {
            MockEventSource.latest().emit("activity_event", activityFrame("run-1"));
        });
        expect(screen.getAllByText("Running task").length).toBeGreaterThan(0);
    });

    test("does NOT open a subscription for a completed task", async () => {
        routeGet(apiGetSpy, {
            tasks: [{ id: "done-1", initialRequestText: "Finished task", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z", status: "completed", endTime: "2026-01-01T00:00:05Z" }],
        });

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: {}, monitoredTaskOrder: [] },
        });

        await openTask(userEvent.setup(), "Finished task");
        await act(async () => {
            await new Promise(resolve => setTimeout(resolve, 50));
        });
        expect(MockEventSource.instances.some(i => i.url.includes("/api/v1/sse/subscribe/done-1"))).toBe(false);
    });
});

describe("mergeDedupeEvents", () => {
    const ev = (over: Partial<A2AEventSSEPayload>): A2AEventSSEPayload => ({
        event_type: "a2a_message",
        timestamp: "2026-01-01T00:00:01.000Z",
        solace_topic: "ns/a2a/v1/gateway/status/gw/t1",
        direction: "status_update",
        source_entity: "A",
        target_entity: "B",
        task_id: "t1",
        payload_summary: {},
        full_payload: {},
        ...over,
    });

    it("drops a frame present in both backfill and live, keeping one", () => {
        const dup = ev({ timestamp: "2026-01-01T00:00:01.000Z", message_id: "m-1" });
        const merged = mergeDedupeEvents([dup], [{ ...dup }]);
        expect(merged).toHaveLength(1);
    });

    it("keeps distinct frames and sorts by timestamp", () => {
        const a = ev({ timestamp: "2026-01-01T00:00:03.000Z", message_id: "m-3", direction: "response" });
        const b = ev({ timestamp: "2026-01-01T00:00:01.000Z", message_id: "m-1", direction: "request" });
        const c = ev({ timestamp: "2026-01-01T00:00:02.000Z", message_id: "m-2" });
        const merged = mergeDedupeEvents([b], [a, c]);
        expect(merged.map(e => e.message_id)).toEqual(["m-1", "m-2", "m-3"]);
    });

    it("treats same-ms frames with different message_id as distinct", () => {
        const a = ev({ timestamp: "2026-01-01T00:00:01.000Z", message_id: "m-1" });
        const b = ev({ timestamp: "2026-01-01T00:00:01.000Z", message_id: "m-2" });
        expect(mergeDedupeEvents([a], [b])).toHaveLength(2);
    });

    it("dedupes the same event across live and backfill despite divergent timestamp/direction", () => {
        const persisted = ev({
            timestamp: "2026-01-01T00:00:01.000Z",
            direction: "status",
            full_payload: { result: { status: { message: { messageId: "abc-123" } } } },
        });
        const liveFrame = ev({
            timestamp: "2026-01-01T00:00:01.842Z",
            direction: "status_update",
            full_payload: { result: { status: { message: { messageId: "abc-123" } } } },
        });
        expect(mergeDedupeEvents([persisted], [liveFrame])).toHaveLength(1);
    });

    it("keeps distinct inner messageIds", () => {
        const a = ev({ full_payload: { result: { status: { message: { messageId: "id-a" } } } } });
        const b = ev({ full_payload: { result: { status: { message: { messageId: "id-b" } } } } });
        expect(mergeDedupeEvents([a], [b])).toHaveLength(2);
    });

    it("dedupes a request frame across sources by params.message.messageId", () => {
        const persisted = ev({ direction: "request", timestamp: "2026-01-01T00:00:01.000Z", full_payload: { params: { message: { messageId: "req-1" } } } });
        const liveFrame = ev({ direction: "request", timestamp: "2026-01-01T00:00:01.300Z", full_payload: { params: { message: { messageId: "req-1" } } } });
        expect(mergeDedupeEvents([persisted], [liveFrame])).toHaveLength(1);
    });

    it("does not collapse distinct artifact_update frames (no inner messageId → composite key)", () => {
        const a = ev({ direction: "artifact_update", timestamp: "2026-01-01T00:00:01.000Z", full_payload: { result: { artifact: { artifactId: "art-1" } } } });
        const b = ev({ direction: "artifact_update", timestamp: "2026-01-01T00:00:02.000Z", full_payload: { result: { artifact: { artifactId: "art-1" } } } });
        expect(mergeDedupeEvents([a], [b])).toHaveLength(2);
    });

    it("falls back to the composite key for frames with no inner messageId", () => {
        const a = ev({ message_id: "x", timestamp: "2026-01-01T00:00:01.000Z", full_payload: {} });
        const b = ev({ message_id: "y", timestamp: "2026-01-01T00:00:02.000Z", full_payload: {} });
        expect(mergeDedupeEvents([a], [b])).toHaveLength(2);
    });

    describe("eventTreeIsComplete", () => {
        const ev2 = (direction: string): A2AEventSSEPayload => ({
            event_type: "a2a_message",
            timestamp: "2026-01-01T00:00:01.000Z",
            solace_topic: "ns/a2a/v1/gateway/status/gw/t1",
            direction,
            source_entity: "A",
            target_entity: "B",
            task_id: "t1",
            payload_summary: {},
            full_payload: {},
        });

        it("is false for an empty or undefined tree", () => {
            expect(eventTreeIsComplete(undefined)).toBe(false);
            expect(eventTreeIsComplete([])).toBe(false);
        });

        it("is false for a partial tree with no terminal frame (backfill race)", () => {
            expect(eventTreeIsComplete([ev2("request"), ev2("status-update")])).toBe(false);
        });

        it("is true once the final response frame is present", () => {
            expect(eventTreeIsComplete([ev2("request"), ev2("status-update"), ev2("task")])).toBe(true);
        });

        it("is true for a tree that terminated in an error", () => {
            expect(eventTreeIsComplete([ev2("request"), ev2("error_response")])).toBe(true);
        });
    });
});

describe("ActivitiesPage backfill-race re-hydrate", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        queryClient.clear();
    });

    const requestOnlyEvent: A2AEventSSEPayload = {
        event_type: "a2a_message",
        timestamp: "2026-01-01T00:00:00.000Z",
        solace_topic: "ns/a2a/v1/gateway/status/gw/done-x",
        direction: "request",
        source_entity: "user",
        target_entity: "done-x",
        task_id: "done-x",
        payload_summary: {},
        full_payload: {},
    };

    test("re-fetches a finished task whose backfill lacks a terminal frame", async () => {
        routeGet(apiGetSpy, {
            tasks: [{ id: "done-x", initialRequestText: "Done task", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z", status: "completed", endTime: "2026-01-01T00:00:05Z" }],
        });
        const loadSpy = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        const partial: TaskFE = {
            taskId: "done-x",
            initialRequestText: "Done task",
            events: [requestOnlyEvent],
            firstSeen: new Date("2026-01-01T00:00:00Z"),
            lastUpdated: new Date("2026-01-01T00:00:00Z"),
            needsHistoricalHydration: false,
        };

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "done-x": partial }, monitoredTaskOrder: ["done-x"], loadTaskFromBackend: loadSpy },
        });

        await openTask(userEvent.setup(), "Done task");
        await waitFor(() => expect(loadSpy).toHaveBeenCalledWith("done-x"), { timeout: 3000 });
    });
});

describe("ActivitiesPage token usage", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    const taskRow = (id: string, requestText: string, extra: Record<string, unknown> = {}) => ({
        id,
        initialRequestText: requestText,
        startTime: "2026-01-01T00:00:00Z",
        lastActivityTime: "2026-01-01T00:00:05Z",
        status: "completed",
        endTime: "2026-01-01T00:00:05Z",
        ...extra,
    });

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        queryClient.clear();
    });

    test("a session row sums its turns and sub-tasks, each task row shows its own, and a row without usage shows none", async () => {
        routeGet(apiGetSpy, {
            tasks: [
                { ...taskRow("turn-2", "Second turn", { lastActivityTime: "2026-01-01T00:01:00Z" }), sessionId: "sess-a", totalInputTokens: 1200, totalOutputTokens: 300 },
                { ...taskRow("sub-1", "Delegated work"), sessionId: "sess-a", parentTaskId: "turn-2", totalInputTokens: 50, totalOutputTokens: 5 },
                { ...taskRow("turn-1", "First turn"), sessionId: "sess-a" },
                { ...taskRow("silent", "No usage"), sessionId: "sess-b" },
            ],
        });
        const user = userEvent.setup();
        renderActivitiesPage();

        await screen.findByText("Second turn");
        const session = sessionRowOf("Second turn");
        const sessionTokens = within(session).getByTestId("session-list-item-tokens");
        expect(sessionTokens.textContent).toBe("1.3K305");
        expect(sessionTokens).toHaveAttribute("title", "Tokens: 1,555 total · 1,250 sent (prompt, cached input included) · 305 received (completion)");
        expect(within(sessionRowOf("No usage")).queryByTestId("session-list-item-tokens")).not.toBeInTheDocument();

        await user.click(within(session).getByRole("button", { expanded: false }));
        expect(within(taskRowOf("Second turn")).getByTestId("task-list-item-tokens").textContent).toBe("1.2K300");
        expect(within(taskRowOf("First turn")).queryByTestId("task-list-item-tokens")).not.toBeInTheDocument();

        await user.click(within(taskRowOf("Second turn")).getByTitle("Expand sub-tasks"));
        expect(within(taskRowOf("Delegated work")).getByTestId("task-list-item-tokens").textContent).toBe("505");
    });

    test("shows the list's token totals even when the row is already in the task store without them", async () => {
        routeGet(apiGetSpy, { tasks: [{ ...taskRow("task-1", "Task one"), totalInputTokens: 900, totalOutputTokens: 80 }] });
        const stored: TaskFE = {
            taskId: "task-1",
            initialRequestText: "Task one",
            events: [],
            firstSeen: new Date("2026-01-01T00:00:00Z"),
            lastUpdated: new Date("2026-01-01T00:00:05Z"),
            needsHistoricalHydration: false,
        };

        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "task-1": stored }, monitoredTaskOrder: ["task-1"] },
        });

        await screen.findByText("Task one");
        expect(screen.getByTestId("session-list-item-tokens").textContent).toBe("90080");
    });
});

describe("ActivitiesPage session row framing", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        queryClient.clear();
    });

    test("without the details panel each row draws its sides and the first row its top", async () => {
        routeGet(apiGetSpy, { tasks: [{ id: "t1", initialRequestText: "Only turn", startTime: "2026-01-01T00:00:00Z", status: "completed", endTime: "2026-01-01T00:00:05Z" }] });
        renderActivitiesPage();

        await screen.findByText("Only turn");
        expect(sessionRowOf("Only turn")).toHaveClass("border-x", "first:border-t", "border-b");
    });
});

describe("ActivitiesPage Ask AI", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;
    const activityMonitorCard = { name: "7c21-uuid", displayName: "Activity Monitor" };
    const ownTask = { id: "t-1", initialRequestText: "Inspect me", userId: "sam_dev_user", sessionId: "sess-a", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z", status: "completed" };
    const someoneElsesTask = { ...ownTask, userId: "someone-else" };

    beforeEach(() => {
        apiGetSpy = vi.spyOn(api.webui, "get");
        navigateSpy.mockClear();
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        mockUseAgentCards.mockReturnValue({ data: [] });
        queryClient.clear();
    });

    test("hands the task and session ids, and the agent's wire name, to a new chat", async () => {
        mockUseAgentCards.mockReturnValue({ data: [activityMonitorCard] });
        routeGet(apiGetSpy, { tasks: [ownTask] });
        const user = userEvent.setup();

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:*:invoke"], hasScope: () => false } });

        const panel = await openTask(user, "Inspect me");
        const button = within(panel).getByTestId("ask-ai");
        expect(button).toBeEnabled();
        await user.click(button);

        expect(navigateSpy).toHaveBeenCalledWith("/chat", {
            state: {
                promptText: "Look up Task ID t-1 in Session ID sess-a and tell me what happened.",
                groupId: "activity-trace-t-1",
                groupName: "Activity Monitor",
                agentName: "7c21-uuid",
            },
        });
    });

    test("disables the hand-off on another user's task for a viewer with no trace or activity reach, since the Activity Monitor would answer task not found", async () => {
        mockUseAgentCards.mockReturnValue({ data: [activityMonitorCard] });
        routeGet(apiGetSpy, { tasks: [someoneElsesTask] });
        const user = userEvent.setup();

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:*:invoke"], hasScope: () => false } });

        const panel = await openTask(user, "Inspect me");
        const button = within(panel).getByTestId("ask-ai");
        expect(button).toHaveAttribute("aria-disabled", "true");
        expect(button).toHaveAccessibleName("Ask AI");
        await user.click(button);
        expect(navigateSpy).not.toHaveBeenCalledWith("/chat", expect.anything());
    });

    test("enables the hand-off on another user's task for a per-agent trace reader, since trace reach lets the Activity Monitor open it", async () => {
        mockUseAgentCards.mockReturnValue({ data: [activityMonitorCard] });
        routeGet(apiGetSpy, { tasks: [someoneElsesTask] });
        const user = userEvent.setup();

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:*:read_trace"], hasScope: () => false } });

        const panel = await openTask(user, "Inspect me");
        expect(within(panel).getByTestId("ask-ai")).not.toHaveAttribute("aria-disabled", "true");
    });

    test("keeps the hand-off disabled for a trace reader when the task's detail is unavailable, since the Activity Monitor's own read is refused too", async () => {
        mockUseAgentCards.mockReturnValue({ data: [activityMonitorCard] });
        routeGet(apiGetSpy, { tasks: [someoneElsesTask] });
        const user = userEvent.setup();

        renderWithProviders(<ActivitiesPage />, {
            featureFlags: { task_logging: true },
            chatContextValues: { sessionId: "test" },
            taskContextValues: { unavailableTaskIds: new Set(["t-1"]) },
            userContextValues: { username: "sam_dev_user", scopes: ["agent:*:read_trace"], hasScope: () => false },
        });

        const panel = await openTask(user, "Inspect me");
        const button = within(panel).getByTestId("ask-ai");
        expect(button).toHaveAttribute("aria-disabled", "true");
        await user.hover(button);
        expect(await screen.findByRole("tooltip")).toHaveTextContent("Task details are not available.");
        await user.click(button);
        expect(navigateSpy).not.toHaveBeenCalledWith("/chat", expect.anything());
    });

    test("enables the hand-off on another user's task for a tenant-wide activity reader", async () => {
        mockUseAgentCards.mockReturnValue({ data: [activityMonitorCard] });
        routeGet(apiGetSpy, { tasks: [someoneElsesTask] });
        const user = userEvent.setup();

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["activity:*:read"], hasScope: () => false } });

        const panel = await openTask(user, "Inspect me");
        expect(within(panel).getByTestId("ask-ai")).toBeEnabled();
    });

    test("withholds the button when the Activity Monitor isn't among the agent cards", async () => {
        mockUseAgentCards.mockReturnValue({ data: [{ name: "Orchestrator" }] });
        routeGet(apiGetSpy, { tasks: [ownTask] });
        const user = userEvent.setup();

        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["agent:*:invoke"], hasScope: () => false } });

        const panel = await openTask(user, "Inspect me");
        expect(within(panel).queryByTestId("ask-ai")).not.toBeInTheDocument();
    });
});

describe("ActivitiesPage task detail states", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;
    const row = { id: "t-1", initialRequestText: "Inspect me", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z", status: "completed", endTime: "2026-01-01T00:00:05Z", sessionId: "sess-a" };

    beforeEach(() => {
        queryClient.clear();
        apiGetSpy = vi.spyOn(api.webui, "get");
        routeGet(apiGetSpy, { tasks: [row] });
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("says the details are not available when the event load was refused", async () => {
        const user = userEvent.setup();
        renderWithProviders(<ActivitiesPage />, {
            featureFlags: { task_logging: true },
            chatContextValues: { sessionId: "test" },
            taskContextValues: { unavailableTaskIds: new Set(["t-1"]) },
        });

        const panel = await openTask(user, "Inspect me");
        expect(await screen.findByTestId("task-detail-state")).toHaveTextContent("Task details are not available.");
        expect(within(panel).getByTestId("download-task-log")).toHaveAttribute("aria-disabled", "true");
    });

    test("says the load failed, and retries it, when the event load hit an error", async () => {
        const user = userEvent.setup();
        const loadSpy = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { failedTaskIds: new Set(["t-1"]), loadTaskFromBackend: loadSpy },
        });

        const panel = await openTask(user, "Inspect me");
        expect(await screen.findByTestId("task-detail-state")).toHaveTextContent("Couldn't load task details.");
        await waitFor(() => expect(loadSpy).toHaveBeenCalledWith("t-1"));

        await user.click(within(panel).getByTestId("close-task-details"));
        await user.click(within(taskRowOf("Inspect me")).getByRole("button"));
        await waitFor(() => expect(loadSpy).toHaveBeenCalledTimes(2));
    });

    test("says it is loading while the first event load is in flight", async () => {
        const user = userEvent.setup();
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { loadTaskFromBackend: () => new Promise<TaskFE | null>(() => {}) },
        });

        await openTask(user, "Inspect me");
        expect(await screen.findByTestId("task-detail-state")).toHaveTextContent("Loading task details…");
    });

    test("says there are no events for a hydrated task that recorded none", async () => {
        const user = userEvent.setup();
        const stored: TaskFE = {
            taskId: "t-1",
            initialRequestText: "Inspect me",
            events: [],
            firstSeen: new Date("2026-01-01T00:00:00Z"),
            lastUpdated: new Date("2026-01-01T00:00:05Z"),
            needsHistoricalHydration: false,
        };
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "t-1": stored }, monitoredTaskOrder: ["t-1"] },
        });

        const panel = await openTask(user, "Inspect me");
        expect(within(panel).queryByTestId("task-detail-state")).not.toBeInTheDocument();
        expect(within(panel).getAllByText("No events to display.").length).toBeGreaterThan(0);
    });
});

describe("ActivitiesPage Ask AI when the agent list cannot be loaded", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;
    const row = { id: "t-1", initialRequestText: "Inspect me", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z", status: "completed", endTime: "2026-01-01T00:00:05Z", sessionId: "sess-a" };

    beforeEach(() => {
        queryClient.clear();
        apiGetSpy = vi.spyOn(api.webui, "get");
        routeGet(apiGetSpy, { tasks: [row] });
        mockUseAgentCards.mockReturnValue({ data: undefined, isError: true } as unknown as ReturnType<typeof mockUseAgentCards>);
    });

    afterEach(() => {
        mockUseAgentCards.mockReturnValue({ data: [] });
        vi.restoreAllMocks();
    });

    test("offers the button disabled with the reason instead of withholding it", async () => {
        const user = userEvent.setup();
        renderActivitiesPage({}, { userContextValues: { username: "sam_dev_user", scopes: ["activity:*:read"], hasScope: () => true } });

        const panel = await openTask(user, "Inspect me");
        const button = within(panel).getByTestId("ask-ai");
        expect(button).toHaveAttribute("aria-disabled", "true");
        await user.click(button);
        expect(navigateSpy).not.toHaveBeenCalledWith("/chat", expect.anything());
    });
});

describe("ActivitiesPage list polling", () => {
    let apiGetSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        apiGetSpy = vi.spyOn(api.webui, "get");
        vi.stubGlobal(
            "EventSource",
            class {
                addEventListener() {}
                removeEventListener() {}
                close() {}
            }
        );
    });

    afterEach(() => {
        apiGetSpy.mockRestore();
        vi.unstubAllGlobals();
        vi.useRealTimers();
        queryClient.clear();
    });

    const runningRow = { id: "run-y", initialRequestText: "Workflow run", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z" };
    const endedRow = { ...runningRow, status: "completed", endTime: "2026-01-01T00:00:09Z" };
    const otherRow = { id: "other-z", initialRequestText: "Other run", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:01Z", status: "completed", endTime: "2026-01-01T00:00:02Z" };

    const requestOnlyTree = (taskId: string, text: string): TaskFE => ({
        taskId,
        initialRequestText: text,
        events: [
            {
                event_type: "a2a_message",
                timestamp: "2026-01-01T00:00:00.000Z",
                solace_topic: `ns/a2a/v1/gateway/status/gw/${taskId}`,
                direction: "request",
                source_entity: "user",
                target_entity: taskId,
                task_id: taskId,
                payload_summary: {},
                full_payload: {},
            },
        ],
        firstSeen: new Date("2026-01-01T00:00:00Z"),
        lastUpdated: new Date("2026-01-01T00:00:00Z"),
        needsHistoricalHydration: false,
    });

    const runningThenEnded = () => {
        let polls = 0;
        const state = { ended: false };
        const row = () => (state.ended ? endedRow : { ...runningRow, lastActivityTime: new Date(Date.UTC(2026, 0, 1, 0, 0, 5 + polls++)).toISOString() });
        return { state, row };
    };

    test("keeps the selected task open when a poll pushes it past the list's page", async () => {
        let polled = false;
        apiGetSpy.mockImplementation((endpoint: unknown) => Promise.resolve(String(endpoint).includes("/api/v1/tasks") ? { data: polled ? [otherRow] : [endedRow] } : { artifacts: [], nextPage: null }));
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "run-y": requestOnlyTree("run-y", "Workflow run") }, monitoredTaskOrder: ["run-y"] },
        });
        await openTask(userEvent.setup({ advanceTimers: vi.advanceTimersByTime }), "Workflow run");

        polled = true;
        await act(() => vi.advanceTimersByTimeAsync(IDLE_TASKS_POLL_MS));
        await screen.findByText("Other run");

        expect(screen.getByTestId("task-detail-panel")).toBeInTheDocument();
    });

    test("keeps streaming a running selected task when a poll pushes it past the list's page", async () => {
        const streams: { url: string; closed: boolean }[] = [];
        vi.stubGlobal(
            "EventSource",
            class {
                private readonly stream: { url: string; closed: boolean };
                constructor(url: string) {
                    this.stream = { url, closed: false };
                    streams.push(this.stream);
                }
                addEventListener() {}
                removeEventListener() {}
                close() {
                    this.stream.closed = true;
                }
            }
        );
        let polled = false;
        apiGetSpy.mockImplementation((endpoint: unknown) => Promise.resolve(String(endpoint).includes("/api/v1/tasks") ? { data: polled ? [otherRow] : [runningRow] } : { artifacts: [], nextPage: null }));
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "run-y": requestOnlyTree("run-y", "Workflow run") }, monitoredTaskOrder: ["run-y"] },
        });
        await openTask(userEvent.setup({ advanceTimers: vi.advanceTimersByTime }), "Workflow run");
        await waitFor(() => expect(streams.some(s => s.url.includes("/api/v1/sse/subscribe/run-y"))).toBe(true));

        polled = true;
        await act(() => vi.advanceTimersByTimeAsync(RUNNING_TASKS_POLL_MS));
        await screen.findByText("Other run");

        expect(screen.getByTestId("task-detail-panel")).toBeInTheDocument();
        expect(streams.filter(s => s.url.includes("/api/v1/sse/subscribe/run-y")).every(s => !s.closed)).toBe(true);
    });

    test("closes the selected task when a search leaves it out", async () => {
        apiGetSpy.mockImplementation((endpoint: unknown) => {
            const url = String(endpoint);
            if (!url.includes("/api/v1/tasks")) return Promise.resolve({ artifacts: [], nextPage: null });
            return Promise.resolve({ data: url.includes("search=") ? [otherRow] : [endedRow] });
        });
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "run-y": requestOnlyTree("run-y", "Workflow run") }, monitoredTaskOrder: ["run-y"] },
        });
        const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
        await openTask(user, "Workflow run");

        fireEvent.change(screen.getByTestId("task-search-input"), { target: { value: "other-z" } });
        await act(() => vi.advanceTimersByTimeAsync(1000));

        await waitFor(() => expect(screen.queryByTestId("task-detail-panel")).not.toBeInTheDocument());
    });

    test("a poll in flight does not disable the Refresh button", async () => {
        let taskCalls = 0;
        apiGetSpy.mockImplementation((endpoint: unknown) => {
            if (!String(endpoint).includes("/api/v1/tasks")) return Promise.resolve({ artifacts: [], nextPage: null });
            taskCalls++;
            return taskCalls === 1 ? Promise.resolve({ data: [endedRow] }) : new Promise(() => {});
        });

        renderActivitiesPage();
        await screen.findByText("Workflow run");
        await act(() => vi.advanceTimersByTimeAsync(IDLE_TASKS_POLL_MS));
        await waitFor(() => expect(taskCalls).toBe(2));

        expect(screen.getByTestId("refresh-tasks")).not.toBeDisabled();
    });

    test("a Refresh click shows its own fetch until it settles", async () => {
        let release: (value: unknown) => void = () => {};
        let taskCalls = 0;
        apiGetSpy.mockImplementation((endpoint: unknown) => {
            if (!String(endpoint).includes("/api/v1/tasks")) return Promise.resolve({ artifacts: [], nextPage: null });
            taskCalls++;
            return taskCalls === 1 ? Promise.resolve({ data: [endedRow] }) : new Promise(resolve => (release = resolve));
        });

        renderActivitiesPage();
        await screen.findByText("Workflow run");
        const button = screen.getByTestId("refresh-tasks");
        await userEvent.setup({ advanceTimers: vi.advanceTimersByTime }).click(button);

        await waitFor(() => expect(button).toBeDisabled());
        expect(button.querySelector("svg")).toHaveClass("animate-spin");
        await act(async () => {
            release({ data: [endedRow] });
        });
        await waitFor(() => expect(button).not.toBeDisabled());
        expect(button.querySelector("svg")).not.toHaveClass("animate-spin");
    });

    test("re-fetches the history of a task that ended while another one was selected", async () => {
        const run = runningThenEnded();
        apiGetSpy.mockImplementation((endpoint: unknown) => Promise.resolve(String(endpoint).includes("/api/v1/tasks") ? { data: [run.row(), otherRow] } : { artifacts: [], nextPage: null }));
        const loadSpy = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "run-y": requestOnlyTree("run-y", "Workflow run"), "other-z": requestOnlyTree("other-z", "Other run") }, monitoredTaskOrder: ["run-y", "other-z"], loadTaskFromBackend: loadSpy },
        });
        const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
        await openTask(user, "Workflow run");
        await act(() => vi.advanceTimersByTimeAsync(RUNNING_TASKS_POLL_MS * (REHYDRATE_MAX_ATTEMPTS + 2)));
        expect(loadSpy.mock.calls.filter(([id]) => id === "run-y")).toHaveLength(REHYDRATE_MAX_ATTEMPTS);

        await openTask(user, "Other run");
        run.state.ended = true;
        await act(() => vi.advanceTimersByTimeAsync(RUNNING_TASKS_POLL_MS));
        loadSpy.mockClear();
        await user.click(within(taskRowOf("Workflow run")).getByRole("button"));
        await act(() => vi.advanceTimersByTimeAsync(REHYDRATE_DELAY_MS * 2));

        expect(loadSpy).toHaveBeenCalledWith("run-y");
    });

    test("shows a task started elsewhere without a Refresh", async () => {
        let listed: unknown[] = [];
        apiGetSpy.mockImplementation((endpoint: unknown) => Promise.resolve(String(endpoint).includes("/api/v1/tasks") ? { data: listed } : { artifacts: [], nextPage: null }));

        renderActivitiesPage();
        await waitFor(() => expect(tasksRequestUrls(apiGetSpy).length).toBe(1));

        listed = [runningRow];
        await act(() => vi.advanceTimersByTimeAsync(IDLE_TASKS_POLL_MS));

        expect(await screen.findByText("Workflow run")).toBeInTheDocument();
    });

    test("re-fetches a selected task's history once the polled list says it ended", async () => {
        const run = runningThenEnded();
        apiGetSpy.mockImplementation((endpoint: unknown) => Promise.resolve(String(endpoint).includes("/api/v1/tasks") ? { data: [run.row()] } : { artifacts: [], nextPage: null }));
        const loadSpy = vi.fn<(taskId: string) => Promise<TaskFE | null>>().mockResolvedValue(null);
        renderWithProviders(<ActivitiesPage />, {
            chatContextValues: { sessionId: "test" },
            taskContextValues: { monitoredTasks: { "run-y": requestOnlyTree("run-y", "Workflow run") }, monitoredTaskOrder: ["run-y"], loadTaskFromBackend: loadSpy },
        });
        await openTask(userEvent.setup({ advanceTimers: vi.advanceTimersByTime }), "Workflow run");

        await act(() => vi.advanceTimersByTimeAsync(RUNNING_TASKS_POLL_MS * (REHYDRATE_MAX_ATTEMPTS + 2)));
        expect(loadSpy).toHaveBeenCalledTimes(REHYDRATE_MAX_ATTEMPTS);
        loadSpy.mockClear();

        run.state.ended = true;
        await act(() => vi.advanceTimersByTimeAsync(RUNNING_TASKS_POLL_MS + REHYDRATE_DELAY_MS * 2));

        expect(loadSpy).toHaveBeenCalledWith("run-y");
    });
});
