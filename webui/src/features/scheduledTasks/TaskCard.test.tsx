// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { ScheduledTask } from "@/types/scheduled-tasks";
import { fillScheduledTaskDerived } from "@/types/scheduled-tasks";

import { TaskCard } from "@/features/scheduledTasks/TaskCard";

vi.mock("@/features/scheduledTasks/utils", () => ({
    formatSchedule: vi.fn(() => "Daily at 09:00"),
}));

function createMockTask(overrides: Partial<ScheduledTask> = {}): ScheduledTask {
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
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
        nextRunTime: new Date(Date.now() + 3600000).toISOString(),
        ...overrides,
    };
}

const defaultProps = {
    onTaskClick: vi.fn(),
    onEdit: vi.fn(),
    onDelete: vi.fn(),
    onToggleEnabled: vi.fn(),
    onViewExecutions: vi.fn(),
};

function renderTaskCard(taskOverrides: Partial<ScheduledTask> = {}, props: Partial<React.ComponentProps<typeof TaskCard>> = {}) {
    const task = createMockTask(taskOverrides);
    return render(<TaskCard task={task} {...defaultProps} {...props} />);
}

describe("TaskCard", () => {
    beforeEach(() => {
        vi.clearAllMocks();
    });

    it("renders task name and description", () => {
        renderTaskCard();
        expect(screen.getByText("Daily Report")).toBeInTheDocument();
        expect(screen.getByText("Generate a daily summary report")).toBeInTheDocument();
    });

    it("labels a quiet last run 'Nothing to report'", () => {
        renderTaskCard({
            lastCompletedExecution: { id: "e1", status: "quiet", scheduledFor: new Date().toISOString(), completedAt: new Date().toISOString() },
        });
        expect(screen.getByText(/Nothing to report/)).toBeInTheDocument();
    });

    it("shows correct status indicator - active (green dot)", () => {
        renderTaskCard({ status: "active" });
        const label = screen.getByText("Active");
        expect(label).toBeInTheDocument();
        expect(label.className).toContain("success");
    });

    it("shows correct status indicator - paused (neutral grey badge)", () => {
        renderTaskCard({ status: "paused" });
        const labels = screen.getAllByText("Paused");
        const badge = labels.find(el => el.className.includes("secondary-w20"));
        expect(badge).toBeInTheDocument();
    });

    it("shows correct status indicator - error (red dot)", () => {
        renderTaskCard({ status: "error" });
        const label = screen.getByText("Error");
        expect(label).toBeInTheDocument();
        expect(label.className).toContain("error");
    });

    it("a paused task with past failures reads as Paused, not Error or an upcoming run", () => {
        const derived = fillScheduledTaskDerived(
            createMockTask({
                status: undefined as unknown as ScheduledTask["status"],
                enabled: false,
                consecutiveFailureCount: 3,
                nextRunTime: new Date(Date.now() + 3600000).toISOString(),
            })
        );
        expect(derived.status).toBe("paused");

        render(<TaskCard task={derived} {...defaultProps} />);
        expect(screen.queryByText("Error")).not.toBeInTheDocument();
        const nextRunRow = screen.getByText("Next run:").parentElement;
        expect(nextRunRow?.textContent).toContain("Paused");
        expect(nextRunRow?.textContent).not.toMatch(/In \d/);
    });

    it("shows schedule description from formatSchedule", () => {
        renderTaskCard();
        expect(screen.getByText("Daily at 09:00")).toBeInTheDocument();
    });

    it("shows 'Config' badge when source is 'CONFIG'", () => {
        renderTaskCard({ source: "config" });
        expect(screen.getByText("Config")).toBeInTheDocument();
    });

    it("does not show 'Config' badge when source is not 'CONFIG'", () => {
        renderTaskCard({ source: "ui" });
        expect(screen.queryByText("Config")).not.toBeInTheDocument();
    });

    it("calls onTaskClick when card is clicked", () => {
        const onTaskClick = vi.fn();
        renderTaskCard({}, { onTaskClick });
        fireEvent.click(screen.getByText("Daily Report"));
        expect(onTaskClick).toHaveBeenCalled();
    });

    it("dropdown menu shows actions when trigger is clicked", () => {
        renderTaskCard();
        const menuTrigger = screen.getByRole("button", { name: /actions/i });
        fireEvent.pointerDown(menuTrigger);

        expect(screen.getByText("Edit Task")).toBeInTheDocument();
        expect(screen.getByText("View Execution History")).toBeInTheDocument();
        expect(screen.getByText("Pause Task")).toBeInTheDocument();
        expect(screen.getByText("Delete Task")).toBeInTheDocument();
    });

    it("shows 'Resume Task' when task is disabled", () => {
        renderTaskCard({ enabled: false });
        const menuTrigger = screen.getByRole("button", { name: /actions/i });
        fireEvent.pointerDown(menuTrigger);

        expect(screen.getByText("Resume Task")).toBeInTheDocument();
    });

    it("calls onToggleEnabled with correct task when Enable/Disable is clicked", () => {
        const onToggleEnabled = vi.fn();
        const task = createMockTask({ enabled: true });
        render(<TaskCard task={task} {...defaultProps} onToggleEnabled={onToggleEnabled} />);

        const menuTrigger = screen.getByRole("button", { name: /actions/i });
        fireEvent.pointerDown(menuTrigger);
        fireEvent.click(screen.getByText("Pause Task"));

        expect(onToggleEnabled).toHaveBeenCalledWith(task);
    });

    it("calls onDelete with task id when delete is clicked", () => {
        const onDelete = vi.fn();
        const task = createMockTask({ id: "task-42" });
        render(<TaskCard task={task} {...defaultProps} onDelete={onDelete} />);

        const menuTrigger = screen.getByRole("button", { name: /actions/i });
        fireEvent.pointerDown(menuTrigger);
        fireEvent.click(screen.getByText("Delete Task"));

        expect(onDelete).toHaveBeenCalledWith("task-42");
    });

    it("calls onEdit with task when edit is clicked", () => {
        const onEdit = vi.fn();
        const task = createMockTask();
        render(<TaskCard task={task} {...defaultProps} onEdit={onEdit} />);

        const menuTrigger = screen.getByRole("button", { name: /actions/i });
        fireEvent.pointerDown(menuTrigger);
        fireEvent.click(screen.getByText("Edit Task"));

        expect(onEdit).toHaveBeenCalledWith(task);
    });

    it("calls onViewExecutions when View Execution History is clicked", () => {
        const onViewExecutions = vi.fn();
        const task = createMockTask();
        render(<TaskCard task={task} {...defaultProps} onViewExecutions={onViewExecutions} />);

        const menuTrigger = screen.getByRole("button", { name: /actions/i });
        fireEvent.pointerDown(menuTrigger);
        fireEvent.click(screen.getByText("View Execution History"));

        expect(onViewExecutions).toHaveBeenCalledWith(task);
    });

    it("shows selected state via aria-selected attribute", () => {
        renderTaskCard({}, { isSelected: true });
        const selectedCard = document.querySelector("[aria-selected='true']");
        expect(selectedCard).toBeInTheDocument();
    });

    it("does not show selected state when isSelected is false", () => {
        renderTaskCard({}, { isSelected: false });
        const selectedCard = document.querySelector("[aria-selected='true']");
        expect(selectedCard).not.toBeInTheDocument();
    });

    describe("Run Now menu item", () => {
        it("shows 'Run Now' when onRunNow is provided for a cron task", () => {
            renderTaskCard({ scheduleType: "cron" }, { onRunNow: vi.fn() });
            const menuTrigger = screen.getByRole("button", { name: /actions/i });
            fireEvent.pointerDown(menuTrigger);
            expect(screen.getByText("Run Now")).toBeInTheDocument();
        });

        it("shows 'Run Now' for interval tasks", () => {
            renderTaskCard({ scheduleType: "interval", scheduleExpression: "30m" }, { onRunNow: vi.fn() });
            const menuTrigger = screen.getByRole("button", { name: /actions/i });
            fireEvent.pointerDown(menuTrigger);
            expect(screen.getByText("Run Now")).toBeInTheDocument();
        });

        it("hides 'Run Now' for ONE_TIME tasks (they are terminal)", () => {
            renderTaskCard({ scheduleType: "one_time", scheduleExpression: "2030-01-01T00:00:00Z" }, { onRunNow: vi.fn() });
            const menuTrigger = screen.getByRole("button", { name: /actions/i });
            fireEvent.pointerDown(menuTrigger);
            expect(screen.queryByText("Run Now")).not.toBeInTheDocument();
        });

        it("hides 'Run Now' for config-sourced tasks (read-only)", () => {
            renderTaskCard({ source: "config" }, { onRunNow: vi.fn() });
            const menuTrigger = screen.getByRole("button", { name: /actions/i });
            fireEvent.pointerDown(menuTrigger);
            expect(screen.queryByText("Run Now")).not.toBeInTheDocument();
        });

        it("hides 'Run Now' entirely when onRunNow is not provided", () => {
            renderTaskCard();
            const menuTrigger = screen.getByRole("button", { name: /actions/i });
            fireEvent.pointerDown(menuTrigger);
            expect(screen.queryByText("Run Now")).not.toBeInTheDocument();
        });

        it("calls onRunNow with the task when the menu item is selected", () => {
            const onRunNow = vi.fn();
            const task = createMockTask();
            render(<TaskCard task={task} {...defaultProps} onRunNow={onRunNow} />);

            const menuTrigger = screen.getByRole("button", { name: /actions/i });
            fireEvent.pointerDown(menuTrigger);
            fireEvent.click(screen.getByText("Run Now"));

            expect(onRunNow).toHaveBeenCalledWith(task);
        });

        it("renders as disabled and shows 'Running…' when isRunNowPending is true", () => {
            const onRunNow = vi.fn();
            renderTaskCard({}, { onRunNow, isRunNowPending: true });
            const menuTrigger = screen.getByRole("button", { name: /actions/i });
            fireEvent.pointerDown(menuTrigger);
            const item = screen.getByText("Running…").closest("[role='menuitem']");
            expect(item).toBeInTheDocument();
            expect(item).toHaveAttribute("data-disabled");
        });
    });
});
