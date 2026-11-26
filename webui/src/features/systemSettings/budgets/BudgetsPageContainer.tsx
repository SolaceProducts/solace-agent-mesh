// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { Plus } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useBudgets, useCreateBudgetMutation, useDeleteBudgetMutation, useUpdateBudgetMutation } from "@/api/budgets";

import { useUserContext } from "@/providers/contexts";

import { Header } from "@/common/layout/Header";
import { PageLayout } from "@/common/layout/PageLayout";

import { Button } from "@/ui/button";

import { useDebounce, useIsUserBudgetsSupported, usePageNumber, useUsageDashboardEnabled } from "@/hooks";

import { budgetUsageRoute } from "../routes";

import type { BudgetFormValues } from "./budgetForm.schema";
import { BudgetsPage } from "./BudgetsPage";
import { ChangeDefaultBudgetDialogContainer } from "./ChangeDefaultBudgetDialogContainer";
import { ManageAssignmentsDialogContainer } from "./manageAssignments/ManageAssignmentsDialogContainer";
import { toBudgetRow, toCreateRequest, toUpdateRequest } from "./mapping";
import type { BudgetPermissions, BudgetRow } from "./types";

const PAGE_SIZE = 20;
const SEARCH_DEBOUNCE_MS = 300;

export function BudgetsPageContainer() {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const navigate = useNavigate();
    const canViewUsage = useUsageDashboardEnabled();
    const userBudgetsSupported = useIsUserBudgetsSupported();
    const permissions = useMemo<BudgetPermissions>(
        () => ({
            canCreate: userHasCapability(CAPABILITIES.createBudgets),
            canEdit: userHasCapability(CAPABILITIES.updateBudgets),
            canDelete: userHasCapability(CAPABILITIES.deleteBudgets) && userHasCapability(CAPABILITIES.assignBudgets),
            canAssign: userHasCapability(CAPABILITIES.assignBudgets),
        }),
        [userHasCapability, CAPABILITIES]
    );

    const [nameFilter, setNameFilter] = useState("");
    const [manageBudget, setManageBudget] = useState<BudgetRow | null>(null);
    const [newDefaultBudget, setNewDefaultBudget] = useState<BudgetRow | null>(null);
    const name = useDebounce(nameFilter, SEARCH_DEBOUNCE_MS).trim() || undefined;
    const [page, setPage] = usePageNumber(name ?? "");

    const { data, isLoading, isPlaceholderData, error } = useBudgets({ pageNumber: page, pageSize: PAGE_SIZE, name, subjectType: "user", sortBy: "amount", sortDir: "desc" }, { enabled: userBudgetsSupported });
    const mesh = useBudgets({ pageNumber: 1, pageSize: 1, subjectType: "mesh" });
    const meshRecord = mesh.data?.data[0];
    const meshBudget = useMemo(() => (meshRecord ? toBudgetRow(meshRecord) : null), [meshRecord]);
    const budgets = useMemo(() => (data?.data ?? []).map(toBudgetRow), [data]);
    const totalCount = data?.meta.pagination.count ?? 0;
    const totalPages = Math.max(1, data?.meta.pagination.totalPages ?? 1);
    const hasActiveFilters = name !== undefined || isPlaceholderData;

    useEffect(() => {
        if (data && page > totalPages) setPage(totalPages);
    }, [data, page, totalPages, setPage]);

    const createBudget = useCreateBudgetMutation();
    const updateBudget = useUpdateBudgetMutation();
    const deleteBudget = useDeleteBudgetMutation();

    const onCreate = async (values: BudgetFormValues) => {
        await createBudget.mutateAsync(toCreateRequest(values));
    };

    const onUpdate = async (budget: BudgetRow, values: BudgetFormValues) => {
        const changes = toUpdateRequest(budget, values);
        if (Object.keys(changes).length === 0) return;
        await updateBudget.mutateAsync({ id: budget.id, changes });
    };

    const [createOpen, setCreateOpen] = useState(false);
    const headerButtons =
        userBudgetsSupported && permissions.canCreate
            ? [
                  <Button key="create" variant="outline" testid="createBudget" onClick={() => setCreateOpen(true)}>
                      <Plus />
                      Create User Budget
                  </Button>,
              ]
            : undefined;

    return (
        <PageLayout>
            <Header title="Budgets" buttons={headerButtons} />
            <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
                <BudgetsPage
                    budgets={budgets}
                    totalCount={totalCount}
                    page={page}
                    pageSize={PAGE_SIZE}
                    onPageChange={setPage}
                    nameFilter={nameFilter}
                    onNameFilterChange={setNameFilter}
                    hasActiveFilters={hasActiveFilters}
                    isLoading={isLoading}
                    error={error}
                    meshBudget={meshBudget}
                    meshLoading={mesh.isLoading}
                    meshError={mesh.error}
                    permissions={permissions}
                    createOpen={createOpen}
                    onCreateOpenChange={setCreateOpen}
                    onCreate={onCreate}
                    onUpdate={onUpdate}
                    onSetDefault={setNewDefaultBudget}
                    onDelete={budget => deleteBudget.mutateAsync(budget.id)}
                    onManageAssignments={setManageBudget}
                    onViewUsage={canViewUsage ? budget => navigate(budgetUsageRoute(budget.id)) : undefined}
                    userBudgetsSupported={userBudgetsSupported}
                />
                {userBudgetsSupported && (
                    <>
                        <ChangeDefaultBudgetDialogContainer budget={newDefaultBudget} listedDefault={budgets.find(budget => budget.isDefault) ?? null} onClose={() => setNewDefaultBudget(null)} />
                        <ManageAssignmentsDialogContainer budget={manageBudget} onClose={() => setManageBudget(null)} />
                    </>
                )}
            </div>
        </PageLayout>
    );
}
