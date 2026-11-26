// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { useDeleteRoleMutation, useSetDefaultRolesMutation } from "@/api/rbac";

import { MorePopoverButton } from "@/common/buttons/MorePopoverButton";
import { ConfirmationDialog } from "@/common/dialogs/ConfirmationDialog";
import { ErrorDialog } from "@/common/dialogs/ErrorDialog";

import { getErrorMessage } from "@/utils/api";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import type { DefaultRolesResponse, RBACRole } from "@/types";

interface RoleRowActionsProps {
    role: RBACRole;
    defaults: DefaultRolesResponse | undefined;
    onView: (role: RBACRole) => void;
    onEdit: (role: RBACRole) => void;
}

export function RoleRowActions({ role, defaults, onView, onEdit }: Readonly<RoleRowActionsProps>) {
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [defaultDeleteError, setDefaultDeleteError] = useState(false);
    const [configDefaultError, setConfigDefaultError] = useState(false);
    const [actionError, setActionError] = useState<{ title: string; message: string } | null>(null);
    const deleteRole = useDeleteRoleMutation();
    const setDefaultRoles = useSetDefaultRolesMutation();

    const defaultRoleIds = defaults?.roleIds ?? [];
    const isDefault = defaultRoleIds.includes(role.id);
    const configurationSetsDefaults = defaults?.origin === "yaml" && defaultRoleIds.length > 0;

    const toggleDefault = async () => {
        if (configurationSetsDefaults) {
            setConfigDefaultError(true);
            return;
        }
        const next = isDefault ? defaultRoleIds.filter(id => id !== role.id) : [...defaultRoleIds, role.id];
        try {
            await setDefaultRoles.mutateAsync(next);
        } catch (e) {
            setActionError({ title: "Default Roles Update Failed", message: getErrorMessage(e, "Failed to update the default roles.") });
        }
    };

    const actions: MenuAction[] =
        role.origin === "custom"
            ? [
                  { id: "toggle-default", label: isDefault ? "Remove from Default Roles" : "Add to Default Roles", disabled: defaults === undefined, onClick: () => void toggleDefault() },
                  { id: "view", label: "View", onClick: () => onView(role) },
                  { id: "edit", label: "Edit", onClick: () => onEdit(role) },
                  { id: "delete", label: "Delete", divider: true, onClick: () => (isDefault ? setDefaultDeleteError(true) : setConfirmDelete(true)) },
              ]
            : [{ id: "view", label: "View", onClick: () => onView(role) }];

    return (
        <>
            <Popover>
                <PopoverTrigger data-testid="roleRowMoreTrigger" onClick={e => e.stopPropagation()} asChild>
                    {MorePopoverButton}
                </PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-50" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={actions} />
                </PopoverContent>
            </Popover>
            <ConfirmationDialog
                open={confirmDelete}
                title="Delete Role"
                content={
                    <div>
                        The role <span className="font-semibold">{role.name}</span> will be permanently deleted.
                    </div>
                }
                onOpenChange={setConfirmDelete}
                onConfirm={async () => {
                    try {
                        await deleteRole.mutateAsync(role.id);
                    } catch (e) {
                        setActionError({ title: "Role Deletion Failed", message: getErrorMessage(e, "Failed to delete the role.") });
                    }
                }}
                actionLabels={{ confirm: "Delete" }}
                isLoading={deleteRole.isPending}
            />
            <ErrorDialog
                open={defaultDeleteError}
                onOpenChange={setDefaultDeleteError}
                errorSource="client"
                title="Cannot Delete Default Role"
                error={
                    <div>
                        Unable to delete <span className="font-semibold">{role.name}</span> because it is a default role. Remove it under Manage Default Roles first.
                    </div>
                }
            />
            <ErrorDialog
                open={configDefaultError}
                onOpenChange={setConfigDefaultError}
                errorSource="client"
                title="Default Roles Set in Configuration"
                error="The default roles come from the Agent Mesh configuration, so a single role cannot be added or removed. Choose the default roles under Manage Default Roles; the set you save replaces the configuration's defaults."
            />
            <ErrorDialog open={actionError !== null} onOpenChange={open => !open && setActionError(null)} title={actionError?.title ?? ""} error={actionError?.message ?? ""} errorSource="api" />
        </>
    );
}
