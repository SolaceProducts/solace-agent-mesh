// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageMoney } from "@/api/usage";

import { isUsageMoney, parseUsageMoney, sumUsageMoney } from "@/utils";

const TENTHS_OF_A_PERCENT = 1000n;
const HALF_A_CENT = 50_000_000n;

export function isSavingAtLeastACent(avoidedCost: string): boolean {
    return isUsageMoney(avoidedCost) && parseUsageMoney(avoidedCost) >= HALF_A_CENT;
}

export function costWithoutSavings(cost: string, avoidedCost: string): UsageMoney | null {
    return isUsageMoney(cost) && isUsageMoney(avoidedCost) ? sumUsageMoney([cost, avoidedCost]) : null;
}

export function percentOfBudget(spend: string, budgetAmount: string | null): number | null {
    if (budgetAmount === null || !isUsageMoney(budgetAmount) || !isUsageMoney(spend)) return null;
    const budget = parseUsageMoney(budgetAmount);
    if (budget <= 0n) return null;
    return Number((parseUsageMoney(spend) * TENTHS_OF_A_PERCENT) / budget) / 10;
}
