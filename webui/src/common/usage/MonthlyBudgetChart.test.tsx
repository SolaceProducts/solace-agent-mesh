// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { UsageMoney } from "@/api/usage";

import { type BudgetMonth, BudgetMonthTooltip, MonthlyBudgetChart } from "./MonthlyBudgetChart";

const month = (name: string, budgetAmount: UsageMoney | null, partialNote: string | null = null): BudgetMonth => ({ month: name, cost: "58.2000000000", budgetAmount, partialNote });
const at = (source: BudgetMonth) => [{ payload: { month: source.month, cost: 58.2, source } }];

describe("BudgetMonthTooltip", () => {
    test("this month shows its spend and the current budget", () => {
        render(<BudgetMonthTooltip active payload={at(month("2026-10", "250.0000000000"))} currentMonth="2026-10" />);
        expect(screen.getByText("Oct")).toBeInTheDocument();
        expect(screen.getByText("$58.20 spent")).toBeInTheDocument();
        expect(screen.getByText("Current budget $250")).toBeInTheDocument();
    });

    test("a month only partly counted says why", () => {
        render(<BudgetMonthTooltip active payload={at(month("2026-08", null, "kept from Aug 12"))} currentMonth="2026-10" />);
        expect(screen.getByText("kept from Aug 12")).toBeInTheDocument();
    });
});

describe("MonthlyBudgetChart", () => {
    test("gives screen readers each month's spend, this month's budget, and no budget for earlier months, naming a partial month's reason", () => {
        render(
            <MonthlyBudgetChart
                heading="Spend by Month"
                months={[month("2026-08", "200.0000000000", "kept from Aug 12"), month("2026-09", "200.0000000000"), month("2026-10", "250.0000000000")]}
                currentMonth="2026-10"
                currentBudget="250.0000000000"
                currentStatus="ok"
            />
        );
        const rows = screen.getAllByRole("row").map(row => row.textContent);
        expect(rows).toEqual(["MonthSpentBudget", "Aug (kept from Aug 12)$58.20Not recorded", "Sep$58.20Not recorded", "Oct$58.20Current budget $250"]);
    });

    test("has no budget column when no month has a budget", () => {
        render(<MonthlyBudgetChart heading="Spend by Month" months={[month("2026-09", null)]} currentMonth="2026-10" currentBudget={null} currentStatus={null} />);
        expect(screen.getAllByRole("columnheader").map(header => header.textContent)).toEqual(["Month", "Spent"]);
    });
});
