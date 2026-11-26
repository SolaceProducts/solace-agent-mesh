// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import type { FeedbackSummaryRow, FeedbackTrendPoint, TrendBucketGranularity } from "@/api/feedback";

import { NegativeTrendChart, TrendTooltip } from "./NegativeTrendChart";
import type { TrendSeries } from "./trendChartData";

beforeAll(() => {
    if (typeof global.ResizeObserver === "undefined") {
        global.ResizeObserver = class {
            observe() {}
            unobserve() {}
            disconnect() {}
        } as unknown as typeof ResizeObserver;
    }
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
        width: 900,
        height: 240,
        top: 0,
        left: 0,
        bottom: 240,
        right: 900,
        x: 0,
        y: 0,
        toJSON: () => {},
    } as DOMRect);
});

function trendRowFixture(id: string, trend: Omit<FeedbackTrendPoint, "totalActivities" | "positiveFeedback">[], name = "Procurement Intake Router"): FeedbackSummaryRow {
    const fullTrend: FeedbackTrendPoint[] = trend.map(point => ({ totalActivities: 1000, positiveFeedback: 40, ...point }));
    return {
        subject: { id, name, type: "agent" },
        totalActivities: 1000,
        positiveFeedback: 40,
        negativeFeedback: fullTrend.reduce((sum, p) => sum + p.negativeFeedback, 0),
        negativeRatio: fullTrend[fullTrend.length - 1]?.negativeRatio ?? 0,
        trend: fullTrend,
    };
}

function twoSubjectRows(): FeedbackSummaryRow[] {
    const dates = ["2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z", "2026-09-03T00:00:00Z"];
    return [
        trendRowFixture(
            "a1",
            dates.map((date, i) => ({ date, negativeFeedback: i + 1, negativeRatio: 0.004 + i * 0.001 })),
            "Procurement Intake Router"
        ),
        trendRowFixture(
            "b2",
            dates.map((date, i) => ({ date, negativeFeedback: i, negativeRatio: 0.002 + i * 0.001 })),
            "Vendor Q&A Assistant"
        ),
    ];
}

function renderChart({ rows, granularity = "day" }: { rows: FeedbackSummaryRow[]; granularity?: TrendBucketGranularity }) {
    return render(<NegativeTrendChart rows={rows} granularity={granularity} />);
}

function renderTooltip() {
    const rows = twoSubjectRows();
    const series: TrendSeries[] = [
        { key: "a1", name: "Procurement Intake Router", color: "#3C69E1" },
        { key: "b2", name: "Vendor Q&A Assistant", color: "#009193" },
    ];
    const payload = [{ dataKey: "a1", graphicalItemId: "a1", value: 0.004, color: series[0].color }];
    return render(<TrendTooltip active payload={payload} label="2026-09-01T00:00:00Z" series={series} rows={rows} granularity="day" hoveredKey="a1" />);
}

describe("NegativeTrendChart", () => {
    it("draws no end-of-line labels", () => {
        renderChart({ rows: twoSubjectRows() });

        const svg = document.querySelector("svg") as unknown as HTMLElement;
        expect(within(svg).queryByText("Procurement Intake Router")).not.toBeInTheDocument();
    });

    it("still plots a single-bucket window as a visible dot, because a line through one point draws nothing", () => {
        renderChart({ rows: [trendRowFixture("a1", [{ date: "2026-09-01T14:00:00Z", negativeRatio: 0.5, negativeFeedback: 5 }])], granularity: "hour" });

        expect(document.querySelectorAll(".recharts-dot").length).toBeGreaterThan(0);
    });

    it("renders without a tail read on an empty trend", () => {
        renderChart({ rows: [] });

        expect(document.querySelector("svg")).not.toBeInTheDocument();
    });

    it("renders the tooltip on a light surface", () => {
        const { container } = renderTooltip();
        expect(container.firstElementChild).toHaveClass("bg-(--background-w10)");
        expect(container.firstElementChild).not.toHaveClass("bg-(--background-wMain)");
    });

    it("names the activity count the hovered ratio is measured against", () => {
        renderTooltip();

        expect(screen.getByText(/1 negative of 1,000 activities/)).toBeInTheDocument();
    });

    it("plots a wide transparent hover band over every line, so the hit area is not the 2px stroke", () => {
        renderChart({ rows: twoSubjectRows() });

        const curves = Array.from(document.querySelectorAll<SVGPathElement>("path.recharts-line-curve"));
        const visibleStrokeWidths = curves.filter(path => path.getAttribute("stroke") !== "transparent").map(path => Number(path.getAttribute("stroke-width")));
        const bandStrokeWidths = curves.filter(path => path.getAttribute("stroke") === "transparent").map(path => Number(path.getAttribute("stroke-width")));

        expect(bandStrokeWidths).toHaveLength(visibleStrokeWidths.length);
        expect(Math.min(...bandStrokeWidths)).toBeGreaterThan(Math.max(...visibleStrokeWidths) * 4);
    });

    it("shows the hovered series once when its hover band repeats its dataKey in the payload", () => {
        const rows = twoSubjectRows();
        const series: TrendSeries[] = [{ key: "a1", name: "Procurement Intake Router", color: "#3C69E1" }];
        const duplicatedPayload = [
            { dataKey: "a1", graphicalItemId: "a1", value: 0.004, color: "#3C69E1" },
            { dataKey: "a1", graphicalItemId: "a1", value: 0.004, color: "#3C69E1" },
        ];

        render(<TrendTooltip active payload={duplicatedPayload} label="2026-09-01T00:00:00Z" series={series} rows={rows} granularity="day" hoveredKey="a1" />);

        expect(screen.getAllByText("Procurement Intake Router")).toHaveLength(1);
    });

    it("titles the chart generically, ranked Top 5, when no subject is filtered", () => {
        render(<NegativeTrendChart rows={twoSubjectRows()} granularity="day" />);

        expect(screen.getByText("Negative Trend (Top 5)")).toBeInTheDocument();
    });

    it("titles the chart with the filtered agent's name and icon instead of the Top 5 ranking label", () => {
        const { container } = render(
            <NegativeTrendChart rows={[trendRowFixture("a1", [{ date: "2026-09-01T00:00:00Z", negativeFeedback: 2, negativeRatio: 0.01 }])]} granularity="day" filteredSubject={{ label: "Procurement Intake Router", kind: "agent" }} />
        );

        expect(screen.queryByText("Negative Trend (Top 5)")).not.toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Procurement Intake Router" })).toBeInTheDocument();
        expect(container.querySelector("svg.lucide-bot")).toBeInTheDocument();
    });

    it("renders the legend below the chart, not above it", () => {
        const { container } = renderChart({ rows: twoSubjectRows() });

        const svg = container.querySelector("svg");
        const legendText = screen.getByText("Procurement Intake Router");
        expect(svg?.compareDocumentPosition(legendText)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    });

    it("left-aligns the legend and sizes its text larger than the chart's small print", () => {
        renderChart({ rows: twoSubjectRows() });

        const legendText = screen.getByText("Procurement Intake Router");
        const legend = legendText.closest("div");
        expect(legend).not.toHaveClass("justify-between");
        expect(legend).toHaveClass("text-sm");
        expect(legend).not.toHaveClass("text-xs");
        expect(legend).not.toHaveClass("text-base");
    });

    it("offsets the legend to start under the chart's plotted grid, not the card title", () => {
        renderChart({ rows: twoSubjectRows() });

        const legendText = screen.getByText("Procurement Intake Router");
        const legend = legendText.closest("div");
        expect(legend).toHaveClass("ml-[56px]");
    });

    it("groups Ratio and Count into one segmented control rather than two loose buttons", async () => {
        const user = userEvent.setup();
        renderChart({ rows: twoSubjectRows() });

        const group = screen.getByRole("tablist");
        expect(within(group).getByRole("tab", { name: "Ratio" })).toHaveAttribute("aria-selected", "true");

        await user.click(within(group).getByRole("tab", { name: "Count" }));

        expect(within(group).getByRole("tab", { name: "Count" })).toHaveAttribute("aria-selected", "true");
        expect(within(group).getByRole("tab", { name: "Ratio" })).toHaveAttribute("aria-selected", "false");
    });

    it("hands back the clicked series' own subject, not the first one plotted", async () => {
        const user = userEvent.setup();
        const onSelectSubject = vi.fn();
        render(<NegativeTrendChart rows={twoSubjectRows()} granularity="day" onSelectSubject={onSelectSubject} />);

        await user.click(screen.getByRole("button", { name: "Vendor Q&A Assistant" }));

        expect(onSelectSubject).toHaveBeenCalledWith({ id: "b2", name: "Vendor Q&A Assistant", type: "agent" });
    });

    it("leaves the legend as plain text when there is nothing left to narrow to", () => {
        renderChart({ rows: twoSubjectRows() });

        expect(screen.queryByRole("button", { name: "Vendor Q&A Assistant" })).not.toBeInTheDocument();
        expect(screen.getByText("Vendor Q&A Assistant")).toBeInTheDocument();
    });

    it("says the lines can be selected only while they can be", () => {
        const { rerender } = render(<NegativeTrendChart rows={twoSubjectRows()} granularity="day" onSelectSubject={vi.fn()} />);
        expect(screen.getByText(/Select a line to filter the dashboard to it/)).toBeInTheDocument();

        rerender(<NegativeTrendChart rows={twoSubjectRows()} granularity="day" />);
        expect(screen.queryByText(/Select a line to filter the dashboard to it/)).not.toBeInTheDocument();
    });

    it("uses the workflow icon when the filtered subject is a workflow", () => {
        const { container } = render(
            <NegativeTrendChart
                rows={[trendRowFixture("w1", [{ date: "2026-09-01T00:00:00Z", negativeFeedback: 2, negativeRatio: 0.01 }], "Budget Approval Workflow")]}
                granularity="day"
                filteredSubject={{ label: "Budget Approval Workflow", kind: "workflow" }}
            />
        );

        expect(container.querySelector("svg.lucide-workflow")).toBeInTheDocument();
    });
});
