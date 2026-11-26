// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { usageBudget } from "@/testing/data/usage";

import { SpendVsBudget } from "./SpendVsBudget";

describe("SpendVsBudget, this month's standing", () => {
    test("states the share of the current budget, the amount carrying its top-up", () => {
        render(<SpendVsBudget spend="96.4000000000" budget={usageBudget({ amount: "200", spend: "96.40", topUp: "50" })} noBudgetLabel="No budget applies" />);
        expect(screen.getByText("$96.40")).toBeInTheDocument();
        expect(screen.getByText("$250").closest("p")).toHaveTextContent(/^38\.5% of \$250 \(\$200 \+ \$50 top-up until .+\) current budget this month$/);
    });

    test.each([
        ["an unlimited budget", usageBudget({ amount: null, spend: "9.80" }), "No budget limit"],
        ["no budget", null, "No budget applies"],
    ])("says so for %s, in either layout", (_what, budget, expected) => {
        const { rerender } = render(<SpendVsBudget spend="9.8000000000" budget={budget} noBudgetLabel="No budget applies" />);
        expect(screen.getByText(expected)).toBeInTheDocument();
        rerender(<SpendVsBudget spend="9.8000000000" budget={budget} noBudgetLabel="No budget applies" layout="inline" />);
        expect(screen.getByText(expected)).toBeInTheDocument();
    });
});
