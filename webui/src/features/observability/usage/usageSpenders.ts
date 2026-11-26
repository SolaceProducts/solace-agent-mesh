// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageRow } from "@/api/usage";

import { formatUsageMoney, formatUsagePercent, isUsageMoney, isZeroUsageMoney, remainderUsageMoney } from "@/utils/usageMoney";

import { rowKey, subjectKey } from "./usageKinds";

export const SPENDER_COLORS = ["var(--accent-n0-wMain)", "var(--accent-n2-wMain)", "var(--accent-n4-wMain)", "var(--accent-n3-wMain)", "var(--accent-n5-wMain)"] as const;
export const OTHER_COLOR = "var(--secondary-w40)";
const LOWEST_ONLY_COLORS = ["var(--accent-n1-wMain)", "var(--accent-n6-wMain)", "var(--accent-n7-wMain)", "var(--accent-n9-w100)", "var(--accent-n2-w100)"] as const;

export type SpenderColorFor = (row: UsageRow, index: number) => string;

export function spenderColors(top: readonly UsageRow[], lowest: readonly UsageRow[]): SpenderColorFor {
    const colors = new Map<string, string>();
    top.forEach((row, index) => colors.set(rowKey(row, index), SPENDER_COLORS[index % SPENDER_COLORS.length]));
    let spare = 0;
    lowest.forEach((row, index) => {
        const key = rowKey(row, index);
        if (colors.has(key)) return;
        colors.set(key, LOWEST_ONLY_COLORS[spare % LOWEST_ONLY_COLORS.length]);
        spare++;
    });
    return (row, index) => colors.get(rowKey(row, index)) ?? SPENDER_COLORS[index % SPENDER_COLORS.length];
}

export interface SpenderOther {
    cost: string;
    share: number;
}

function shownRows(rows: readonly UsageRow[], alsoShown: readonly UsageRow[]): UsageRow[] {
    const listed = new Set(rows.map(subjectKey));
    const listsKeyless = listed.has(null);
    return [
        ...rows,
        ...alsoShown.filter(row => {
            const key = subjectKey(row);
            return key === null ? !listsKeyless : !listed.has(key);
        }),
    ];
}

export function otherSpend(rows: readonly UsageRow[], kindCost: string, alsoShown: readonly UsageRow[] = []): SpenderOther | null {
    const shown = shownRows(rows, alsoShown);
    if (![kindCost, ...shown.map(row => row.cost)].every(isUsageMoney)) return null;
    const cost = remainderUsageMoney(
        kindCost,
        shown.map(row => row.cost)
    );
    if (isZeroUsageMoney(cost)) return null;
    const shownShare = shown.reduce((sum, row) => sum + (row.shareOfSpend ?? 0), 0);
    return { cost, share: Math.max(0, 100 - shownShare) };
}

export function outsideSpend(totalCost: string, kindCost: string): string | null {
    if (!isUsageMoney(totalCost) || !isUsageMoney(kindCost)) return null;
    const cost = remainderUsageMoney(totalCost, [kindCost]);
    if (isZeroUsageMoney(cost)) return null;
    return cost;
}

export function hasBreakdownFigures(listCost: string, totalCost: string): boolean {
    return isUsageMoney(listCost) && isUsageMoney(totalCost);
}

export function formatSliceDetail(cost: string, share: number | null, of: string): string {
    return share === null ? formatUsageMoney(cost) : `${formatUsageMoney(cost)} · ${formatUsagePercent(share)} of ${of}`;
}
