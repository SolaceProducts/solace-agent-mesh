// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { queryClient } from "@/providers/query/QueryClient";

import type { ScheduledTask, TaskExecution } from "@/types/scheduled-tasks";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { TaskExecutionHistoryPage } from "./TaskExecutionHistoryPage";

const TASK = {
    id: "task-1",
    name: "Daily Report",
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
    maxRetries: 0,
    retryDelaySeconds: 60,
    timeoutSeconds: 300,
    consecutiveFailureCount: 0,
    runCount: 1,
    createdAt: new Date(0).toISOString(),
    updatedAt: new Date(0).toISOString(),
    nextRunTime: new Date(0).toISOString(),
} as ScheduledTask;

const FINISHED = { id: "exec-1", scheduledTaskId: "task-1", status: "completed", scheduledFor: new Date(0).toISOString() } as TaskExecution;

function routeGets() {
    const spy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "get");
    spy.mockImplementation((endpoint: unknown) => {
        const url = String(endpoint);
        if (url.includes("/executions")) return Promise.resolve({ data: [FINISHED], meta: { pagination: { pageNumber: 1, count: 1, pageSize: 20, nextPage: null, totalPages: 1 } } });
        if (url.includes("/api/v1/scheduledTasks/task-1")) return Promise.resolve(TASK);
        return Promise.resolve({ data: [] });
    });
}

describe("TaskExecutionHistoryPage — deleting an execution", () => {
    afterEach(() => {
        vi.restoreAllMocks();
        queryClient.clear();
    });

    test("shows the server's reason when the delete is refused", async () => {
        const addNotification = vi.fn();
        routeGets();
        const deleteSpy: ReturnType<typeof vi.spyOn> = vi.spyOn(api.webui, "delete");
        deleteSpy.mockRejectedValue(new Error("an execution that is still running or finishing cannot be deleted"));

        renderWithProviders(<TaskExecutionHistoryPage task={TASK} onBack={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} />, { chatContextValues: { addNotification } });

        await screen.findByText("Execution History");
        let trigger: Element | null = null;
        await waitFor(() => {
            trigger = screen.getAllByRole("row")[1]?.querySelector("button[aria-haspopup='menu']") ?? null;
            expect(trigger).not.toBeNull();
        });
        fireEvent.pointerDown(trigger as unknown as Element);
        fireEvent.click(await screen.findByRole("menuitem", { name: /delete/i }));
        fireEvent.click(await screen.findByRole("button", { name: "Delete" }));

        await waitFor(() => {
            expect(addNotification).toHaveBeenCalledWith(expect.stringContaining("Delete failed: an execution that is still running or finishing cannot be deleted"), "warning");
        });
        expect(deleteSpy).toHaveBeenCalledWith(expect.stringContaining("exec-1"));
    });
});
