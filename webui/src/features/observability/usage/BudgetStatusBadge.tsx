// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { TriangleAlert } from "lucide-react";

import type { UsageBudget } from "@/api/usage";

import { Badge } from "@/ui/badge";

import { formatBudgetMoney, formatUsagePercent } from "@/utils";

interface BudgetStatusBadgeProps {
    budget: UsageBudget | null;
    isThisMonth: boolean;
}

const STANDING = {
    approaching: { type: "warning", label: "Approaching budget" },
    exceeded: { type: "error", label: "Over budget" },
} as const;

function standingTooltip(label: string, budget: UsageBudget): string {
    if (budget.usagePercent === null || budget.effectiveAmount === null) return label;
    return `${label} · ${formatUsagePercent(budget.usagePercent)} of ${formatBudgetMoney(budget.effectiveAmount)} current budget`;
}

export function BudgetStatusBadge({ budget, isThisMonth }: Readonly<BudgetStatusBadgeProps>) {
    if (!isThisMonth || !budget || (budget.status !== "approaching" && budget.status !== "exceeded")) return null;
    const { type, label } = STANDING[budget.status];
    return (
        <Badge type={type} className="max-w-full min-w-0 shrink-[100]" tooltip={standingTooltip(label, budget)}>
            <TriangleAlert aria-hidden className="shrink-0" />
            <span className="truncate">{label}</span>
        </Badge>
    );
}
