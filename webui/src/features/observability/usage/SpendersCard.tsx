// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import type { UsageKind, UsageRow, UsageUserRow } from "@/api/usage";

import { usageLoadErrorMessage } from "@/common/usage";

import { Button } from "@/ui/button";
import { Skeleton } from "@/ui/skeleton";

import { MessageBanner } from "@/common";
import { cn, formatCostPerActivity, formatUsageMoney, formatUsagePercent, usageMoneyToNumber } from "@/utils";

import { BudgetStatusBadge } from "./BudgetStatusBadge";
import { SubjectName } from "./SubjectName";
import { detailTargetFor, serializeDetailTarget, type UsageDetailTarget } from "./usageDetailTarget";
import { type DonutSlice, UsageDonut } from "./UsageDonut";
import { displayName, KIND_CONFIG, rowKey, SPENDERS_CARD_MIN_HEIGHT } from "./usageKinds";
import { formatSliceDetail, OTHER_COLOR, otherSpend, type SpenderColorFor } from "./usageSpenders";

const TOTAL_SPEND = "total spend";

interface SpendersCardProps {
    title: string;
    kind: UsageKind;
    rows?: readonly UsageRow[];
    kindCost?: string;
    isStale: boolean;
    isThisMonth: boolean;
    isLoading: boolean;
    error: Error | null;
    colorFor: SpenderColorFor;
    selected?: UsageDetailTarget | null;
    onOpen?: (target: UsageDetailTarget) => void;
    alsoShown?: readonly UsageRow[] | null;
}

const SPENDER_ROW_CLASS = "flex items-center gap-4 px-2 py-1.5";

function secondaryFigure(kind: UsageKind, row: UsageRow): string {
    if (kind === "users") return row.shareOfSpend === null ? "—" : formatUsagePercent(row.shareOfSpend);
    return formatCostPerActivity(row.costPerActivity);
}

function SpenderRowContent({ kind, row, rank, color, isThisMonth }: Readonly<{ kind: UsageKind; row: UsageRow; rank: number; color: string; isThisMonth: boolean }>) {
    return (
        <>
            <span className="w-4 shrink-0 text-right text-xs text-(--secondary-text-wMain)">{rank}</span>
            <span className="size-2.5 shrink-0 rounded-xs" style={{ backgroundColor: color }} aria-hidden />
            <span className="flex min-w-0 flex-1 items-center gap-2 overflow-hidden">
                <SubjectName kind={kind} row={row} className="shrink" />
                {kind === "users" && <BudgetStatusBadge budget={(row as UsageUserRow).budget} isThisMonth={isThisMonth} />}
            </span>
            <span className="shrink-0 font-medium tabular-nums">{formatUsageMoney(row.cost)}</span>
            <span className="w-14 shrink-0 text-right text-xs text-(--secondary-text-wMain) tabular-nums">{secondaryFigure(kind, row)}</span>
        </>
    );
}

export function SpendersCard({ title, kind, rows, kindCost, isStale, isThisMonth, isLoading, error, colorFor, selected = null, onOpen, alsoShown }: Readonly<SpendersCardProps>) {
    const listed = useMemo(() => rows ?? [], [rows]);
    const ringRest = useMemo(() => (kindCost && !isStale ? otherSpend(listed, kindCost) : null), [listed, kindCost, isStale]);
    const other = useMemo(() => {
        if (alsoShown === undefined) return ringRest;
        return kindCost && !isStale && alsoShown ? otherSpend(listed, kindCost, alsoShown) : null;
    }, [listed, kindCost, isStale, alsoShown, ringRest]);
    const ringRestLabel = alsoShown === undefined ? "Other" : `All other ${KIND_CONFIG[kind].plural.toLowerCase()}`;
    const slices = useMemo<DonutSlice[]>(() => {
        const ranked = listed.map((row, index) => ({
            key: rowKey(row, index),
            value: usageMoneyToNumber(row.cost),
            color: colorFor(row, index),
            label: displayName(kind, row),
            detail: formatSliceDetail(row.cost, row.shareOfSpend, TOTAL_SPEND),
        }));
        return ringRest ? [...ranked, { key: "other", value: usageMoneyToNumber(ringRest.cost), color: OTHER_COLOR, label: ringRestLabel, detail: formatSliceDetail(ringRest.cost, kind === "users" ? ringRest.share : null, TOTAL_SPEND) }] : ranked;
    }, [kind, listed, ringRest, ringRestLabel, colorFor]);
    const figureHeading = kind === "users" ? "share of total spend" : "cost / activity";

    let body;
    if (error && !rows) {
        body = <MessageBanner variant="error" message={usageLoadErrorMessage("Couldn't load spenders.", error)} />;
    } else if (isLoading) {
        body = <Skeleton className="h-28" />;
    } else if (listed.length === 0) {
        body = (
            <div className="flex items-center gap-4">
                <UsageDonut slices={[]} />
                <p className="text-sm text-(--secondary-text-wMain)">No usage in this period</p>
            </div>
        );
    } else {
        body = (
            <div className={cn("flex items-start gap-4 transition-opacity", isStale && "opacity-60")} aria-busy={isStale}>
                <UsageDonut slices={kindCost ? slices : []} />
                <ol className="flex min-w-0 flex-1 flex-col text-sm">
                    {listed.map((row, index) => {
                        const target = detailTargetFor(kind, row);
                        const content = <SpenderRowContent kind={kind} row={row} rank={index + 1} color={colorFor(row, index)} isThisMonth={isThisMonth} />;
                        if (!target || !onOpen) {
                            return (
                                <li key={rowKey(row, index)} className={cn(SPENDER_ROW_CLASS, "border-b last:border-b-0")}>
                                    {content}
                                </li>
                            );
                        }
                        const isSelected = selected !== null && serializeDetailTarget(target) === serializeDetailTarget(selected);
                        return (
                            <li key={rowKey(row, index)} className="border-b last:border-b-0">
                                <Button
                                    variant="ghost"
                                    data-state={isSelected ? "selected" : undefined}
                                    className={cn(
                                        SPENDER_ROW_CLASS,
                                        "h-auto w-full justify-start rounded-none text-left font-normal text-(--primary-text-wMain) enabled:hover:bg-(--secondary-w10)/50 enabled:hover:text-(--primary-text-wMain) data-[state=selected]:bg-(--secondary-w10)"
                                    )}
                                    onClick={() => onOpen(target)}
                                >
                                    {content}
                                </Button>
                            </li>
                        );
                    })}
                    {other && (
                        <li className={SPENDER_ROW_CLASS}>
                            <span className="w-4 shrink-0" />
                            <span className="size-2.5 shrink-0 rounded-xs" style={{ backgroundColor: OTHER_COLOR }} aria-hidden />
                            <span className="min-w-0 flex-1 text-(--secondary-text-wMain)">Other</span>
                            <span className="shrink-0 font-medium tabular-nums">{formatUsageMoney(other.cost)}</span>
                            <span className="w-14 shrink-0 text-right text-xs text-(--secondary-text-wMain) tabular-nums">{kind === "users" ? formatUsagePercent(other.share) : ""}</span>
                        </li>
                    )}
                </ol>
            </div>
        );
    }

    return (
        <section aria-label={title} className="flex min-w-0 flex-col gap-3 rounded-sm border p-4" style={{ minHeight: SPENDERS_CARD_MIN_HEIGHT }}>
            <header className="flex items-baseline justify-between gap-2">
                <h3 className="text-sm font-semibold">{title}</h3>
                <span className="text-xs text-(--secondary-text-wMain)">{figureHeading}</span>
            </header>
            {body}
        </section>
    );
}
