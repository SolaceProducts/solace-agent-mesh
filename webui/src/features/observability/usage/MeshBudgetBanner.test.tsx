// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { usageBudget } from "@/testing/data/usage";

import { MeshBudgetBanner } from "./MeshBudgetBanner";

const NOW = new Date("2026-10-16T14:00:00.000Z");

describe("MeshBudgetBanner", () => {
    test("warns once the mesh passes its alert threshold, against the budget with its top-up", () => {
        render(<MeshBudgetBanner budget={usageBudget({ amount: "1000", spend: "900", topUp: "100" })} now={NOW} />);
        const banner = screen.getByRole("alert");
        expect(banner).toHaveTextContent("Agent Mesh is at 81.8% of its monthly budget");
        expect(banner).toHaveTextContent("$900.00 of $1,100.00 spent with 16 days left in the period.");
    });

    test("says the budget is reached once spend meets it", () => {
        render(<MeshBudgetBanner budget={usageBudget({ amount: "1000", spend: "1000" })} now={NOW} />);
        const banner = screen.getByRole("alert");
        expect(banner).toHaveTextContent("Agent Mesh monthly budget reached");
        expect(banner).toHaveTextContent("$1,000.00 of $1,000.00 spent with 16 days left in the period.");
        expect(screen.queryByRole("button", { name: "Go to Budgets" })).not.toBeInTheDocument();
    });

    test("offers the Budgets page to a viewer who may open it", () => {
        const onGoToBudgets = vi.fn();
        render(<MeshBudgetBanner budget={usageBudget({ amount: "1000", spend: "1000" })} now={NOW} onGoToBudgets={onGoToBudgets} />);
        fireEvent.click(screen.getByRole("button", { name: "Go to Budgets" }));
        expect(onGoToBudgets).toHaveBeenCalledOnce();
    });

    test("says the mesh is over the budget once spend passes it, rather than reading spend as a part of it", () => {
        render(<MeshBudgetBanner budget={usageBudget({ amount: "1", spend: "90.23" })} now={NOW} />);
        expect(screen.getByRole("alert")).toHaveTextContent("$90.23 spent, over the $1.00 budget, with 16 days left in the period.");
    });

    test("says a $0 budget is overspent, though its usage percentage reads 100", () => {
        render(<MeshBudgetBanner budget={usageBudget({ amount: "0", spend: "5" })} now={NOW} />);
        expect(screen.getByRole("alert")).toHaveTextContent("$5.00 spent, over the $0.00 budget, with 16 days left in the period.");
    });

    test.each([
        ["below its alert threshold", usageBudget({ amount: "1000", spend: "500" })],
        ["unlimited", usageBudget({ amount: null, spend: "500" })],
    ])("says nothing for a mesh %s", (_what, budget) => {
        const { container } = render(<MeshBudgetBanner budget={budget} now={NOW} />);
        expect(container).toBeEmptyDOMElement();
    });
});
