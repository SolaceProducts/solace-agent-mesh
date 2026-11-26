// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";

import { BUDGET_OPTIONS_PAGE_SIZE, useBudgets } from "@/api/budgets";
import { useUsers } from "@/api/rbac";

import { useUserContext } from "@/providers/contexts";

import { OnboardingBanner } from "@/common/banners/OnboardingBanner";
import { PaginationControls } from "@/common/layout/PaginationControls";
import { TableStateBoundary } from "@/common/status";

import { DOC_LINKS } from "@/utils/docs";

import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";

import { DefaultBudgetBadge } from "@/features/systemSettings/budgets/DefaultBudgetBadge";

import { MessageBanner } from "@/common";
import { useBudgetsEnabled, useDebounce, usePageNumber } from "@/hooks";
import { useSortState } from "@/ui";

import { useRoleLookup } from "./useRoleLookup";
import { UsersTable } from "./UsersTable";

const ANY_ROLE = "__any__";
const ANY_BUDGET = "__any__";
const PAGE_SIZE = 20;
const SEARCH_DEBOUNCE_MS = 300;

export function UsersPage() {
    const [roleFilter, setRoleFilter] = useState<string>(ANY_ROLE);
    const [budgetFilter, setBudgetFilter] = useState<string>(ANY_BUDGET);
    const [emailQuery, setEmailQuery] = useState("");

    const { sortKey, sortDir, handleSort } = useSortState("displayName");
    const debouncedQuery = useDebounce(emailQuery, SEARCH_DEBOUNCE_MS);
    const budgetsEnabled = useBudgetsEnabled();
    const roleId = roleFilter === ANY_ROLE ? undefined : roleFilter;
    const budgetId = budgetsEnabled && budgetFilter !== ANY_BUDGET ? budgetFilter : undefined;
    const search = debouncedQuery.trim() || undefined;
    const sort = `${sortKey}:${sortDir}`;
    const [page, setPage] = usePageNumber(`${search ?? ""}|${roleId ?? ""}|${budgetId ?? ""}|${sort}`);

    const { data, isLoading, error } = useUsers({ pageNumber: page, pageSize: PAGE_SIZE, search, roleId, budgetId, sort, expandBudget: budgetsEnabled });
    const { roles, roleById } = useRoleLookup();
    const { data: budgetOptions, error: budgetOptionsError } = useBudgets({ pageNumber: 1, pageSize: BUDGET_OPTIONS_PAGE_SIZE, subjectType: "user" }, { enabled: budgetsEnabled });

    const users = useMemo(() => data?.data ?? [], [data]);
    const pagination = data?.meta.pagination;

    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canEditBudget = budgetsEnabled && userHasCapability(CAPABILITIES.assignBudgets);
    const totalCount = pagination?.count ?? 0;
    const totalPages = Math.max(1, pagination?.totalPages ?? 1);

    useEffect(() => {
        if (page > totalPages) setPage(totalPages);
    }, [page, totalPages, setPage]);

    const hasActiveFilters = roleFilter !== ANY_ROLE || budgetId !== undefined || emailQuery.trim() !== "";
    const clearFilters = () => {
        setRoleFilter(ANY_ROLE);
        setBudgetFilter(ANY_BUDGET);
        setEmailQuery("");
    };

    const pageStart = (page - 1) * PAGE_SIZE;

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-4 p-6 md:p-8">
            <OnboardingBanner
                storageKey="sam-users-onboarding-dismissed"
                header="Manage access from one place."
                description="Assign roles to users to control what they can see and do across Solace Agent Mesh. Roles are collections of scopes you define on the Roles tab."
                learnMoreText="Learn more about user management"
                learnMoreUrl={DOC_LINKS.userManagementUsers}
            />

            <h2 className="text-lg font-semibold">Users ({totalCount})</h2>
            <div className="flex flex-wrap items-center gap-3">
                <SearchInput value={emailQuery} onChange={setEmailQuery} placeholder="Filter by name or email" testid="userEmailSearch" />
                <Select value={roleFilter} onValueChange={setRoleFilter}>
                    <SelectTrigger className="w-56" aria-label="Filter by role">
                        <SelectValue placeholder="Any Role" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value={ANY_ROLE}>Any Role</SelectItem>
                        {roles.map(role => (
                            <SelectItem key={role.id} value={role.id}>
                                {role.name}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
                {budgetsEnabled && (
                    <Select value={budgetFilter} onValueChange={setBudgetFilter} disabled={!!budgetOptionsError}>
                        <SelectTrigger className="w-56" aria-label="Filter by budget">
                            <SelectValue placeholder="Any Budget" />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value={ANY_BUDGET}>Any Budget</SelectItem>
                            {(budgetOptions?.data ?? []).map(budget => (
                                <SelectItem key={budget.id} value={budget.id}>
                                    {budget.name} {budget.isDefault && <DefaultBudgetBadge withTooltip={false} />}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                )}
            </div>

            {budgetsEnabled && budgetOptionsError && <MessageBanner variant="error" message="Couldn't load budgets. The budget filter is unavailable." />}

            <TableStateBoundary
                resourceName="Users"
                isLoading={isLoading}
                error={error}
                itemCount={totalCount}
                hasActiveFilters={hasActiveFilters}
                onClearFilters={clearFilters}
                emptyDescription="Users come from your identity provider. After they sign in, they appear here, where you can assign roles to control what they can see and do."
            >
                <div className="min-h-0 flex-1 overflow-auto rounded-xs border">
                    <UsersTable users={users} roleById={roleById} sortKey={sortKey} sortDir={sortDir} onSort={handleSort} showBudgets={budgetsEnabled} canEditBudget={canEditBudget} />
                </div>
                <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={setPage} summary={`Showing ${pageStart + 1}–${pageStart + users.length} of ${totalCount}`} />
            </TableStateBoundary>
        </div>
    );
}
