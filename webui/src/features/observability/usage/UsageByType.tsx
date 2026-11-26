// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo } from "react";

import { type UsageAnySortField, type UsageCostByKind, type UsageDay, type UsageKind, type UsageWindow, type UsageWindowParams, useUsageList } from "@/api/usage";

import { HelpPopover } from "@/common/HelpPopover";
import { PaginationControls } from "@/common/layout/PaginationControls";
import { TableStateBoundary } from "@/common/status";
import { usageErrorDetail } from "@/common/usage";

import { SearchInput } from "@/ui/search-input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui/tabs";

import { useDebounce, usePageNumber } from "@/hooks";
import { useSortState } from "@/ui";
import { cn } from "@/utils";

import { CostBreakdownTable } from "./CostBreakdownTable";
import { CostBreakdownTooltip } from "./CostBreakdownTooltip";
import { ModelDailyCostCard } from "./ModelDailyCostCard";
import { SpendersCard } from "./SpendersCard";
import type { UsageDetailTarget } from "./usageDetailTarget";
import { isUsageKind, KIND_CONFIG, SPENDERS_SHOWN, TABLE_PAGE_SIZE, TOP_PAGE_SIZE, USAGE_KINDS } from "./usageKinds";
import { hasBreakdownFigures, spenderColors } from "./usageSpenders";

const SEARCH_DEBOUNCE_MS = 300;
const COST_FIRST = { sortKey: "cost", sortDir: "desc" } as const;

interface UsageByTypeProps {
    kind: UsageKind;
    kinds?: readonly UsageKind[];
    onKindChange: (kind: UsageKind) => void;
    nameFilter: string;
    onNameFilterChange: (name: string) => void;
    windowParams: UsageWindowParams;
    costByKind?: UsageCostByKind;
    totalCost?: string;
    daily?: readonly UsageDay[];
    summaryWindow?: UsageWindow;
    summaryError?: Error | null;
    totalIsStale: boolean;
    today: string;
    isThisMonth: boolean;
    openDetail: UsageDetailTarget | null;
    onOpenDetail: (target: UsageDetailTarget) => void;
}

function isRefreshing(query: { isPlaceholderData: boolean; isRefetching: boolean }): boolean {
    return query.isPlaceholderData || query.isRefetching;
}

export function UsageByType({ kind, kinds = USAGE_KINDS, onKindChange, ...panelProps }: Readonly<UsageByTypeProps>) {
    return (
        <section aria-labelledby="usage-by-type-heading" className="flex flex-col gap-3">
            <h2 id="usage-by-type-heading" className="font-semibold">
                Usage by Type
            </h2>
            <Tabs value={kind} onValueChange={value => isUsageKind(value) && onKindChange(value)}>
                <TabsList aria-label="Usage type">
                    {kinds.map(k => (
                        <TabsTrigger key={k} value={k} aria-label={KIND_CONFIG[k].tab}>
                            {KIND_CONFIG[k].tab}
                        </TabsTrigger>
                    ))}
                </TabsList>
                <TabsContent value={kind}>
                    <UsageTypePanel key={kind} kind={kind} {...panelProps} />
                </TabsContent>
            </Tabs>
        </section>
    );
}

function UsageTypePanel({
    kind,
    nameFilter,
    onNameFilterChange,
    windowParams,
    costByKind,
    totalCost,
    daily,
    summaryWindow,
    summaryError,
    totalIsStale,
    today,
    isThisMonth,
    openDetail,
    onOpenDetail,
}: Readonly<Omit<UsageByTypeProps, "kinds" | "onKindChange">>) {
    const config = KIND_CONFIG[kind];
    const kindCost = config.rowsOverlap ? undefined : costByKind?.[kind];
    const listCost = costByKind?.[kind];

    const isModels = kind === "models";
    const top = useUsageList(
        kind,
        isModels ? { window: windowParams, pageNumber: 1, pageSize: SPENDERS_SHOWN, sortBy: "cost", sortDir: "desc", expandDaily: true } : { window: windowParams, pageNumber: 1, pageSize: TOP_PAGE_SIZE, sortBy: "cost", sortDir: "desc" }
    );
    const bottom = useUsageList(kind, { window: windowParams, pageNumber: 1, pageSize: SPENDERS_SHOWN, sortBy: "cost", sortDir: "asc" }, { enabled: !isModels });
    const topRows = useMemo(() => top.data?.data.slice(0, SPENDERS_SHOWN), [top.data]);
    const bottomRows = bottom.data?.data;
    const colorFor = useMemo(() => spenderColors(topRows ?? [], bottomRows ?? []), [topRows, bottomRows]);

    const name = useDebounce(nameFilter, SEARCH_DEBOUNCE_MS).trim() || undefined;
    const sortState = useSortState<UsageAnySortField>(config.defaultSort.key, config.defaultSort.dir);
    const { sortKey, sortDir } = !isThisMonth && sortState.sortKey === "budgetUsage" ? COST_FIRST : sortState;
    const { handleSort } = sortState;
    const [page, setPage] = usePageNumber(`${JSON.stringify(windowParams)}|${name ?? ""}|${sortKey}:${sortDir}`);
    const table = useUsageList(kind, { window: windowParams, pageNumber: page, pageSize: TABLE_PAGE_SIZE, sortBy: sortKey, sortDir, name });

    const totalCount = table.data?.meta.pagination.count ?? 0;
    const totalPages = Math.max(1, table.data?.meta.pagination.totalPages ?? 1);
    const pageStart = (page - 1) * TABLE_PAGE_SIZE;
    const tableLoading = table.isLoading || (table.isPlaceholderData && totalCount === 0);

    useEffect(() => {
        if (table.data && page > totalPages) setPage(totalPages);
    }, [table.data, page, totalPages, setPage]);

    return (
        <div className="flex flex-col gap-4">
            {isModels ? (
                <ModelDailyCostCard
                    rows={top.data?.data}
                    daily={daily}
                    window={summaryWindow}
                    isStale={totalIsStale || isRefreshing(top)}
                    isLoading={top.isLoading}
                    error={top.error}
                    dailyError={summaryError}
                    today={today}
                    showRestOfMonth={isThisMonth && !totalIsStale}
                    onOpen={onOpenDetail}
                />
            ) : (
                <div className="grid gap-4 lg:grid-cols-2">
                    <SpendersCard
                        title="Top Spenders"
                        kind={kind}
                        rows={topRows}
                        colorFor={colorFor}
                        kindCost={kindCost}
                        isStale={totalIsStale || isRefreshing(top)}
                        isThisMonth={isThisMonth}
                        isLoading={top.isLoading}
                        error={top.error}
                        selected={openDetail}
                        onOpen={onOpenDetail}
                    />
                    <SpendersCard
                        title="Lowest Spenders"
                        alsoShown={top.data ? (topRows ?? []) : null}
                        kind={kind}
                        rows={bottomRows}
                        colorFor={colorFor}
                        kindCost={kindCost}
                        isStale={totalIsStale || isRefreshing(top) || isRefreshing(bottom)}
                        isThisMonth={isThisMonth}
                        isLoading={bottom.isLoading}
                        error={bottom.error}
                        selected={openDetail}
                        onOpen={onOpenDetail}
                    />
                </div>
            )}
            <section aria-labelledby="usage-cost-breakdown-heading" className="flex min-w-0 flex-col gap-3 rounded-sm border p-4">
                <div className="flex items-center gap-1">
                    <h3 id="usage-cost-breakdown-heading" className="text-sm font-semibold">
                        Cost Breakdown
                    </h3>
                    {config.outside && !totalIsStale && totalCost && listCost && hasBreakdownFigures(listCost, totalCost) && (
                        <HelpPopover content={<CostBreakdownTooltip config={config.outside} listCost={listCost} totalCost={totalCost} rowsOverlap={config.rowsOverlap} />} size="16" label="About this table" />
                    )}
                </div>
                <SearchInput value={nameFilter} onChange={onNameFilterChange} placeholder="Filter by name" testid="usageNameFilter" />
                <div className="flex h-[29rem] min-h-0 flex-col gap-3">
                    <TableStateBoundary
                        resourceName={config.plural}
                        isLoading={tableLoading}
                        error={table.data || !table.error ? null : new Error(usageErrorDetail(table.error))}
                        itemCount={totalCount}
                        hasActiveFilters={name !== undefined}
                        onClearFilters={() => onNameFilterChange("")}
                        emptyDescription="No usage in this period."
                        stateClassName="py-8"
                    >
                        <div className={cn("min-h-0 min-w-0 flex-1 overflow-auto rounded-xs border transition-opacity", table.isPlaceholderData && "opacity-60")} aria-busy={table.isPlaceholderData}>
                            <CostBreakdownTable kind={kind} rows={table.data?.data ?? []} sort={{ key: sortKey, dir: sortDir, onSort: handleSort }} isThisMonth={isThisMonth} selected={openDetail} onOpen={onOpenDetail} />
                        </div>
                        <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={setPage} summary={`Showing ${pageStart + 1}-${Math.min(pageStart + TABLE_PAGE_SIZE, totalCount)} of ${totalCount}`} />
                    </TableStateBoundary>
                </div>
            </section>
        </div>
    );
}
