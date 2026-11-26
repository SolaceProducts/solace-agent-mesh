// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, VisuallyHidden } from "@/ui/dialog";

import type { ClaimMapping } from "@/types";

import type { RoleLookup } from "./useRoleLookup";
import { UserRoleChips } from "./UserRoleChips";

interface ClaimMappingViewDialogProps {
    mapping: ClaimMapping | null;
    roleById: RoleLookup;
    onOpenChange: (open: boolean) => void;
    onEdit?: (mapping: ClaimMapping) => void;
}

export function ClaimMappingViewDialog({ mapping, roleById, onOpenChange, onEdit }: Readonly<ClaimMappingViewDialogProps>) {
    return (
        <Dialog open={mapping !== null} onOpenChange={onOpenChange}>
            <DialogContent showCloseButton={false} className="flex max-h-[80vh] flex-col sm:max-w-2xl">
                {mapping && (
                    <>
                        <DialogHeader className="shrink-0">
                            <div className="flex items-center justify-between gap-4">
                                <DialogTitle>Claim Mapping</DialogTitle>
                                {onEdit && mapping.origin === "custom" && (
                                    <Button variant="outline" size="sm" onClick={() => onEdit(mapping)}>
                                        Edit
                                    </Button>
                                )}
                            </div>
                            <VisuallyHidden>
                                <DialogDescription>Read-only details for the {mapping.name} claim mapping.</DialogDescription>
                            </VisuallyHidden>
                        </DialogHeader>

                        <div className="mt-4 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto">
                            <div className="flex flex-col gap-1">
                                <span className="text-sm text-(--secondary-text-wMain)">Name</span>
                                <p className="text-sm font-medium">{mapping.name}</p>
                            </div>
                            <div className="flex flex-col gap-1">
                                <span className="text-sm text-(--secondary-text-wMain)">Claim Value</span>
                                <p className="font-mono text-sm">{mapping.claimValue}</p>
                            </div>
                            <div className="flex flex-col gap-1.5">
                                <span className="text-sm text-(--secondary-text-wMain)">Roles</span>
                                {mapping.roleIds.length === 0 ? <p className="text-sm">No roles assigned.</p> : <UserRoleChips roles={mapping.roleIds.map(roleId => ({ roleId }))} roleById={roleById} max={mapping.roleIds.length} />}
                            </div>
                        </div>

                        <DialogFooter className="shrink-0">
                            <DialogClose asChild>
                                <Button variant="outline">Close</Button>
                            </DialogClose>
                        </DialogFooter>
                    </>
                )}
            </DialogContent>
        </Dialog>
    );
}
