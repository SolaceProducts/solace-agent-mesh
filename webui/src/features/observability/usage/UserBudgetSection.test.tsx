// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { usageBudget } from "@/testing/data/usage";

import { BudgetStandingBanner, PeriodSpendAndBudget } from "./UserBudgetSection";

const NOW = new Date("2026-10-16T14:00:00.000Z");

describe("PeriodSpendAndBudget, off This month", () => {
    test("sets the period's spend beside the current monthly budget, with no standing between them", () => {
        render(<PeriodSpendAndBudget spend="412.3000000000" budget={usageBudget({ amount: "450", spend: "412.30" })} />);
        const figures = screen.getByLabelText("Cost and Budget");
        expect(figures).toHaveTextContent("Cost This Period$412.30");
        expect(figures).toHaveTextContent("Current Monthly Budget$450");
        expect(figures).not.toHaveTextContent("%");
    });

    test("reads a top-up as one figure, its breakdown for a screen reader", () => {
        render(<PeriodSpendAndBudget spend="96.4000000000" budget={usageBudget({ amount: "200", spend: "96.40", topUp: "50" })} />);
        expect(screen.getByText("$250")).toBeInTheDocument();
        expect(screen.getByText(/^\(\$200 \+ \$50 top-up until .+\)$/)).toHaveClass("sr-only");
    });

    test.each([
        ["an unlimited budget as Unlimited", usageBudget({ amount: null, spend: "9.80" }), "Unlimited"],
        ["no budget as such", null, "No budget applies"],
    ])("reads %s", (_what, budget, expected) => {
        render(<PeriodSpendAndBudget spend="9.8000000000" budget={budget} />);
        expect(screen.getByText(expected)).toBeInTheDocument();
    });
});

describe("BudgetStandingBanner", () => {
    test("names a user near their budget, the amount carrying its top-up", () => {
        render(<BudgetStandingBanner name="Olive Operations" budget={usageBudget({ amount: "100", spend: "90", topUp: "10" })} now={NOW} />);
        expect(screen.getByRole("alert")).toHaveTextContent(/Olive Operations is at 81\.8% of their \$110 \(\$100 \+ \$10 top-up until .+\) current budget/);
        expect(screen.getByRole("alert")).toHaveTextContent("Cost this month: $90.00, with 16 days left.");
    });

    test("offers the Users page only when given a way there", async () => {
        const budget = usageBudget({ amount: "100", spend: "105" });
        const onGoToUsers = vi.fn();
        const { rerender } = render(<BudgetStandingBanner name="Olive Operations" budget={budget} now={NOW} onGoToUsers={onGoToUsers} />);
        await userEvent.click(screen.getByRole("button", { name: "Go to Users" }));
        expect(onGoToUsers).toHaveBeenCalledOnce();
        rerender(<BudgetStandingBanner name="Olive Operations" budget={budget} now={NOW} />);
        expect(screen.queryByRole("button", { name: "Go to Users" })).not.toBeInTheDocument();
    });

    test.each([
        ["within budget", usageBudget({ amount: "200", spend: "96.40", topUp: "50" })],
        ["unlimited", usageBudget({ amount: null, spend: "9.80" })],
    ])("says nothing for a user %s", (_what, budget) => {
        const { container } = render(<BudgetStandingBanner name="Casey" budget={budget} now={NOW} />);
        expect(container).toBeEmptyDOMElement();
    });
});
