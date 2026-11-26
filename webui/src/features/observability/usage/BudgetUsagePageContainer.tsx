// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { useBudget } from "@/api/budgets";
import { isUsageSubjectId, type UsageWindowParams, useUsageList } from "@/api/usage";

import { useUserContext } from "@/providers/contexts";

import { Header } from "@/common/layout/Header";
import { PageFooter } from "@/common/layout/PageFooter";
import { PageLayout } from "@/common/layout/PageLayout";
import { PaginationControls } from "@/common/layout/PaginationControls";
import { EmptyState, TableStateBoundary } from "@/common/status";
import { TRY_AGAIN_SLOW_READ_HINT, usageErrorDetail } from "@/common/usage";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";

import { BudgetDetails, toBudgetRow } from "@/features/systemSettings";
import { BUDGETS_ROUTE } from "@/features/systemSettings/routes";

import { useBudgetsEnabled, useDebounce, useIsUserManagementEnabled, usePageNumber } from "@/hooks";
import { useSortState } from "@/ui";
import { cn } from "@/utils";

import { OBSERVABILITY_ROUTES } from "../routes";

import { type BudgetUsersSortField, BudgetUsersTable } from "./BudgetUsersTable";
import type { UsageDetailTarget } from "./usageDetailTarget";
import { TABLE_PAGE_SIZE } from "./usageKinds";
import { usageDetailSearchFor } from "./useUsageUrlState";

const SEARCH_DEBOUNCE_MS = 300;
const THIS_MONTH: UsageWindowParams = { period: "this_month" };

function BudgetLoadError({ notFound, error, onRetry }: Readonly<{ notFound: boolean; error: Error | null; onRetry: () => void }>) {
    if (notFound) return <EmptyState variant="notFound" title="Budget Not Found" subtitle="There is no user budget with this id." />;
    return <EmptyState variant="error" title="Couldn't Load Budget" subtitle={getErrorMessage(error, "Couldn't load the budget.")} buttons={[{ text: "Retry", variant: "outline", onClick: onRetry }]} />;
}

export function BudgetUsagePageContainer() {
    const { budgetId = "" } = useParams();
    const navigate = useNavigate();

    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canManageUsers = useIsUserManagementEnabled();
    const budgetsEnabled = useBudgetsEnabled();
    const canManageBudget = canManageUsers && budgetsEnabled && userHasCapability(CAPABILITIES.assignBudgets);
    const viewUsage = (target: UsageDetailTarget) => navigate(OBSERVABILITY_ROUTES.usage + usageDetailSearchFor(target));

    const isBudgetId = isUsageSubjectId(budgetId);
    const budgetQuery = useBudget(budgetId, { enabled: isBudgetId });
    const budget = budgetQuery.data?.subjectType === "user" ? budgetQuery.data : undefined;
    const notUserBudget = !isBudgetId || (budgetQuery.data !== undefined && budget === undefined);
    const [nameFilter, setNameFilter] = useState("");
    const name = useDebounce(nameFilter, SEARCH_DEBOUNCE_MS).trim() || undefined;
    const { sortKey, sortDir, handleSort } = useSortState<BudgetUsersSortField>("cost", "desc");
    const [page, setPage] = usePageNumber(`${name ?? ""}|${sortKey}:${sortDir}`);
    const table = useUsageList("users", { window: THIS_MONTH, pageNumber: page, pageSize: TABLE_PAGE_SIZE, sortBy: sortKey, sortDir, name, budgetId }, { enabled: budget !== undefined });

    const totalCount = table.data?.meta.pagination.count ?? 0;
    const totalPages = Math.max(1, table.data?.meta.pagination.totalPages ?? 1);
    const pageStart = (page - 1) * TABLE_PAGE_SIZE;
    const tableLoading = budgetQuery.isLoading || table.isLoading || (table.isPlaceholderData && totalCount === 0);

    useEffect(() => {
        if (table.data && page > totalPages) setPage(totalPages);
    }, [table.data, page, totalPages, setPage]);

    const title = budget?.name ?? "Budget";
    const header = <Header title={<h1 className="truncate">{title}</h1>} breadcrumbs={[{ label: "Budgets", onClick: () => navigate(BUDGETS_ROUTE) }, { label: title }]} />;

    const footer = (
        <PageFooter>
            <Button variant="outline" testid="budgetUsageClose" onClick={() => navigate(BUDGETS_ROUTE)}>
                Close
            </Button>
        </PageFooter>
    );

    if (notUserBudget || (budgetQuery.error && !budget)) {
        return (
            <PageLayout>
                {header}
                <BudgetLoadError notFound={notUserBudget || (budgetQuery.error as Error & { status?: number }).status === 404} error={budgetQuery.error} onRetry={() => budgetQuery.refetch()} />
                {footer}
            </PageLayout>
        );
    }

    return (
        <PageLayout>
            {header}
            <div className="flex min-h-0 min-w-0 flex-1 flex-col">
                <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-auto p-6 md:p-8">
                    {budget && <BudgetDetails budget={toBudgetRow(budget)} />}
                    <div className="mt-4 flex flex-col gap-1">
                        <h2 className="font-semibold">User Usage</h2>
                        {budget?.isDefault && <p className="text-sm text-(--secondary-text-wMain)">Includes users with no budget assigned.</p>}
                    </div>
                    <SearchInput value={nameFilter} onChange={setNameFilter} placeholder="Filter by name" testid="budgetUsersNameFilter" />
                    <TableStateBoundary
                        resourceName="Users"
                        isLoading={tableLoading}
                        error={table.data || !table.error ? null : new Error(usageErrorDetail(table.error, TRY_AGAIN_SLOW_READ_HINT))}
                        errorActions={[{ text: "Retry", variant: "outline", onClick: () => table.refetch() }]}
                        itemCount={totalCount}
                        hasActiveFilters={name !== undefined}
                        onClearFilters={() => setNameFilter("")}
                        emptyTitle="No Users"
                        emptyDescription="This budget has not been assigned to any users."
                        stateClassName="rounded-xs border py-8"
                    >
                        <div className={cn("min-w-0 rounded-xs border transition-opacity", table.isPlaceholderData && "opacity-60")} aria-busy={table.isPlaceholderData}>
                            <BudgetUsersTable rows={table.data?.data ?? []} sort={{ key: sortKey, dir: sortDir, onSort: handleSort }} onViewUsage={viewUsage} canManageBudget={canManageBudget} />
                        </div>
                        <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={setPage} summary={`Showing ${pageStart + 1}-${Math.min(pageStart + TABLE_PAGE_SIZE, totalCount)} of ${totalCount}`} />
                    </TableStateBoundary>
                </div>
                {footer}
            </div>
        </PageLayout>
    );
}
