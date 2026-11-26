// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useUsageSavings } from "@/api/usage";

import { usageGoldens, usageSummary } from "@/testing/data/usage";

import { UsageOverview } from "./UsageOverview";

vi.mock("@/api/usage", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/usage")>()), useUsageSavings: vi.fn() }));

const serveSavings = (data: unknown, query: { isPlaceholderData?: boolean } = {}) => vi.mocked(useUsageSavings).mockReturnValue({ data, error: null, isPlaceholderData: false, ...query } as ReturnType<typeof useUsageSavings>);
const savingsCard = () => screen.queryByRole("region", { name: "Smart Context Savings" });
const currentSavings = { ...usageGoldens.savings, dataAsOf: usageSummary.dataAsOf };
const WOULD_HAVE_COST = /Without Smart Context, the cost would have been/;

function renderOverview({ isLoading = false, isStale = false } = {}) {
    return render(
        <UsageOverview
            summary={isLoading ? undefined : usageSummary}
            windowParams={{ period: "this_month" }}
            isLoading={isLoading}
            isStale={isStale}
            error={null}
            comparisonLabel="vs. same point last month"
            isThisMonth
            periodPicker={null}
            today="2026-10-16"
        />
    );
}

beforeEach(() => {
    vi.mocked(useUsageSavings).mockReset();
});

describe("UsageOverview", () => {
    test("leaves the card out when the platform does not serve the savings routes", () => {
        serveSavings(null);
        renderOverview();
        expect(screen.getByTestId("usageTotalSpend")).toBeInTheDocument();
        expect(savingsCard()).not.toBeInTheDocument();
    });

    test("shows the card beside the total, with what the period would have cost", () => {
        serveSavings(currentSavings);
        renderOverview();
        expect(savingsCard()).toHaveTextContent("$5.25");
        expect(savingsCard()).toHaveTextContent("Without Smart Context, the cost would have been $1,245.83.");
    });

    test("a period with no measured call reads as not measured rather than as nothing saved", () => {
        serveSavings(usageGoldens.savingsBuilding);
        renderOverview();
        expect(savingsCard()).toHaveTextContent("Smart Context savings were not measured in this period.");
        expect(savingsCard()).not.toHaveTextContent("$0.00");
    });

    test("savings only from unpriced calls, in a period with priced ones, read as tokens rather than $0.00", () => {
        serveSavings({ ...usageGoldens.savings, avoidedInputTokens: 18_400, avoidedCost: "0.0000000000" });
        renderOverview();
        expect(savingsCard()).toHaveTextContent("18K input tokens");
        expect(savingsCard()).toHaveTextContent("These savings have no cost estimate because they came from models with no price set or with a price of $0.");
        expect(savingsCard()).not.toHaveTextContent("$0.00");
    });

    test("a priced figure beside calls recorded without a price says which tokens the cost leaves out", () => {
        serveSavings({ ...currentSavings, calls: 1_200, unpricedCalls: 20 });
        renderOverview();
        expect(savingsCard()).toHaveTextContent("$5.25");
        expect(savingsCard()).toHaveTextContent("20 of 1,200 calls used a model with no price set, so the cost leaves out any tokens those calls avoided.");
    });

    test("one unpriced call keeps the total and reads in the singular", () => {
        serveSavings({ ...currentSavings, calls: 1_200, unpricedCalls: 1 });
        renderOverview();
        expect(savingsCard()).toHaveTextContent("1 of 1,200 calls used a model with no price set, so the cost leaves out any tokens that call avoided.");
    });

    test("a priced figure with every call priced carries no note about unpriced calls", () => {
        serveSavings({ ...currentSavings, unpricedCalls: 0 });
        renderOverview();
        expect(savingsCard()).toHaveTextContent("$5.25");
        expect(savingsCard()).not.toHaveTextContent("no price set");
    });
});

describe("UsageOverview keeps the 'would have cost' sum off while the two reports may not cover the same calls", () => {
    test("the savings still hold the previous period's figures", () => {
        serveSavings(currentSavings, { isPlaceholderData: true });
        renderOverview();
        expect(savingsCard()).toHaveTextContent("$5.25");
        expect(savingsCard()).toHaveAttribute("aria-busy", "true");
        expect(savingsCard()).not.toHaveTextContent(WOULD_HAVE_COST);
    });

    test("the summary still holds the previous period's figures", () => {
        serveSavings(currentSavings);
        renderOverview({ isStale: true });
        expect(savingsCard()).toHaveTextContent("$5.25");
        expect(savingsCard()).not.toHaveTextContent(WOULD_HAVE_COST);
    });

    test("the savings come from an older rollup pass than the summary, as after a failed refetch of the savings alone", () => {
        serveSavings({ ...usageGoldens.savings, dataAsOf: "2026-10-16T13:58:00.000Z" });
        renderOverview();
        expect(savingsCard()).toHaveTextContent("$5.25");
        expect(savingsCard()).not.toHaveTextContent(WOULD_HAVE_COST);
    });
});
