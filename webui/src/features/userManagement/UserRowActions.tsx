// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { MorePopoverButton } from "@/common/buttons";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import type { User } from "@/types";

import { EditUserBudgetDialog } from "./EditUserBudgetDialog";
import { UserFormDialog } from "./UserFormDialog";
import { isSystemUser } from "./utils";

interface UserRowActionsProps {
    user: User;
    canEditBudget?: boolean;
}

export function UserRowActions({ user, canEditBudget = false }: Readonly<UserRowActionsProps>) {
    const [editOpen, setEditOpen] = useState(false);
    const [budgetOpen, setBudgetOpen] = useState(false);

    const actions: MenuAction[] = [];
    if (!isSystemUser(user)) actions.push({ id: "edit", label: "Edit User Roles", onClick: () => setEditOpen(true) });
    if (canEditBudget) actions.push({ id: "editBudget", label: "Manage Budget", onClick: () => setBudgetOpen(true) });

    if (actions.length === 0) return null;

    return (
        <>
            <Popover>
                <PopoverTrigger data-testid="userRowMoreTrigger" onClick={e => e.stopPropagation()} asChild>
                    {MorePopoverButton}
                </PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={actions} />
                </PopoverContent>
            </Popover>
            <UserFormDialog user={user} open={editOpen} onOpenChange={setEditOpen} />
            {budgetOpen && <EditUserBudgetDialog user={user} open={budgetOpen} onOpenChange={setBudgetOpen} />}
        </>
    );
}
