// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { budgetLineSegments, formatAxisDollars } from "./usageChartFormat";

describe("formatAxisDollars", () => {
    test("keeps axis ticks short", () => {
        expect(formatAxisDollars(80)).toBe("$80");
        expect(formatAxisDollars(1.2)).toBe("$1.20");
        expect(formatAxisDollars(2400)).toBe("$2.4k");
        expect(formatAxisDollars(24000)).toBe("$24k");
    });
});

describe("budgetLineSegments", () => {
    test("draws the current month's band strong and the earlier months' faint", () => {
        expect(budgetLineSegments(0, 400, 4, 3)).toEqual([
            { x1: 0, x2: 300, current: false },
            { x1: 300, x2: 400, current: true },
        ]);
    });

    test("keeps a faint segment on each side of a current month in the middle", () => {
        expect(budgetLineSegments(10, 310, 3, 1)).toEqual([
            { x1: 10, x2: 110, current: false },
            { x1: 110, x2: 210, current: true },
            { x1: 210, x2: 310, current: false },
        ]);
    });

    test("draws the whole line faint when the current month isn't charted", () => {
        expect(budgetLineSegments(0, 300, 3, -1)).toEqual([{ x1: 0, x2: 300, current: false }]);
    });
});
