// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgents } from "@/api/agents";
import { useWorkflowsQuery } from "@/api/workflows";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, VisuallyHidden } from "@/ui/dialog";

import type { RBACRole } from "@/types";

import { type ComponentOption } from "./scopeBundles/bundles";
import { ScopeBundleEditor } from "./scopeBundles/ScopeBundleEditor";
import { originLabel } from "./utils";

interface RoleViewDialogProps {
    role: RBACRole | null;
    onOpenChange: (open: boolean) => void;
    onEdit?: (role: RBACRole) => void;
}

export function RoleViewDialog({ role, onOpenChange, onEdit }: Readonly<RoleViewDialogProps>) {
    const { data: agentData } = useAgents({ enabled: role !== null });
    const { data: workflowData } = useWorkflowsQuery({ enabled: role !== null });
    const agents = useMemo<ComponentOption[]>(() => (agentData ?? []).filter(a => a.id).map(a => ({ id: a.id!, name: a.name, description: a.description })), [agentData]);
    const workflows = useMemo<ComponentOption[]>(() => (workflowData ?? []).map(w => ({ id: w.id, name: w.displayName || w.name, description: w.description })), [workflowData]);

    return (
        <Dialog open={role !== null} onOpenChange={onOpenChange}>
            <DialogContent showCloseButton={false} className="flex h-[80vh] flex-col sm:max-w-2xl">
                {role && (
                    <>
                        <DialogHeader className="shrink-0">
                            <div className="flex items-center justify-between gap-4">
                                <DialogTitle>{role.name}</DialogTitle>
                                {onEdit && role.origin === "custom" && (
                                    <Button variant="outline" size="sm" onClick={() => onEdit(role)}>
                                        Edit
                                    </Button>
                                )}
                            </div>
                            <VisuallyHidden>
                                <DialogDescription>Read-only details for the {role.name} role.</DialogDescription>
                            </VisuallyHidden>
                        </DialogHeader>

                        <div className="mt-6 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto sm:max-w-2xl">
                            <div className="flex shrink-0 items-center gap-2">
                                <span className="text-sm text-(--secondary-text-wMain)">Type</span>
                                <div>{originLabel(role.origin)}</div>
                            </div>
                            <div className="mb-2 flex shrink-0 flex-col gap-1">
                                <span className="text-sm text-(--secondary-text-wMain)">Description</span>
                                <p className="text-sm">{role.description || "No description."}</p>
                            </div>
                            <div className="my-2 flex flex-col">
                                <ScopeBundleEditor value={role.scopes} onChange={() => {}} agents={agents} workflows={workflows} disabled />
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
