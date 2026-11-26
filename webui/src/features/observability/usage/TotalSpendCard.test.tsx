// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { UsageDay, UsageMoney, UsageSummary, UsageWindow } from "@/api/usage";

import { usageBudget, usageSummary } from "@/testing/data/usage";
import { NO_TOKENS } from "@/utils";

import { MonthSpendTooltip, TotalSpendCard } from "./TotalSpendCard";

const LABEL = "vs. same point last month";

function renderCard(summary: Partial<UsageSummary> = {}, isThisMonth = true) {
    render(<TotalSpendCard summary={{ ...usageSummary, ...summary }} comparisonLabel={LABEL} isThisMonth={isThisMonth} today="2026-10-19" />);
}

const comparisonLine = () => screen.getByText("same point last month").closest("p");

describe("TotalSpendCard comparison line", () => {
    test("shows spend rising against the comparison period", () => {
        renderCard();
        expect(screen.getByText("$1,240.58")).toBeInTheDocument();
        expect(screen.getByText("Up")).toBeInTheDocument();
        expect(comparisonLine()).toHaveTextContent(/36\.0%vs\. same point last month \(Sep 1 – Sep 16, 14:05 UTC\)$/);
    });

    test("shows spend falling against the comparison period", () => {
        renderCard({ comparison: { cost: "1500.0000000000", changePercent: -17.3 } });
        expect(screen.getByText("Down")).toBeInTheDocument();
        expect(screen.getByText("17.3%")).toBeInTheDocument();
    });

    test("reads a change that rounds to 0.0% as no change rather than an arrow", () => {
        renderCard({ comparison: { cost: "1240.0000000000", changePercent: 0.04 } });
        expect(comparisonLine()).toHaveTextContent(`No change ${LABEL}`);
        expect(screen.queryByText("Up")).not.toBeInTheDocument();
    });

    test.each([
        ["cost now reads as a rise from $0, not a percentage", usageSummary.totals.cost, "Up from $0"],
        ["no cost now either reads as no change", "0.0000000000", "No change "],
        ["a cost the card shows as $0.00 reads as no change, not a rise", "0.0030000000", "No change "],
        ["a malformed cost reads as no comparison, not a rise", "not money", "— "],
        ["a negative cost reads as no comparison, not a rise", "-1.0000000000", "— "],
    ] as const)("with no cost in the comparison window, %s", (_what, cost, change) => {
        renderCard({ totals: { ...usageSummary.totals, cost: cost as UsageMoney }, comparison: { cost: "0.0000000000", changePercent: null } });
        expect(comparisonLine()).toHaveTextContent(`${change}vs. same point last month (No cost recorded Sep 1 – Sep 16, 14:05 UTC.)`);
        expect(screen.queryByText("0.0%")).not.toBeInTheDocument();
    });

    test.each([
        ["whole days", { startTime: "2026-10-30T00:00:00.000Z", endTime: "2026-11-01T00:00:00.000Z" }, "Oct 30 – Oct 31"],
        ["a day ending partway", { startTime: "2026-10-01T00:00:00.000Z", endTime: "2026-10-01T09:30:00.000Z" }, "Oct 1, until 09:30 UTC"],
        ["a window spanning two years", { startTime: "2026-12-31T00:00:00.000Z", endTime: "2027-01-02T00:00:00.000Z" }, "Dec 31, 2026 – Jan 1, 2027"],
    ] satisfies ReadonlyArray<[string, UsageWindow, string]>)("names the comparison window's dates on the period: %s", (_what, comparisonWindow, dates) => {
        renderCard({ comparisonWindow });
        expect(comparisonLine()).toHaveTextContent(`(${dates})`);
    });

    test("a comparison window it cannot read leaves the dates out rather than taking the card down", () => {
        renderCard({ comparisonWindow: { ...usageSummary.comparisonWindow!, startTime: "not a time" as UsageWindow["startTime"] }, comparison: { cost: "0.0000000000", changePercent: null } });
        expect(comparisonLine()).toHaveTextContent("Up from $0vs. same point last month (No cost recorded for the previous period.)");
    });

    test.each([
        ["usage that old is no longer kept", true, "No usage from the previous period to compare. Usage is kept for 90 days."],
        ["history is still being built", false, "Older usage is still being calculated, so there is nothing to compare yet."],
    ])("keeps the line's shape with no comparison window, and says why: %s", (_why, historyComplete, reason) => {
        renderCard({ comparison: null, comparisonWindow: null, historyComplete });
        expect(comparisonLine()).toHaveTextContent(`— ${LABEL} (${reason})`);
    });
});

describe("TotalSpendCard mesh budget", () => {
    const approaching = usageBudget({ amount: "1400", spend: "1240.58" });

    test("shows this month's standing on This month", () => {
        renderCard({ meshBudget: approaching });
        expect(screen.getByText("$1,400").closest("p")).toHaveTextContent("88.6% of $1,400 current budget this month");
    });

    test("shows a line in place of the bar for a budget with no cap", () => {
        renderCard({ meshBudget: usageBudget({ amount: null, spend: "1240.58" }) });
        expect(screen.getByText("No Agent Mesh budget limit")).toBeInTheDocument();
    });

    test.each([
        ["with no mesh budget", { meshBudget: null }, true],
        ["on another period, since the standing is this month's", { meshBudget: approaching }, false],
    ])("shows no budget standing %s", (_what, summary, isThisMonth) => {
        renderCard(summary, isThisMonth);
        expect(screen.queryByText(/current budget|budget limit/)).not.toBeInTheDocument();
    });
});

describe("TotalSpendCard months", () => {
    const daily: UsageDay[] = [
        { date: "2026-07-25", cost: "99.0000000000", tokens: NO_TOKENS, activities: 1 },
        { date: "2026-08-10", cost: "400.0000000000", tokens: NO_TOKENS, activities: 1 },
        { date: "2026-09-12", cost: "510.2500000000", tokens: NO_TOKENS, activities: 1 },
        { date: "2026-10-05", cost: "330.3300000000", tokens: NO_TOKENS, activities: 1 },
    ];
    const longPeriod = { daily, window: { startTime: "2026-07-25T00:00:00.000Z", endTime: "2026-10-20T00:00:00.000Z" } } as const;

    test("gives every month the period touches against the current budget, saying which it covers only partly", () => {
        renderCard(longPeriod, false);
        const table = screen.getByRole("table", { name: "Cost by Month" });
        expect(
            within(table)
                .getAllByRole("row")
                .map(row => row.textContent)
        ).toEqual(["MonthCost", "Jul (Partial month · 7 of 31 days)$99.00", "Aug$400.00", "Sep$510.25", "Oct$330.33 (51.6% of current budget)"]);
        expect(screen.getByText("Current budget $2,400")).toBeInTheDocument();
    });

    test("draws no budget line with no mesh budget", () => {
        renderCard({ ...longPeriod, meshBudget: null }, false);
        expect(screen.getByRole("table", { name: "Cost by Month" })).toBeInTheDocument();
        expect(screen.queryByText(/Current budget/)).not.toBeInTheDocument();
    });

    test("has no months on a period of 31 days or fewer", () => {
        renderCard({ daily });
        expect(screen.queryByRole("table", { name: "Cost by Month" })).not.toBeInTheDocument();
    });
});

describe("MonthSpendTooltip", () => {
    const at = (month: string, partial = false) => [{ payload: { month, cost: 1, source: { month, cost: "1.5000000000", days: partial ? 10 : 30, daysInMonth: 31, partial } } }];

    test("marks this month's spend as so far, with its share of the current budget", () => {
        render(<MonthSpendTooltip active payload={at("2026-10")} today="2026-10-19" budget={usageSummary.meshBudget} />);
        expect(screen.getByText("$1.50 cost so far")).toBeInTheDocument();
        expect(screen.getByText("51.6% of current budget")).toBeInTheDocument();
    });

    test("reads an earlier month's spend as final", () => {
        render(<MonthSpendTooltip active payload={at("2026-09")} today="2026-10-19" budget={usageSummary.meshBudget} />);
        expect(screen.getByText("Sep")).toBeInTheDocument();
        expect(screen.getByText("$1.50 cost")).toBeInTheDocument();
        expect(screen.queryByText(/current budget|Partial month/)).not.toBeInTheDocument();
    });

    test("says how much of a month the period covers when it covers only part", () => {
        render(<MonthSpendTooltip active payload={at("2026-07", true)} today="2026-10-19" budget={usageSummary.meshBudget} />);
        expect(screen.getByText("Partial month · 10 of 31 days")).toBeInTheDocument();
    });
});
