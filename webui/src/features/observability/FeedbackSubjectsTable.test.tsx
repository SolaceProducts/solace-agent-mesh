// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { FeedbackSummaryRow, FeedbackTrendPoint } from "@/api/feedback";

import { FeedbackSubjectsTable, NEGATIVE_RATIO_TOOLTIP } from "./FeedbackSubjectsTable";

const TYPE_CELL = 1;
const TOTAL_ACTIVITIES_CELL = 2;
const POSITIVE_FEEDBACK_CELL = 3;
const NEGATIVE_RATIO_CELL = 5;

const row = (id: string, name: string, type: "agent" | "workflow", over: Partial<FeedbackSummaryRow> = {}): FeedbackSummaryRow => ({
    subject: { id, name, type },
    totalActivities: 14300,
    positiveFeedback: 548,
    negativeFeedback: 11,
    negativeRatio: 0.00077,
    trend: [],
    ...over,
});

const trendOf = (...ratios: number[]): FeedbackTrendPoint[] => ratios.map((negativeRatio, i) => ({ date: `2026-09-0${i + 1}T00:00:00Z`, totalActivities: 100, positiveFeedback: 0, negativeFeedback: Math.round(negativeRatio * 100), negativeRatio }));

function renderTable(over: Partial<React.ComponentProps<typeof FeedbackSubjectsTable>> = {}) {
    const props = {
        rows: [row("a1", "Procurement Intake Router", "agent"), row("w1", "Budget Approval Workflow", "workflow")],
        sortKey: "negativeRatio" as const,
        sortDir: "desc" as const,
        onSort: vi.fn(),
        onSelectSubject: vi.fn(),
        ...over,
    };
    render(<FeedbackSubjectsTable {...props} />);
    return props;
}

describe("FeedbackSubjectsTable", () => {
    it("renders the mock's seven columns", () => {
        renderTable();

        ["Agent / Workflow", "Type", "Total Activities", "Positive Feedback", "Negative Feedback", "Negative Ratio (%)", "Trend"].forEach(label => {
            expect(screen.getByText(label)).toBeInTheDocument();
        });
    });

    it("explains the ratio's denominator on hovering the Negative Ratio heading", async () => {
        renderTable();

        await userEvent.hover(screen.getByRole("button", { name: /Negative Ratio/i }));

        expect(await screen.findByRole("tooltip")).toHaveTextContent(NEGATIVE_RATIO_TOOLTIP);
    });

    it("keeps the Negative Ratio heading named after its column", async () => {
        renderTable();

        const heading = screen.getByRole("button", { name: "Negative Ratio (%)" });
        await userEvent.hover(heading);
        await screen.findByRole("tooltip");

        expect(screen.getByRole("button", { name: "Negative Ratio (%)" })).toBe(heading);
    });

    it("leaves the other headings unexplained", async () => {
        renderTable();

        await userEvent.hover(screen.getByRole("button", { name: /Positive Feedback/i }));

        await expect(screen.findByRole("tooltip", {}, { timeout: 1500 })).rejects.toThrow();
    });

    it("names total activity as the denominator, not rated activity", () => {
        expect(NEGATIVE_RATIO_TOOLTIP).toMatch(/total activities/i);
        expect(NEGATIVE_RATIO_TOOLTIP).toMatch(/not by the number of rated activities/i);
    });

    it("formats counts with separators and the ratio through the shared formatter", () => {
        renderTable({
            rows: [row("a1", "Procurement Intake Router", "agent"), row("w1", "Budget Approval Workflow", "workflow", { totalActivities: 9120, positiveFeedback: 203, negativeRatio: 0.0421 })],
        });

        const cellsOf = (name: string) => within(screen.getByText(name).closest("tr")!).getAllByRole("cell");

        const agentCells = cellsOf("Procurement Intake Router");
        expect(agentCells[TOTAL_ACTIVITIES_CELL]).toHaveTextContent("14,300");
        expect(agentCells[POSITIVE_FEEDBACK_CELL]).toHaveTextContent("548");
        expect(agentCells[NEGATIVE_RATIO_CELL]).toHaveTextContent("0.077%");

        const workflowCells = cellsOf("Budget Approval Workflow");
        expect(workflowCells[TOTAL_ACTIVITIES_CELL]).toHaveTextContent("9,120");
        expect(workflowCells[POSITIVE_FEEDBACK_CELL]).toHaveTextContent("203");
        expect(workflowCells[NEGATIVE_RATIO_CELL]).toHaveTextContent("4.210%");
    });

    it("reports a sort request for a sortable column", async () => {
        const props = renderTable();

        await userEvent.click(screen.getByRole("button", { name: /Total Activities/i }));

        expect(props.onSort).toHaveBeenCalledWith("totalActivities");
    });

    it("pins its header, so scrolling a long page of rows keeps the columns named", () => {
        const { container } = render(<FeedbackSubjectsTable rows={[row("a1", "Procurement Intake Router", "agent")]} sortKey="negativeRatio" sortDir="desc" onSort={vi.fn()} onSelectSubject={vi.fn()} />);

        expect(container.querySelector("thead")!.className).toContain("[&_th]:sticky");
    });

    it("offers no sort control on Trend, which the API cannot order", () => {
        renderTable();

        expect(screen.queryByRole("button", { name: /Trend/i })).not.toBeInTheDocument();
    });

    it("reports the clicked subject, carrying its type", async () => {
        const props = renderTable();

        await userEvent.click(screen.getByText("Budget Approval Workflow"));

        expect(props.onSelectSubject).toHaveBeenCalledWith({ id: "w1", name: "Budget Approval Workflow", type: "workflow" });
    });

    it("opens a row from the keyboard", async () => {
        const props = renderTable();

        const cell = screen.getByText("Procurement Intake Router");
        await userEvent.click(cell);
        vi.mocked(props.onSelectSubject).mockClear();

        const rowEl = cell.closest("tr")!;
        rowEl.focus();
        await userEvent.keyboard("{Enter}");

        expect(props.onSelectSubject).toHaveBeenCalledWith({ id: "a1", name: "Procurement Intake Router", type: "agent" });
    });

    it("falls back to the id when a subject has no resolved name", () => {
        renderTable({ rows: [row("a1", "", "agent")] });

        expect(screen.getByText("a1")).toBeInTheDocument();
    });

    it("skips a row with no subject, which the tenant shape produces", () => {
        const tenantRow = { totalActivities: 1, positiveFeedback: 0, negativeFeedback: 0, negativeRatio: 0, trend: [] } as FeedbackSummaryRow;
        renderTable({ rows: [tenantRow] });

        expect(screen.queryAllByRole("row")).toHaveLength(1);
    });

    it("gives each row the sparkline for its own subject", () => {
        renderTable({
            rows: [row("a1", "Procurement Intake Router", "agent", { trend: trendOf(0.01, 0.01, 0.05, 0.05) }), row("w1", "Budget Approval Workflow", "workflow", { trend: trendOf(0.05, 0.05, 0.01, 0.01) })],
        });

        const agentRow = screen.getByText("Procurement Intake Router").closest("tr")!;
        const workflowRow = screen.getByText("Budget Approval Workflow").closest("tr")!;

        expect(within(agentRow).getByLabelText("Negative rate rising for Procurement Intake Router")).toBeInTheDocument();
        expect(within(workflowRow).getByLabelText("Negative rate falling for Budget Approval Workflow")).toBeInTheDocument();
    });

    it("states the trend direction without a direction glyph beside the sparkline", () => {
        renderTable({ rows: [row("a1", "Procurement Intake Router", "agent", { trend: trendOf(0.01, 0.01, 0.05, 0.05) })] });

        const agentRow = screen.getByText("Procurement Intake Router").closest("tr")!;

        expect(agentRow.querySelector("svg.lucide-arrow-up-right")).toBeNull();
        expect(agentRow.querySelector("svg.lucide-arrow-down-right")).toBeNull();
        expect(agentRow.querySelector("svg.lucide-minus")).toBeNull();
        expect(within(agentRow).getByRole("img", { name: "Negative rate rising for Procurement Intake Router" })).toHaveAttribute("title", "Negative rate rising");
    });

    it("marks each subject type with its own glyph and leaves an unrecognised type unglyphed", () => {
        const unknownType = "unknown" as "agent" | "workflow";
        renderTable({
            rows: [row("a1", "Procurement Intake Router", "agent"), row("w1", "Budget Approval Workflow", "workflow"), row("u1", "Unattributed Subject", unknownType)],
        });

        const typeCellOf = (name: string) => within(screen.getByText(name).closest("tr")!).getAllByRole("cell")[TYPE_CELL];

        expect(typeCellOf("Procurement Intake Router").querySelector(".lucide-bot")).toBeInTheDocument();
        expect(typeCellOf("Budget Approval Workflow").querySelector(".lucide-workflow")).toBeInTheDocument();

        const unknownCell = typeCellOf("Unattributed Subject");
        expect(unknownCell).toHaveTextContent("unknown");
        expect(unknownCell.querySelector("svg")).not.toBeInTheDocument();
    });

    it("shows the descending chevron on the active sort column and the neutral icon elsewhere", () => {
        renderTable();

        const ratioHeader = screen.getByRole("button", { name: /Negative Ratio/i }).closest("th")!;
        const activitiesHeader = screen.getByRole("button", { name: /Total Activities/i }).closest("th")!;

        expect(ratioHeader.querySelector(".lucide-chevron-down")).toBeInTheDocument();
        expect(activitiesHeader.querySelector(".lucide-chevrons-up-down")).toBeInTheDocument();
    });
});
