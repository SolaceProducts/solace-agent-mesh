// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { OnboardingBanner } from "@/common/banners/OnboardingBanner";
import { EmptySection } from "@/common/layout";
import { PaginationControls } from "@/common/layout/PaginationControls";
import { TableStateBoundary } from "@/common/status";

import { DOC_LINKS } from "@/utils/docs";

import { SearchInput } from "@/ui/search-input";

import type { BudgetFormValues } from "./budgetForm.schema";
import { BudgetFormDialog } from "./BudgetFormDialog";
import { BudgetsList } from "./BudgetsList";
import { MeshBudgetSummary } from "./MeshBudgetSummary";
import type { BudgetPermissions, BudgetRow } from "./types";

const BANNER_HEADER = "Control model spending with budgets.";
const BANNER_DESCRIPTION = "Budgets limit how much each user, or all of Agent Mesh, can spend on model usage each calendar month. Set a default budget for users without one, and add an alert before spending reaches a limit.";
const MESH_ONLY_BANNER_DESCRIPTION = "Budgets limit how much Agent Mesh can spend on model usage each calendar month. Add an alert before spending reaches the limit.";
const EMPTY_DESCRIPTION = "Budgets limit how much each user, or all of Agent Mesh, can spend on model usage each calendar month. Create a budget to get started.";
const USER_BUDGETS_UNSUPPORTED_MESSAGE = "User budgets are available in production deployments of Agent Mesh. In the desktop app, use the Agent Mesh budget to limit spending.";

export interface BudgetsPageProps {
    budgets: readonly BudgetRow[];
    totalCount: number;
    page: number;
    pageSize: number;
    onPageChange: (page: number) => void;

    nameFilter: string;
    onNameFilterChange: (value: string) => void;
    hasActiveFilters: boolean;

    isLoading?: boolean;
    error?: Error | null;

    meshBudget: BudgetRow | null;
    meshLoading?: boolean;
    meshError?: Error | null;

    permissions: BudgetPermissions;
    createOpen: boolean;
    onCreateOpenChange: (open: boolean) => void;
    onCreate: (values: BudgetFormValues) => Promise<void>;
    onUpdate: (budget: BudgetRow, values: BudgetFormValues) => Promise<void>;
    onSetDefault: (budget: BudgetRow) => void;
    onDelete: (budget: BudgetRow) => Promise<void>;
    onManageAssignments: (budget: BudgetRow) => void;
    onViewUsage?: (budget: BudgetRow) => void;
    userBudgetsSupported?: boolean;
}

export function BudgetsPage({
    budgets,
    totalCount,
    page,
    pageSize,
    onPageChange,
    nameFilter,
    onNameFilterChange,
    hasActiveFilters,
    isLoading,
    error,
    meshBudget,
    meshLoading,
    meshError,
    permissions,
    createOpen,
    onCreateOpenChange,
    onCreate,
    onUpdate,
    onSetDefault,
    onDelete,
    onManageAssignments,
    onViewUsage,
    userBudgetsSupported = true,
}: Readonly<BudgetsPageProps>) {
    const [editOpen, setEditOpen] = useState(false);
    const [editBudget, setEditBudget] = useState<BudgetRow | null>(null);
    const openEdit = (budget: BudgetRow) => {
        setEditBudget(budget);
        setEditOpen(true);
    };
    const [meshOpen, setMeshOpen] = useState(false);
    const [meshDialogBudget, setMeshDialogBudget] = useState<BudgetRow | null>(null);
    const openMeshDialog = (budget: BudgetRow) => {
        setMeshDialogBudget(budget);
        setMeshOpen(true);
    };

    const totalPages = Math.max(1, Math.ceil(totalCount / pageSize));
    const pageStart = (page - 1) * pageSize;

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-4 p-6 md:p-8">
            <OnboardingBanner
                storageKey="sam-budgets-onboarding-dismissed"
                header={BANNER_HEADER}
                description={userBudgetsSupported ? BANNER_DESCRIPTION : MESH_ONLY_BANNER_DESCRIPTION}
                learnMoreText="Learn more about budgets"
                learnMoreUrl={DOC_LINKS.budgets}
            />

            <MeshBudgetSummary budget={meshBudget} isLoading={meshLoading} error={meshError} canEdit={permissions.canEdit} onEdit={openMeshDialog} />

            <h2 id="user-budget-title" className="font-semibold">
                User Budgets
            </h2>
            {userBudgetsSupported ? (
                <>
                    <div className="flex flex-wrap items-end justify-between gap-3">
                        <SearchInput value={nameFilter} onChange={onNameFilterChange} testid="budgetNameSearch" />
                        <p className="text-xs text-(--secondary-text-wMain)">All budgets are in USD and reset at the start of each month (UTC)</p>
                    </div>

                    <TableStateBoundary
                        resourceName="Budgets"
                        isLoading={isLoading}
                        error={error}
                        itemCount={totalCount}
                        hasActiveFilters={hasActiveFilters}
                        onClearFilters={() => onNameFilterChange("")}
                        emptyDescription={EMPTY_DESCRIPTION}
                        emptyActions={permissions.canCreate ? [{ text: "Create User Budget", variant: "default", onClick: () => onCreateOpenChange(true) }] : undefined}
                    >
                        <div className="min-h-0 flex-1 overflow-auto rounded-xs border">
                            <BudgetsList budgets={budgets} permissions={permissions} onViewUsage={onViewUsage} onManageAssignments={onManageAssignments} onEdit={openEdit} onSetDefault={onSetDefault} onDelete={onDelete} />
                        </div>
                        <PaginationControls variant="compact" totalPages={totalPages} currentPage={page} onPageChange={onPageChange} summary={`Showing ${pageStart + 1}-${Math.min(pageStart + pageSize, totalCount)} of ${totalCount}`} />
                    </TableStateBoundary>

                    <BudgetFormDialog open={createOpen} onOpenChange={onCreateOpenChange} onSubmit={onCreate} />
                    <BudgetFormDialog open={editOpen} budget={editBudget} onOpenChange={setEditOpen} onSubmit={values => (editBudget ? onUpdate(editBudget, values) : Promise.resolve())} />
                </>
            ) : (
                <EmptySection message={USER_BUDGETS_UNSUPPORTED_MESSAGE} className="px-4 text-center" />
            )}

            <BudgetFormDialog open={meshOpen} scope="mesh" budget={meshDialogBudget} onOpenChange={setMeshOpen} onSubmit={values => (meshDialogBudget ? onUpdate(meshDialogBudget, values) : Promise.resolve())} />
        </div>
    );
}
