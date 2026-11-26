// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const PARTIAL_PERIOD_OPACITY = 0.35;

const MONTH_LABEL = new Intl.DateTimeFormat("en-US", { month: "short", timeZone: "UTC" });

export function formatUsageMonth(month: string): string {
    return MONTH_LABEL.format(new Date(`${month}-01T00:00:00.000Z`));
}

export function formatAxisDollars(value: number): string {
    if (value >= 1000) return `$${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)}k`;
    return `$${value % 1 === 0 ? value : value.toFixed(2)}`;
}

export interface BudgetLineSegment {
    x1: number;
    x2: number;
    current: boolean;
}

export function budgetLineSegments(x1: number, x2: number, monthCount: number, currentIndex: number): BudgetLineSegment[] {
    if (currentIndex < 0 || currentIndex >= monthCount) return [{ x1, x2, current: false }];
    const band = (x2 - x1) / monthCount;
    const start = x1 + band * currentIndex;
    const end = start + band;
    const segments = [
        { x1, x2: start, current: false },
        { x1: start, x2: end, current: true },
        { x1: end, x2, current: false },
    ];
    return segments.filter(segment => segment.x2 > segment.x1);
}
