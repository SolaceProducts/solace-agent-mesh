// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Button } from "@/ui/button";
import { TruncatedText } from "@/ui/truncated-text";

import { formatBudgetLimit } from "@/utils";

import { BudgetRowActions } from "./BudgetRowActions";
import { DefaultBudgetBadge } from "./DefaultBudgetBadge";
import { formatWarnPercentage } from "./format";
import type { BudgetPermissions, BudgetRow } from "./types";

interface BudgetsListProps {
    budgets: readonly BudgetRow[];
    permissions: BudgetPermissions;
    onViewUsage?: (budget: BudgetRow) => void;
    onManageAssignments: (budget: BudgetRow) => void;
    onEdit: (budget: BudgetRow) => void;
    onSetDefault: (budget: BudgetRow) => void;
    onDelete: (budget: BudgetRow) => Promise<void>;
}

export function BudgetsList({ budgets, permissions, onViewUsage, onManageAssignments, onEdit, onSetDefault, onDelete }: Readonly<BudgetsListProps>) {
    const assignmentsAction = permissions.canAssign ? "Manage Assignments" : "View Assignments";
    return (
        <ul aria-label="User budgets">
            {budgets.map(budget => (
                <li key={budget.id} className="flex items-center gap-4 border-b px-4 py-3">
                    <div className="flex min-w-0 flex-1 flex-col gap-1">
                        <div className="flex min-w-0 items-center gap-3">
                            {onViewUsage ? (
                                <Button variant="link" className="h-auto min-w-0 p-0" onClick={() => onViewUsage(budget)}>
                                    <TruncatedText text={budget.name} className="block" />
                                </Button>
                            ) : (
                                <TruncatedText text={budget.name} className="font-medium" />
                            )}
                            <span className="shrink-0 font-semibold">{formatBudgetLimit(budget.amount)}</span>
                            {budget.isDefault && <DefaultBudgetBadge />}
                        </div>
                        <span className="text-sm text-(--secondary-text-wMain)">{budget.warnPercentage === null ? formatWarnPercentage(null) : `Alert at ${formatWarnPercentage(budget.warnPercentage)}`}</span>
                    </div>
                    <Button variant="ghost" aria-label={`${assignmentsAction} for ${budget.name}`} onClick={() => onManageAssignments(budget)}>
                        {assignmentsAction}
                    </Button>
                    <BudgetRowActions budget={budget} permissions={permissions} onViewUsage={onViewUsage} onEdit={onEdit} onSetDefault={onSetDefault} onDelete={onDelete} />
                </li>
            ))}
        </ul>
    );
}
