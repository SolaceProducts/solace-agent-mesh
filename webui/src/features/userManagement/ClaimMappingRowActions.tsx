// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { useDeleteClaimMappingMutation } from "@/api/rbac";

import { MorePopoverButton } from "@/common/buttons";
import { ConfirmationDialog } from "@/common/dialogs";
import { ErrorDialog } from "@/common/dialogs/ErrorDialog";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import type { ClaimMapping } from "@/types";

interface ClaimMappingRowActionsProps {
    mapping: ClaimMapping;
    onView: (mapping: ClaimMapping) => void;
    onEdit: (mapping: ClaimMapping) => void;
}

export function ClaimMappingRowActions({ mapping, onView, onEdit }: Readonly<ClaimMappingRowActionsProps>) {
    const [confirmDelete, setConfirmDelete] = useState(false);
    const [actionError, setActionError] = useState<string | null>(null);
    const deleteMapping = useDeleteClaimMappingMutation();

    const actions: MenuAction[] =
        mapping.origin === "custom"
            ? [
                  { id: "view", label: "View", onClick: () => onView(mapping) },
                  { id: "edit", label: "Edit", onClick: () => onEdit(mapping) },
                  { id: "delete", label: "Delete", divider: true, onClick: () => setConfirmDelete(true) },
              ]
            : [{ id: "view", label: "View", onClick: () => onView(mapping) }];

    return (
        <>
            <Popover>
                <PopoverTrigger data-testid="claimMappingRowMoreTrigger" onClick={e => e.stopPropagation()} asChild>
                    {MorePopoverButton}
                </PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={actions} />
                </PopoverContent>
            </Popover>
            <ConfirmationDialog
                open={confirmDelete}
                title="Delete Claim Mapping"
                content={
                    <div>
                        The claim mapping for <span className="font-mono font-semibold">{mapping.claimValue}</span> will be permanently deleted.
                    </div>
                }
                onOpenChange={setConfirmDelete}
                onConfirm={async () => {
                    try {
                        await deleteMapping.mutateAsync(mapping.id);
                    } catch (e) {
                        setActionError(e instanceof Error ? e.message : "Failed to delete the claim mapping.");
                    }
                }}
                actionLabels={{ confirm: "Delete" }}
                isLoading={deleteMapping.isPending}
            />
            <ErrorDialog open={actionError !== null} onOpenChange={open => !open && setActionError(null)} title="Claim Mapping Deletion Failed" error={actionError ?? ""} errorSource="api" />
        </>
    );
}
