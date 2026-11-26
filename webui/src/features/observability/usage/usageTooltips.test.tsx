// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { NO_TOKENS } from "@/utils";

import { DailyByModelTooltip } from "./ModelDailyCostCard";
import { DailyCostTooltip, WeeklyCostTooltip } from "./TokenCostChart";
import { TrendTooltip } from "./UsageTrendChart";

const TODAY = "2026-10-16";
const day = { date: "2026-10-03", cost: "12.3400000000", tokens: { uncachedInput: 2_200_000, output: 835_000, cacheRead: 1_312_000, cacheWrite: 148_000 }, activities: 1234 };
const at = (payload: unknown) => [{ payload }];

describe("chart tooltips", () => {
    test("show nothing for a day still to come, which has a place on the axis and no figures", () => {
        const future = "2026-10-31";
        const { container } = render(
            <>
                <DailyCostTooltip active payload={at({ key: future })} today={TODAY} />
                <TrendTooltip active payload={at({ key: future, value: null })} today={TODAY} measure="cost" />
                <DailyByModelTooltip active payload={at({ date: future })} series={[]} days={new Map()} today={TODAY} />
            </>
        );
        expect(container).toBeEmptyDOMElement();
    });

    test("the cost tooltips show nothing for a bar of the other kind, which recharts can hand over as the chart switches between days and weeks", () => {
        const week = { start: "2026-09-28", firstDay: "2026-10-01", lastDay: "2026-10-04", days: 4, cost: "40.0000000000", tokens: NO_TOKENS, activities: 2000 } as const;
        const { container } = render(
            <>
                <WeeklyCostTooltip active payload={at({ key: day.date, cost: 12.34, day })} today={TODAY} />
                <DailyCostTooltip active payload={at({ key: week.firstDay, cost: 40, week })} today={TODAY} />
            </>
        );
        expect(container).toBeEmptyDOMElement();
    });

    test("the daily cost tooltip names the day, its cost, its tokens and what they were used for", () => {
        const { container } = render(<DailyCostTooltip active payload={at({ date: day.date, cost: 12.34, day })} today={TODAY} />);
        expect(screen.getByText("Sat, Oct 3")).toBeInTheDocument();
        expect(container).toHaveTextContent("$12.34 token cost");
        expect(container).toHaveTextContent("4.5M tokens");
        expect(container).toHaveTextContent("Input 2.2M • Output 835K • Cache read 1.3M • Cache write 148K");
    });
});
