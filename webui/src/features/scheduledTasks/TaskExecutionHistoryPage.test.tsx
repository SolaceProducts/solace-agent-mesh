// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { queryClient } from "@/providers/query/QueryClient";

import type { ExecutionStatus, ScheduledTask, TaskExecution } from "@/types/scheduled-tasks";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { TaskExecutionHistoryPage } from "./TaskExecutionHistoryPage";

function scheduledTask(overrides: Partial<ScheduledTask> = {}): ScheduledTask {
    return {
        id: "task-1",
        name: "Daily Report",
        description: "Generate a daily summary report",
        namespace: "default",
        createdBy: "user-1",
        scheduleType: "cron",
        scheduleExpression: "0 9 * * *",
        timezone: "UTC",
        targetAgentName: "report-agent",
        targetType: "agent",
        taskMessage: [{ type: "text", text: "Generate report" }],
        enabled: true,
        status: "active",
        maxRetries: 3,
        retryDelaySeconds: 60,
        timeoutSeconds: 300,
        consecutiveFailureCount: 0,
        runCount: 10,
        createdAt: new Date(0).toISOString(),
        updatedAt: new Date(0).toISOString(),
        nextRunTime: new Date(0).toISOString(),
        ...overrides,
    } as ScheduledTask;
}

const EMPTY_EXECUTIONS = { data: [], meta: { pagination: { pageNumber: 1, count: 0, pageSize: 20, nextPage: null, totalPages: 1 } } };

function routeGet(liveTask: ScheduledTask) {
    const spy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "get");
    spy.mockImplementation((endpoint: unknown) => {
        const url = String(endpoint);
        if (url.includes("/executions")) return Promise.resolve(EMPTY_EXECUTIONS);
        if (url.includes("/api/v1/scheduledTasks/task-1")) return Promise.resolve(liveTask);
        return Promise.resolve({ data: [] });
    });
    return spy;
}

const noopHandlers = { onBack: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn() };

describe("TaskExecutionHistoryPage — live task state", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        queryClient.clear();
    });

    test("renders enabled-state from the live query, not the stale prop", async () => {
        routeGet(scheduledTask({ enabled: false, status: "paused" }));

        renderWithProviders(<TaskExecutionHistoryPage task={scheduledTask({ enabled: true, status: "active" })} {...noopHandlers} />);

        expect(await screen.findByText("Paused")).toBeInTheDocument();
        expect(screen.queryByText("Active")).not.toBeInTheDocument();
    });

    test("resuming a live-paused task hits /enable and round-trips the badge, following the live task not the prop", async () => {
        let enabled = false;
        const getSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "get");
        getSpy.mockImplementation((endpoint: unknown) => {
            const url = String(endpoint);
            if (url.includes("/executions")) return Promise.resolve(EMPTY_EXECUTIONS);
            if (url.includes("/api/v1/scheduledTasks/task-1")) return Promise.resolve(scheduledTask({ enabled, status: enabled ? "active" : "paused" }));
            return Promise.resolve({ data: [] });
        });
        const postSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "post");
        postSpy.mockImplementation((endpoint: unknown) => {
            if (String(endpoint).includes("/enable")) enabled = true;
            return Promise.resolve({});
        });

        renderWithProviders(<TaskExecutionHistoryPage task={scheduledTask({ enabled: true, status: "active" })} {...noopHandlers} />);
        await screen.findByText("Paused");

        fireEvent.pointerDown(screen.getByRole("button", { name: /actions/i }));
        expect(await screen.findByText("Resume Task")).toBeInTheDocument();
        expect(screen.queryByText("Pause Task")).not.toBeInTheDocument();

        fireEvent.click(screen.getByText("Resume Task"));

        await waitFor(() => expect(postSpy).toHaveBeenCalledWith(expect.stringContaining("/enable")));
        expect(await screen.findByText("Active")).toBeInTheDocument();
    });
});

describe("TaskExecutionHistoryPage — pause/resume error handling", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        queryClient.clear();
    });

    test("surfaces a notification when the pause request fails instead of failing silently", async () => {
        const addNotification = vi.fn();
        routeGet(scheduledTask({ enabled: true, status: "active" }));
        const postSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "post");
        postSpy.mockImplementation((endpoint: unknown) => (String(endpoint).includes("/disable") ? Promise.reject(new Error("boom")) : Promise.resolve({})));

        renderWithProviders(<TaskExecutionHistoryPage task={scheduledTask({ enabled: true, status: "active" })} {...noopHandlers} />, { chatContextValues: { addNotification } });

        await screen.findByText("Active");

        fireEvent.pointerDown(screen.getByRole("button", { name: /actions/i }));
        fireEvent.click(await screen.findByText("Pause Task"));

        await waitFor(() => {
            expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Pause failed"), "warning");
        });
    });

    test("surfaces a notification when run-now fails instead of failing silently", async () => {
        const addNotification = vi.fn();
        routeGet(scheduledTask({ enabled: true, status: "active" }));
        const postSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "post");
        postSpy.mockImplementation((endpoint: unknown) => (String(endpoint).includes("/run") ? Promise.reject(new Error("boom")) : Promise.resolve({})));

        renderWithProviders(<TaskExecutionHistoryPage task={scheduledTask({ enabled: true, status: "active" })} {...noopHandlers} />, { chatContextValues: { addNotification } });
        await screen.findByText("Active");

        fireEvent.pointerDown(screen.getByRole("button", { name: /actions/i }));
        fireEvent.click(await screen.findByText("Run Now"));

        await waitFor(() => {
            expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Run Now failed"), "warning");
        });
    });
});

describe("TaskExecutionHistoryPage — configuration actions", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        queryClient.clear();
    });

    test("edit, run-now, and delete delegate to their handlers", async () => {
        const onEdit = vi.fn();
        const onDelete = vi.fn();
        routeGet(scheduledTask({ enabled: true, status: "active" }));
        const postSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "post");
        postSpy.mockResolvedValue({});

        renderWithProviders(<TaskExecutionHistoryPage task={scheduledTask({ enabled: true, status: "active" })} onBack={vi.fn()} onEdit={onEdit} onDelete={onDelete} />);
        await screen.findByText("Active");

        fireEvent.click(screen.getByRole("button", { name: "Edit" }));
        expect(onEdit).toHaveBeenCalled();

        fireEvent.pointerDown(screen.getByRole("button", { name: /actions/i }));
        fireEvent.click(await screen.findByText("Run Now"));
        await waitFor(() => expect(postSpy).toHaveBeenCalledWith(expect.stringContaining("/run")));

        fireEvent.pointerDown(screen.getByRole("button", { name: /actions/i }));
        fireEvent.click(await screen.findByText("Delete"));
        expect(onDelete).toHaveBeenCalled();
    });
});

function finishedExecution(status: ExecutionStatus, sessionId?: string): TaskExecution {
    return { id: "exec-1", scheduledTaskId: "task-1", status, sessionId, scheduledFor: new Date(0).toISOString(), startedAt: new Date(0).toISOString(), completedAt: new Date(1000).toISOString(), retryCount: 0 };
}

function routeGetWithExecution(execution: TaskExecution) {
    const spy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "get");
    spy.mockImplementation((endpoint: unknown) => {
        const url = String(endpoint);
        if (url.includes("/api/v1/scheduledTasks/executions/exec-1")) return Promise.resolve(execution);
        if (url.includes("/executions")) return Promise.resolve({ data: [execution], meta: { pagination: { pageNumber: 1, count: 1, pageSize: 20, nextPage: null, totalPages: 1 } } });
        if (url.includes("/api/v1/scheduledTasks/task-1")) return Promise.resolve(scheduledTask());
        return Promise.resolve({ data: [] });
    });
}

describe("TaskExecutionHistoryPage — view chat output", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        queryClient.clear();
    });

    test.each([
        { status: "quiet", sessionId: undefined, offered: false },
        { status: "quiet", sessionId: "session-1", offered: true },
        { status: "completed", sessionId: "session-1", offered: true },
        { status: "completed", sessionId: undefined, offered: true },
    ] as const)("latest $status run with session $sessionId offers the chat output: $offered", async ({ status, sessionId, offered }) => {
        routeGetWithExecution(finishedExecution(status, sessionId));

        renderWithProviders(<TaskExecutionHistoryPage task={scheduledTask()} {...noopHandlers} />);
        await screen.findByText("Latest Execution");

        await waitFor(() => expect(screen.queryByRole("button", { name: /View Chat Output/ }) !== null).toBe(offered));
    });

    test.each([
        { status: "quiet", sessionId: undefined, offered: false },
        { status: "quiet", sessionId: "session-1", offered: true },
        { status: "completed", sessionId: "session-1", offered: true },
        { status: "completed", sessionId: undefined, offered: true },
    ] as const)("selected $status run with session $sessionId offers the chat output: $offered", async ({ status, sessionId, offered }) => {
        routeGetWithExecution(finishedExecution(status, sessionId));

        renderWithProviders(<TaskExecutionHistoryPage task={scheduledTask()} {...noopHandlers} />, { initialPath: "/?execution=exec-1" });
        await screen.findByRole("button", { name: "Daily Report" });

        expect(screen.queryByRole("button", { name: /View Chat Output/ }) !== null).toBe(offered);
    });
});
