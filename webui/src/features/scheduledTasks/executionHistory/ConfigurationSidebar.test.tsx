// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { ScheduledTask, TaskExecution } from "@/types/scheduled-tasks";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ConfigurationSidebar } from "./ConfigurationSidebar";

vi.mock("@/features/scheduledTasks/utils", () => ({ formatSchedule: vi.fn(() => "Daily at 09:00") }));

function executionSnapshot(snapshotName: string): TaskExecution {
    return {
        id: "exec-1",
        scheduledTaskId: "task-1",
        status: "completed",
        scheduledFor: new Date(0).toISOString(),
        retryCount: 0,
        taskSnapshot: {
            name: snapshotName,
            scheduleType: "cron",
            scheduleExpression: "0 9 * * *",
            timezone: "UTC",
            targetAgentName: "report-agent",
            targetType: "agent",
            taskMessage: [{ type: "text", text: "snapshot instructions" }],
        },
    } as TaskExecution;
}

function task(overrides: Partial<ScheduledTask> = {}): ScheduledTask {
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

const handlers = {
    onEdit: vi.fn(),
    onRunNow: vi.fn(),
    onToggleEnabled: vi.fn(),
    onDelete: vi.fn(),
};

describe("ConfigurationSidebar status visibility", () => {
    test("shows an Active status badge for an enabled task without opening the menu", () => {
        renderWithProviders(<ConfigurationSidebar task={task()} execution={null} isReadOnly={false} isRunNowPending={false} {...handlers} />);
        expect(screen.getByText("Active")).toBeInTheDocument();
    });

    test("shows a Paused status badge for a disabled task without opening the menu", () => {
        renderWithProviders(<ConfigurationSidebar task={task({ enabled: false, status: "paused" })} execution={null} isReadOnly={false} isRunNowPending={false} {...handlers} />);
        expect(screen.getByText("Paused")).toBeInTheDocument();
    });

    test("in read-only execution view shows the snapshot name and hides the live Status", () => {
        renderWithProviders(<ConfigurationSidebar task={task({ name: "Live Name", enabled: false, status: "paused" })} execution={executionSnapshot("Snapshot Name")} isReadOnly={true} isRunNowPending={false} {...handlers} />);
        expect(screen.getByText("Snapshot Name")).toBeInTheDocument();
        expect(screen.queryByText("Live Name")).not.toBeInTheDocument();
        expect(screen.queryByText("Status")).not.toBeInTheDocument();
        expect(screen.queryByText("Paused")).not.toBeInTheDocument();
    });
});
