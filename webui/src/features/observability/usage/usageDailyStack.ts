// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageDay, UsageMoney } from "@/api/usage";

import { isUsageMoney, remainderUsageMoney } from "@/utils/usageMoney";

export interface StackSeries {
    key: string;
    daily: readonly UsageDay[];
}

export interface StackDay {
    date: string;
    costs: Record<string, string>;
    other: string;
    total: string;
}

const ZERO: UsageMoney = "0.0000000000";

export function stackDailyCost(total: readonly UsageDay[], series: readonly StackSeries[]): StackDay[] | null {
    const amounts = [...total, ...series.flatMap(({ daily }) => daily)].map(day => day.cost);
    if (!amounts.every(isUsageMoney)) return null;
    const byKey = series.map(({ key, daily }) => ({ key, costs: new Map(daily.map(day => [day.date, day.cost])) }));
    return total.map(day => {
        const costs = Object.fromEntries(byKey.map(({ key, costs: daily }) => [key, daily.get(day.date) ?? ZERO]));
        return { date: day.date, costs, other: remainderUsageMoney(day.cost, Object.values(costs)), total: day.cost };
    });
}
