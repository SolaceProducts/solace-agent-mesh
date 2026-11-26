// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageSubjectTotals } from "@/api/usage";

import { formatCostPerActivity, formatUsageMoney, formatUsagePercent } from "@/utils";

import { formatCount } from "../feedbackFormat";

function Kpi({ label, value, sub }: Readonly<{ label: string; value: string; sub?: string }>) {
    return (
        <div className="flex flex-col gap-1 rounded-sm border p-4">
            <dt className="text-sm font-medium text-(--secondary-text-wMain)">{label}</dt>
            <dd className="text-2xl font-semibold tabular-nums">{value}</dd>
            {sub && <dd className="text-xs text-(--secondary-text-wMain)">{sub}</dd>}
        </div>
    );
}

export function SubjectKpiRow({ totals }: Readonly<{ totals: UsageSubjectTotals }>) {
    return (
        <dl className="grid gap-4 @md:grid-cols-3">
            <Kpi label="Total Cost" value={formatUsageMoney(totals.cost)} sub={totals.shareOfSpend === null ? undefined : `${formatUsagePercent(totals.shareOfSpend)} of total cost`} />
            <Kpi label="Activities" value={formatCount(totals.activities)} />
            <Kpi label="Avg Cost / Activity" value={formatCostPerActivity(totals.costPerActivity)} />
        </dl>
    );
}
