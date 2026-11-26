// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageBudget } from "@/api/usage";

import { formatTopUpExpiry } from "@/utils";

import { HintedText } from "./HintedText";
import { formatCurrentBudget, formatTopUpBreakdown } from "./usageBudget";

export function CurrentBudgetAmount({ budget, className }: Readonly<{ budget: UsageBudget; className?: string }>) {
    const amount = formatCurrentBudget(budget);
    const breakdown = formatTopUpBreakdown(budget);
    if (!breakdown || !budget.topUp) return <span className={className}>{amount}</span>;
    return (
        <HintedText hint={`${breakdown} until ${formatTopUpExpiry(budget.topUp.expiresTime)}`} className={className}>
            {amount}
        </HintedText>
    );
}
