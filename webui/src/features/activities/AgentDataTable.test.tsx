// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { AgentPerformanceMetrics } from "@/types/activities";

import { getAgentColumns } from "./AgentColumns";
import { AgentDataTable } from "./AgentDataTable";

function agent(displayName: string, totalLlmTimeMs: number, totalToolTimeMs: number): AgentPerformanceMetrics {
    return { agentName: displayName, instanceId: `${displayName}:task`, displayName, llmCalls: [], toolCalls: [], totalLlmTimeMs, totalToolTimeMs };
}

const AGENTS = [agent("Bravo", 1000, 500), agent("Alpha", 4000, 1000), agent("Charlie", 2000, 0)];

function renderTable(onRowClick = vi.fn(), selectedRowId?: string) {
    const columns = getAgentColumns();
    render(<AgentDataTable columns={columns} data={AGENTS} onRowClick={onRowClick} selectedRowId={selectedRowId} />);
    return onRowClick;
}

function bodyNames() {
    return screen
        .getAllByRole("row")
        .slice(1)
        .map(row => within(row).getAllByRole("cell")[0].textContent);
}

async function sortBy(columnTitle: string, direction: "Asc" | "Desc") {
    await userEvent.click(screen.getByRole("button", { name: columnTitle }));
    await userEvent.click(await screen.findByRole("menuitem", { name: direction }));
}

describe("AgentDataTable", () => {
    test("renders a header per column and a row per agent in data order", () => {
        renderTable();

        for (const title of ["Agent Name", "Total Active Time", "LLM Time", "Tool Time"]) {
            expect(screen.getByRole("button", { name: title })).toBeInTheDocument();
        }
        expect(bodyNames()).toEqual(["Bravo", "Alpha", "Charlie"]);
    });

    test("formats cell values in seconds, including the computed active time", () => {
        renderTable();

        const alphaCells = within(screen.getByText("Alpha").closest("tr")!).getAllByRole("cell");
        expect(alphaCells.map(cell => cell.textContent)).toEqual(["Alpha", "5.00s", "4.00s", "1.00s"]);
    });

    test("sorts by an accessor column in both directions", async () => {
        renderTable();

        await sortBy("LLM Time", "Asc");
        expect(bodyNames()).toEqual(["Bravo", "Charlie", "Alpha"]);

        await sortBy("LLM Time", "Desc");
        expect(bodyNames()).toEqual(["Alpha", "Charlie", "Bravo"]);
    });

    test("sorts total active time with the custom sort function (LLM + tool time)", async () => {
        renderTable();

        await sortBy("Total Active Time", "Asc");
        expect(bodyNames()).toEqual(["Bravo", "Charlie", "Alpha"]);

        await sortBy("Total Active Time", "Desc");
        expect(bodyNames()).toEqual(["Alpha", "Charlie", "Bravo"]);
    });

    test("hides a column from the header menu", async () => {
        renderTable();

        await userEvent.click(screen.getByRole("button", { name: "Tool Time" }));
        await userEvent.click(await screen.findByRole("menuitem", { name: "Hide" }));

        expect(screen.queryByRole("button", { name: "Tool Time" })).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: "LLM Time" })).toBeInTheDocument();
    });

    test("reports the clicked agent and marks the selected row", async () => {
        const onRowClick = renderTable(vi.fn(), "Charlie:task");

        expect(screen.getByText("Charlie").closest("tr")).toHaveAttribute("data-state", "selected");
        expect(screen.getByText("Alpha").closest("tr")).not.toHaveAttribute("data-state", "selected");

        await userEvent.click(screen.getByText("Alpha"));
        expect(onRowClick).toHaveBeenCalledWith(AGENTS[1]);
    });

    test("shows the empty state when there is no data", () => {
        render(<AgentDataTable columns={getAgentColumns()} data={[]} onRowClick={vi.fn()} />);

        expect(screen.getByText("No agent data available.")).toBeInTheDocument();
    });
});
