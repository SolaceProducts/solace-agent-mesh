// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { Bot } from "lucide-react";

import type { UsageUserDetail } from "@/api/usage";

import { formatUsageMoney, formatUsagePercent, isZeroUsageMoney, usageMoneyToNumber } from "@/utils";

import { type DonutSlice, UsageDonut } from "./UsageDonut";
import { ROOT_KIND_ICONS, ROOT_KIND_LABELS } from "./usageIcons";
import { formatSliceDetail, OTHER_COLOR, SPENDER_COLORS } from "./usageSpenders";

const USER_SPEND = "their spend";

export function UserRootsCard({ detail }: Readonly<{ detail: UsageUserDetail }>) {
    const hasOther = !isZeroUsageMoney(detail.otherCost);
    const slices = useMemo<DonutSlice[]>(() => {
        const ranked = detail.topRoots.map((root, index) => ({
            key: `${root.kind}:${root.id ?? root.name}`,
            value: usageMoneyToNumber(root.cost),
            color: SPENDER_COLORS[index % SPENDER_COLORS.length],
            label: root.name,
            detail: formatSliceDetail(root.cost, root.shareOfUser, USER_SPEND),
        }));
        const other = { key: "other", value: usageMoneyToNumber(detail.otherCost), color: OTHER_COLOR, label: "Other", detail: formatSliceDetail(detail.otherCost, detail.otherShareOfUser, USER_SPEND) };
        return hasOther ? [...ranked, other] : ranked;
    }, [detail, hasOther]);

    return (
        <section aria-label="Top Agents & Workflows" className="flex flex-col gap-3 rounded-sm border p-4">
            <h3 className="text-sm font-semibold">Top Agents & Workflows</h3>
            {detail.topRoots.length === 0 && !hasOther ? (
                <p className="text-sm text-(--secondary-text-wMain)">No usage in this period</p>
            ) : (
                <div className="flex flex-col items-center gap-4 @lg:flex-row @lg:items-start">
                    <UsageDonut slices={slices} size={96} />
                    <ol className="flex w-full min-w-0 flex-1 flex-col text-sm">
                        {detail.topRoots.map((root, index) => {
                            const Icon = ROOT_KIND_ICONS[root.kind] ?? Bot;
                            return (
                                <li key={`${root.kind}:${root.id ?? root.name}`} className="flex items-center gap-2 border-b py-1.5 last:border-b-0">
                                    <span className="w-4 shrink-0 text-right text-xs text-(--secondary-text-wMain)">{index + 1}</span>
                                    <Icon className="size-4 shrink-0 text-(--secondary-text-wMain)" aria-hidden />
                                    <span className="flex min-w-0 flex-1 items-baseline gap-2">
                                        <span className="truncate" title={root.name}>
                                            {root.name}
                                        </span>
                                        <span className="shrink-0 text-xs text-(--secondary-text-wMain)">{ROOT_KIND_LABELS[root.kind] ?? root.kind}</span>
                                    </span>
                                    <span className="w-20 shrink-0 text-right font-medium tabular-nums">{formatUsageMoney(root.cost)}</span>
                                    <span className="w-14 shrink-0 text-right text-xs text-(--secondary-text-wMain) tabular-nums">{formatUsagePercent(root.shareOfUser)}</span>
                                </li>
                            );
                        })}
                        {hasOther && (
                            <li className="flex items-center gap-2 py-1.5">
                                <span className="w-4 shrink-0" />
                                <span className="size-2.5 shrink-0 rounded-xs" style={{ backgroundColor: OTHER_COLOR }} aria-hidden />
                                <span className="min-w-0 flex-1 text-(--secondary-text-wMain)">Other</span>
                                <span className="w-20 shrink-0 text-right font-medium tabular-nums">{formatUsageMoney(detail.otherCost)}</span>
                                <span className="w-14 shrink-0 text-right text-xs text-(--secondary-text-wMain) tabular-nums">{formatUsagePercent(detail.otherShareOfUser)}</span>
                            </li>
                        )}
                    </ol>
                </div>
            )}
        </section>
    );
}
