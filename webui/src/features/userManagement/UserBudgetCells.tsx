// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CornerDownRight } from "lucide-react";

import type { AppliedBudget, AppliedTopUp } from "@/api/budgets";

import { Badge } from "@/ui/badge";

import { formatBudgetLimit, formatTopUpExpiry, formatUsd } from "@/utils";

type UserBudget = AppliedBudget | null | undefined;

const UNKNOWN = "—";
const VIA_DEFAULT_TOOLTIP = "Assigned through the default user budget";

function limitTooltip(budget: AppliedBudget, topUp: AppliedTopUp | null | undefined, effectiveAmount: string | null | undefined): string {
    const amount = effectiveAmount === undefined ? budget.amount : effectiveAmount;
    const limit = amount === null ? formatBudgetLimit(amount) : `${formatUsd(amount)} (USD)`;
    const raised = topUp && budget.amount !== null ? ", including the additional amount" : "";
    const line = `Limit: ${limit}${raised}`;
    return budget.source === "default" ? `${VIA_DEFAULT_TOOLTIP}. ${line}` : line;
}

export function UserBudgetCell({ budget, topUp, effectiveAmount }: Readonly<{ budget: UserBudget; topUp?: AppliedTopUp | null; effectiveAmount?: string | null }>) {
    if (!budget) return UNKNOWN;
    const viaDefault = budget.source === "default";
    const tooltip = limitTooltip(budget, topUp, effectiveAmount);
    return (
        <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <Badge className="max-w-full min-w-0" tooltip={tooltip}>
                {viaDefault && <CornerDownRight aria-hidden className="text-(--secondary-text-wMain)" />}
                <span className="truncate">{budget.name}</span> <span className="sr-only">{tooltip}</span>
            </Badge>
            {topUp && <TopUpBadge topUp={topUp} budget={budget} />}
        </div>
    );
}

function TopUpBadge({ topUp, budget }: Readonly<{ topUp: AppliedTopUp; budget: AppliedBudget }>) {
    const until = formatTopUpExpiry(topUp.expiresTime);
    if (budget.amount === null) {
        return (
            <Badge variant="outline" className="shrink-0 border-dashed text-(--secondary-text-wMain)" tooltip={`Additional amount until ${until}. It has no effect while ${budget.name} is unlimited.`}>
                + {formatUsd(topUp.amount)}
            </Badge>
        );
    }
    return (
        <Badge className="shrink-0" tooltip={`Additional amount until ${until}`}>
            + {formatUsd(topUp.amount)}
        </Badge>
    );
}
