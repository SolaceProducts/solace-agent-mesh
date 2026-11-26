// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { ExecutionStatus, TaskExecution } from "@/types/scheduled-tasks";

import { ExecutionHistoryTable } from "./ExecutionHistoryTable";

function execution(status: ExecutionStatus): TaskExecution {
    return { id: `exec-${status}`, scheduledTaskId: "task-1", status, scheduledFor: new Date(0).toISOString() } as TaskExecution;
}

function renderTable(executions: TaskExecution[], onDeleteExecution = vi.fn()) {
    render(
        <ExecutionHistoryTable
            executions={executions}
            totalCount={executions.length}
            page={1}
            onPageChange={vi.fn()}
            onRowClick={vi.fn()}
            isLoading={false}
            onDeleteExecution={onDeleteExecution}
            filterFrom=""
            filterTo=""
            onFilterFromChange={vi.fn()}
            onFilterToChange={vi.fn()}
        />
    );
    return onDeleteExecution;
}

function openRowMenu(rowIndex: number) {
    const rows = screen.getAllByRole("row").slice(1);
    const trigger = rows[rowIndex].querySelector("button[aria-haspopup='menu']");
    if (!trigger) throw new Error("row has no actions menu");
    fireEvent.pointerDown(trigger);
}

describe("ExecutionHistoryTable — delete", () => {
    test.each<ExecutionStatus>(["pending", "running"])("Delete is disabled for a %s execution", async status => {
        const onDelete = renderTable([execution(status)]);

        openRowMenu(0);
        const item = await screen.findByRole("menuitem", { name: /delete/i });

        expect(item).toHaveAttribute("aria-disabled", "true");
        fireEvent.click(item);
        expect(onDelete).not.toHaveBeenCalled();
    });

    test("Delete is available for a finished execution", async () => {
        const finished = execution("completed");
        const onDelete = renderTable([finished]);

        openRowMenu(0);
        const item = await screen.findByRole("menuitem", { name: /delete/i });
        fireEvent.click(item);

        expect(onDelete).toHaveBeenCalledWith(finished);
    });
});
