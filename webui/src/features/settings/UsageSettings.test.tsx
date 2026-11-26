// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { type UsageSavings, type UsageSelf, useSelfUsage, useSelfUsageSavings } from "@/api/usage";

import { usageBudget, usageGoldens } from "@/testing/data/usage";

import { UsageSettings } from "./UsageSettings";

vi.mock("@/api/usage", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/usage")>()), useSelfUsage: vi.fn(), useSelfUsageSavings: vi.fn() }));

const selfGolden = usageGoldens.self as UsageSelf;
const serve = (query: { data?: unknown; error?: unknown }) => vi.mocked(useSelfUsage).mockReturnValue({ data: undefined, error: null, ...query } as ReturnType<typeof useSelfUsage>);
const spend = () => within(screen.getByRole("region", { name: "Cost Versus Current Budget" }));
const monthBudgets = () =>
    within(screen.getByRole("region", { name: "Last Three Months" }))
        .getAllByRole("row")
        .slice(1)
        .map(row => within(row).getAllByRole("cell").at(-1)?.textContent);

const serveSavings = (savings: Partial<UsageSavings>) => {
    vi.mocked(useSelfUsageSavings).mockReturnValue({ data: { ...usageGoldens.savings, scope: "self", ...savings } } as ReturnType<typeof useSelfUsageSavings>);
};

beforeEach(() => {
    vi.mocked(useSelfUsage).mockReset();
    vi.mocked(useSelfUsageSavings).mockReturnValue({ data: undefined } as ReturnType<typeof useSelfUsageSavings>);
});

describe("UsageSettings", () => {
    test("the backend's golden for a caller known only by their sign-in subject: the default user budget, even with nothing spent", () => {
        serve({ data: usageGoldens.selfUnattributed });
        render(<UsageSettings />);
        expect(screen.getByRole("region", { name: "Cost Versus Current Budget" })).toHaveTextContent("0.0% of $100 current budget this month");
        expect(monthBudgets()).toEqual(["Not recorded", "Not recorded", "Current budget $100"]);
    });

    test.each([
        { budget: null, label: "No budget applies to you" },
        { budget: usageBudget({ amount: null, spend: "12.50" }), label: "Your budget has no limit" },
    ])("with no limited budget, says '$label' and Smart Context's savings read as the cost without them", ({ budget, label }) => {
        serve({ data: { ...usageGoldens.selfUnattributed, budget, totals: { ...usageGoldens.selfUnattributed.totals, cost: "12.5000000000" } } });
        serveSavings({ avoidedCost: "4.2500000000" });
        render(<UsageSettings />);
        expect(spend().getByText(label)).toBeInTheDocument();
        const note = within(screen.getByTestId("smartContextSavingsNote"));
        expect(note.getByText("Smart Context saved an estimated $4.25 this month.")).toBeInTheDocument();
        expect(note.getByText("Without Smart Context, this month's cost would be $16.75.")).toBeInTheDocument();
    });

    test("a saving too small to move the budget's percentage reads as the cost without it, never as '91.6% instead of 91.6%'", () => {
        serve({ data: selfGolden });
        serveSavings({ avoidedCost: "0.2000000000" });
        render(<UsageSettings />);
        const note = within(screen.getByTestId("smartContextSavingsNote"));
        expect(note.getByText("Smart Context saved an estimated $0.20 this month.")).toBeInTheDocument();
        expect(note.getByText("Without Smart Context, this month's cost would be $412.50.")).toBeInTheDocument();
        expect(note.queryByText(/instead of/)).not.toBeInTheDocument();
    });

    test("savings from an older rollup pass than the usage keep the saving but not the cost without it", () => {
        serve({ data: selfGolden });
        serveSavings({ dataAsOf: "2026-10-15T19:14:00.000Z" });
        render(<UsageSettings />);
        const note = within(screen.getByTestId("smartContextSavingsNote"));
        expect(note.getByText("Smart Context saved an estimated $5.25 this month.")).toBeInTheDocument();
        expect(note.queryByText(/Without Smart Context/)).not.toBeInTheDocument();
    });

    test("with every model unpriced, or a saving under a cent, the savings are tokens alone; with nothing saved or nothing measured, no note", () => {
        serve({ data: selfGolden });
        serveSavings(usageGoldens.savingsUnpriced);
        const { rerender } = render(<UsageSettings />);
        expect(screen.getByTestId("smartContextSavingsNote")).toHaveTextContent(/^Smart Context kept an estimated 250K input tokens out of LLM requests this month\.$/);

        serveSavings({ avoidedInputTokens: 1_000, avoidedCost: "0.0001500000" });
        rerender(<UsageSettings />);
        expect(screen.getByTestId("smartContextSavingsNote")).toHaveTextContent(/^Smart Context kept an estimated 1K input tokens out of LLM requests this month\.$/);

        serveSavings({ avoidedInputTokens: 0, avoidedCost: "0.0000000000" });
        rerender(<UsageSettings />);
        expect(screen.queryByTestId("smartContextSavingsNote")).not.toBeInTheDocument();

        serveSavings(usageGoldens.savingsBuilding);
        rerender(<UsageSettings />);
        expect(screen.queryByTestId("smartContextSavingsNote")).not.toBeInTheDocument();
    });

    test("a failed load says so with the server's message; a failed refetch keeps the figures on screen", () => {
        const error = Object.assign(new Error("Service unavailable"), { status: 500 });
        serve({ error });
        const { rerender } = render(<UsageSettings />);
        expect(screen.getByText(/Couldn't load your usage/)).toBeInTheDocument();

        serve({ data: selfGolden, error });
        rerender(<UsageSettings />);
        expect(spend().getByText("$412.30")).toBeInTheDocument();
        expect(within(screen.getByRole("region", { name: "Last Three Months" })).getAllByRole("row")[1]).toHaveTextContent(/^Aug \(kept from Aug 12\)/);
        expect(screen.queryByText(/Couldn't load your usage/)).not.toBeInTheDocument();
    });
});
