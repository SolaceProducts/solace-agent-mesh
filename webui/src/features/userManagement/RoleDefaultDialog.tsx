// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { useAssignableRoles, useDefaultRoles, useSetDefaultRolesMutation } from "@/api/rbac";

import { CustomCheckbox } from "@/common/CustomCheckbox";
import { ConfirmationDialog } from "@/common/dialogs";
import { ErrorDialog } from "@/common/dialogs/ErrorDialog";
import { HelpPopover } from "@/common/HelpPopover";
import { PageLabel } from "@/common/layout";

import { NoAssignableRolesBanner } from "./NoAssignableRolesBanner";
import { RoleFormDialog } from "./RoleFormDialog";
import { RoleMultiSelect } from "./RoleMultiSelect";

interface RoleDefaultDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
}

const setKey = (ids: readonly string[]) => [...ids].sort((a, b) => a.localeCompare(b)).join(",");

export function RoleDefaultDialog({ open, onOpenChange }: Readonly<RoleDefaultDialogProps>) {
    const { data: selectableRoles = [] } = useAssignableRoles();
    const { data: defaults } = useDefaultRoles();
    const setDefaultRoles = useSetDefaultRolesMutation();

    const currentDefaultIds = (defaults?.roleIds ?? []).filter(id => selectableRoles.some(role => role.id === id));
    const noRoles = selectableRoles.length === 0;

    const [enabled, setEnabled] = useState(false);
    const [selectedIds, setSelectedIds] = useState<string[]>([]);
    const [createRoleOpen, setCreateRoleOpen] = useState(false);
    const [actionError, setActionError] = useState<string | null>(null);

    const currentKey = setKey(currentDefaultIds);
    useEffect(() => {
        if (open) {
            setEnabled(currentDefaultIds.length > 0);
            setSelectedIds(currentDefaultIds);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [open, currentKey]);

    const desiredIds = enabled ? selectedIds : [];
    const dirty = setKey(desiredIds) !== currentKey;

    const onConfirm = async () => {
        try {
            await setDefaultRoles.mutateAsync(desiredIds);
        } catch (e) {
            setActionError(e instanceof Error ? e.message : "Failed to update the default roles.");
        }
    };

    return (
        <>
            <ConfirmationDialog
                open={open}
                onOpenChange={onOpenChange}
                title="Manage Default Roles"
                actionLabels={{ confirm: "Save" }}
                isEnabled={dirty}
                isLoading={setDefaultRoles.isPending}
                onConfirm={onConfirm}
                content={
                    <div className="flex flex-col gap-4">
                        {noRoles && (
                            <NoAssignableRolesBanner
                                message="No assignable roles exist yet. Create a role before you select a default role. System roles cannot be default roles."
                                actionLabel="Create Role"
                                onAction={() => {
                                    onOpenChange(false);
                                    setCreateRoleOpen(true);
                                }}
                            />
                        )}
                        <div className="flex items-center gap-2">
                            <CustomCheckbox ariaLabel="Assign default roles to users with no other roles" checked={enabled} onChange={e => setEnabled(e.target.checked)} disabled={noRoles} />
                            Assign default roles to users with no other roles
                            <HelpPopover
                                side="right"
                                content="When you turn this on, a user receives the selected roles only if they have no roles assigned directly, through a claim mapping, or in Agent Mesh configuration. Turn it off to leave those users with no roles."
                            />
                        </div>
                        {enabled && (
                            <div className="flex flex-col gap-1">
                                <PageLabel>Default Roles</PageLabel>
                                <RoleMultiSelect roles={selectableRoles} value={selectedIds} onChange={setSelectedIds} />
                            </div>
                        )}
                    </div>
                }
            />
            <RoleFormDialog open={createRoleOpen} onOpenChange={setCreateRoleOpen} />
            <ErrorDialog open={actionError !== null} onOpenChange={open => !open && setActionError(null)} title="Default Roles Update Failed" error={actionError ?? ""} errorSource="api" />
        </>
    );
}
