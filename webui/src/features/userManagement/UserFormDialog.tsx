// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";

import { useAssignableRoles, useSetUserRoleGrantsMutation, useUserRoleGrants } from "@/api/rbac";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { ConfirmationDialog } from "@/common/dialogs/ConfirmationDialog";
import { PageLabel } from "@/common/layout";

import { type User } from "@/types";

import { NoAssignableRolesBanner } from "./NoAssignableRolesBanner";
import { RoleMultiSelect } from "./RoleMultiSelect";
import { USER_MANAGEMENT_ROUTES } from "./routes";

interface UserFormDialogProps {
    user: User;
    open: boolean;
    onOpenChange: (open: boolean) => void;
}

export function UserFormDialog({ user, open, onOpenChange }: Readonly<UserFormDialogProps>) {
    const navigate = useNavigate();

    const { data: assignableRoles = [], isSuccess: rolesLoaded, isError: rolesFailed, error: rolesError } = useAssignableRoles();
    const { data: directGrants, isSuccess: grantsLoaded, isError: grantsFailed, error: grantsError } = useUserRoleGrants(user.id, { enabled: open });
    const setGrants = useSetUserRoleGrantsMutation();

    const [selectedIds, setSelectedIds] = useState<string[]>([]);
    const [saveError, setSaveError] = useState<string | null>(null);

    useEffect(() => {
        if (open && rolesLoaded && grantsLoaded && directGrants) {
            setSelectedIds(directGrants);
            setSaveError(null);
        }
    }, [open, rolesLoaded, grantsLoaded, directGrants]);

    const loadFailed = rolesFailed || grantsFailed;
    const loadError = grantsError ?? rolesError;
    const loading = open && !loadFailed && (!rolesLoaded || !grantsLoaded);
    const noAssignableRoles = rolesLoaded && assignableRoles.length === 0;

    const identity = user.displayName ?? user.email;

    const handleOpenChange = (next: boolean) => {
        if (!next) {
            setGrants.reset();
            setSaveError(null);
        }
        onOpenChange(next);
    };

    const onSave = async () => {
        setSaveError(null);
        try {
            await setGrants.mutateAsync({ userId: user.id, roleIds: selectedIds });
        } catch (e) {
            setSaveError(e instanceof Error ? e.message : "Failed to update the user's roles.");
            throw e;
        }
    };

    return (
        <ConfirmationDialog
            open={open}
            onOpenChange={handleOpenChange}
            title="Edit User Roles"
            description={`Update the direct role assignments for ${identity}.`}
            actionLabels={{ confirm: "Save" }}
            isLoading={setGrants.isPending}
            isEnabled={!loading && !loadFailed}
            onConfirm={onSave}
            content={
                <div className="flex flex-col gap-2 py-2">
                    {saveError && <MessageBanner variant="error" message={saveError} />}
                    {!saveError &&
                        !loadFailed &&
                        (noAssignableRoles ? (
                            <NoAssignableRolesBanner
                                message="No assignable roles exist yet. Create a role before you assign one directly to a user. System roles cannot be assigned directly."
                                actionLabel="Go to Roles"
                                onAction={() => {
                                    handleOpenChange(false);
                                    navigate(USER_MANAGEMENT_ROUTES.roles);
                                }}
                            />
                        ) : (
                            <MessageBanner variant="info" message="This dialog sets the user's direct role assignments only. Roles granted another way, such as through a claim mapping, stay in effect." />
                        ))}
                    <PageLabel>Roles</PageLabel>
                    {loadFailed ? (
                        <MessageBanner variant="error" message={loadError instanceof Error ? loadError.message : "Failed to load this user's roles. Close and try again."} />
                    ) : (
                        <RoleMultiSelect roles={assignableRoles} value={selectedIds} onChange={setSelectedIds} disabled={noAssignableRoles || loading} />
                    )}
                </div>
            }
        />
    );
}
