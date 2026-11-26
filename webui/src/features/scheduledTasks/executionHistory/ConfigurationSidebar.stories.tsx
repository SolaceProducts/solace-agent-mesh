// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, within } from "storybook/test";

import type { ScheduledTask, TaskExecution } from "@/types/scheduled-tasks";

import { ConfigurationSidebar } from "./ConfigurationSidebar";

function task(overrides: Partial<ScheduledTask> = {}): ScheduledTask {
    return {
        id: "task-1",
        name: "Daily Report",
        description: "Generate a daily summary report and email it to the team.",
        namespace: "default",
        createdBy: "user-1",
        scheduleType: "cron",
        scheduleExpression: "0 9 * * *",
        timezone: "America/New_York",
        targetAgentName: "report-agent",
        targetType: "agent",
        taskMessage: [{ type: "text", text: "Generate the daily report." }],
        enabled: true,
        status: "active",
        maxRetries: 3,
        retryDelaySeconds: 60,
        timeoutSeconds: 300,
        consecutiveFailureCount: 0,
        runCount: 42,
        createdAt: new Date(0).toISOString(),
        updatedAt: new Date(0).toISOString(),
        nextRunTime: new Date(0).toISOString(),
        ...overrides,
    } as ScheduledTask;
}

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
            timezone: "America/New_York",
            targetAgentName: "report-agent",
            targetType: "agent",
            taskMessage: [{ type: "text", text: "Generate the daily report." }],
        },
    } as TaskExecution;
}

const meta = {
    title: "ScheduledTasks/ConfigurationSidebar",
    component: ConfigurationSidebar,
    parameters: {
        docs: {
            description: {
                component:
                    "Configuration rail on the scheduled-task detail page. The status badge is the first field in the Configuration body and surfaces whether the task is Active/Paused without opening the actions menu. It is hidden in the read-only execution-snapshot view, where the fields reflect the config that produced the selected run.",
            },
        },
    },
    args: {
        onEdit: fn(),
        onRunNow: fn(),
        onToggleEnabled: fn(),
        onDelete: fn(),
        isRunNowPending: false,
        execution: null,
        isReadOnly: false,
    },
    decorators: [
        Story => (
            <div className="h-screen w-[320px] border-r">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof ConfigurationSidebar>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Active: Story = {
    args: {
        task: task(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("Active")).toBeInTheDocument();
    },
};

export const Paused: Story = {
    args: {
        task: task({ enabled: false, status: "paused" }),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("Paused")).toBeInTheDocument();
    },
};

export const ReadOnlyExecutionSnapshot: Story = {
    args: {
        task: task({ name: "Daily Report (current)", enabled: false, status: "paused" }),
        execution: executionSnapshot("Daily Report (as it ran)"),
        isReadOnly: true,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("Daily Report (as it ran)")).toBeInTheDocument();
        expect(canvas.queryByText("Daily Report (current)")).not.toBeInTheDocument();
        expect(canvas.queryByText("Status")).not.toBeInTheDocument();
    },
};
