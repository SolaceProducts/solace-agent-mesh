// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { ScheduledTask } from "@/types/scheduled-tasks";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { TaskCards } from "./TaskCards";

vi.mock("@/features/scheduledTasks/utils", () => ({ formatSchedule: vi.fn(() => "Daily at 09:00") }));
vi.mock("@/api/agents", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/agents")>()),
    useAgents: () => ({ data: [{ id: "db-1", name: "Sales Helper", instanceName: "agent_1111" }] }),
}));
vi.mock("@/api/remote-agents", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/remote-agents")>()),
    useRemoteAgents: () => ({ data: [] }),
}));
vi.mock("@/api/agent-cards", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/agent-cards")>()),
    useAgentCards: () => ({ data: [{ name: "workflow_2222", displayName: "Nightly Workflow", isWorkflow: true }] }),
}));

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
    onManualCreate: vi.fn(),
    onAIAssisted: vi.fn(),
    onEdit: vi.fn(),
    onDelete: vi.fn(),
    onToggleEnabled: vi.fn(),
    onViewExecutions: vi.fn(),
    onRunNow: vi.fn(),
};

describe("TaskCards", () => {
    test("library-empty: shows the no-tasks empty state", () => {
        renderWithProviders(<TaskCards tasks={[]} {...handlers} />);
        expect(screen.getByText("No Scheduled Tasks Found")).toBeInTheDocument();
    });

    test("with tasks: renders the task cards grid", () => {
        renderWithProviders(<TaskCards tasks={[task()]} {...handlers} />);
        expect(screen.getByText("Daily Report")).toBeInTheDocument();
    });

    test("filter-empty: typing a non-matching filter shows the filter empty state", async () => {
        renderWithProviders(<TaskCards tasks={[task()]} {...handlers} />);
        const search = screen.getByPlaceholderText(/filter/i);
        await userEvent.type(search, "zzzznomatch");
        expect(await screen.findByText("No Tasks Match Your Filter")).toBeInTheDocument();
    });

    test("filtering by an agent's display name shows every task whose stored target resolves to it", async () => {
        const tasks = [
            task({ id: "t1", name: "Alpha", description: "first", targetAgentName: "agent_1111" }),
            task({ id: "t2", name: "Beta", description: "second", targetAgentName: "agent_1111" }),
            task({ id: "t3", name: "Gamma", description: "third", targetAgentName: "report-agent" }),
        ];
        renderWithProviders(<TaskCards tasks={tasks} {...handlers} />);
        await userEvent.type(screen.getByPlaceholderText(/filter/i), "sales helper");
        expect(screen.getByText("Alpha")).toBeInTheDocument();
        expect(screen.getByText("Beta")).toBeInTheDocument();
        expect(screen.queryByText("Gamma")).not.toBeInTheDocument();
    });

    test("filtering by a workflow's display name shows tasks targeting that workflow", async () => {
        const tasks = [task({ id: "t1", name: "Alpha", targetAgentName: "workflow_2222", targetType: "workflow" }), task({ id: "t2", name: "Beta", targetAgentName: "report-agent" })];
        renderWithProviders(<TaskCards tasks={tasks} {...handlers} />);
        await userEvent.type(screen.getByPlaceholderText(/filter/i), "nightly");
        expect(screen.getByText("Alpha")).toBeInTheDocument();
        expect(screen.queryByText("Beta")).not.toBeInTheDocument();
    });

    test("filtering by the stored internal agent name still matches", async () => {
        const tasks = [task({ id: "t1", name: "Alpha", targetAgentName: "agent_1111" }), task({ id: "t2", name: "Beta", targetAgentName: "report-agent" })];
        renderWithProviders(<TaskCards tasks={tasks} {...handlers} />);
        await userEvent.type(screen.getByPlaceholderText(/filter/i), "agent_1111");
        expect(screen.getByText("Alpha")).toBeInTheDocument();
        expect(screen.queryByText("Beta")).not.toBeInTheDocument();
    });
});
