// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { MessageBanner } from "@/common/banners";
import { MorePopoverButton } from "@/common/buttons/MorePopoverButton";
import { ConfirmationDialog } from "@/common/dialogs/ConfirmationDialog";
import { ErrorDialog } from "@/common/dialogs/ErrorDialog";

import { getErrorMessage } from "@/utils/api";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import type { BudgetPermissions, BudgetRow } from "./types";

interface BudgetRowActionsProps {
    budget: BudgetRow;
    permissions: BudgetPermissions;
    onViewUsage?: (budget: BudgetRow) => void;
    onEdit: (budget: BudgetRow) => void;
    onSetDefault: (budget: BudgetRow) => void;
    onDelete: (budget: BudgetRow) => Promise<void>;
}

export function BudgetRowActions({ budget, permissions, onViewUsage, onEdit, onSetDefault, onDelete }: Readonly<BudgetRowActionsProps>) {
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [isDeleting, setIsDeleting] = useState(false);
    const [defaultDeleteError, setDefaultDeleteError] = useState(false);
    const [actionError, setActionError] = useState<{ title: string; message: string } | null>(null);

    const confirmDeletion = async () => {
        setIsDeleting(true);
        try {
            await onDelete(budget);
        } catch (e) {
            setActionError({ title: "Couldn't Delete Budget", message: getErrorMessage(e, "Couldn't delete the budget.") });
        } finally {
            setIsDeleting(false);
        }
    };

    const actions: MenuAction[] = [];
    if (permissions.canAssign) {
        actions.push({ id: "set-default", label: "Set as Default", disabled: budget.isDefault, onClick: () => onSetDefault(budget) });
    }
    if (onViewUsage) actions.push({ id: "view-usage", label: "View Details", onClick: () => onViewUsage(budget) });
    if (permissions.canEdit) {
        actions.push({ id: "edit", label: "Edit", onClick: () => onEdit(budget) });
    }
    if (permissions.canDelete) {
        actions.push({ id: "delete", label: "Delete", divider: true, onClick: () => (budget.isDefault ? setDefaultDeleteError(true) : setConfirmDelete(true)) });
    }

    return (
        <>
            {actions.length > 0 && (
                <Popover>
                    <PopoverTrigger data-testid="budgetRowMoreTrigger" onClick={e => e.stopPropagation()} asChild>
                        {MorePopoverButton}
                    </PopoverTrigger>
                    <PopoverContent align="end" side="bottom" className="w-50" sideOffset={0} onClick={e => e.stopPropagation()}>
                        <Menu actions={actions} />
                    </PopoverContent>
                </Popover>
            )}
            <ConfirmationDialog
                open={confirmDelete}
                title="Delete Budget"
                content={
                    <div className="flex flex-col gap-4">
                        <div>
                            The budget <span className="font-semibold">{budget.name}</span> will be permanently deleted.
                        </div>
                        {(budget.assignedCount > 0 || budget.hasUnlistedAssignments) && <MessageBanner variant="info" message="Users assigned to this budget revert to the default user budget." />}
                    </div>
                }
                onOpenChange={setConfirmDelete}
                onConfirm={confirmDeletion}
                actionLabels={{ confirm: "Delete" }}
                isLoading={isDeleting}
            />
            <ErrorDialog
                open={defaultDeleteError}
                onOpenChange={setDefaultDeleteError}
                errorSource="client"
                title="Cannot Delete Default Budget"
                error={
                    <div>
                        You can't delete <span className="font-semibold">{budget.name}</span> because it's the default budget. First set another budget as the default.
                    </div>
                }
            />
            <ErrorDialog open={actionError !== null} onOpenChange={open => !open && setActionError(null)} title={actionError?.title ?? ""} error={actionError?.message ?? ""} errorSource="api" />
        </>
    );
}
