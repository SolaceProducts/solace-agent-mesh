// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";

import { useDeleteWorkflowMutation } from "@/api/workflows/hooks";

import { useChatContext } from "@/providers/contexts";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import { UserAccessDialog } from "@/features/userManagement/UserAccessDialog";

import { ErrorDialog } from "@/common";

import { DeleteWorkflowConfirmationDialog } from "./DeleteWorkflowConfirmationDialog";

interface WorkflowActionsPopoverProps {
    workflowId: string | undefined;
    workflowName: string;
    onOpen?: () => void;
    canDelete: boolean;
    canEdit?: boolean;
    canViewAccess?: boolean;
    onEditInBuilder?: () => void;
    children: React.ReactNode;
}

export function WorkflowActionsPopover({ workflowId, workflowName, onOpen, canDelete, canEdit = false, canViewAccess = false, onEditInBuilder, children }: Readonly<WorkflowActionsPopoverProps>) {
    const [menuOpen, setMenuOpen] = useState(false);
    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
    const [viewAccessOpen, setViewAccessOpen] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);
    const { addNotification } = useChatContext();
    const navigate = useNavigate();
    const deleteWorkflow = useDeleteWorkflowMutation();

    const offersEditWithAI = !!onEditInBuilder;
    useEffect(() => {
        if (!import.meta.env.DEV || workflowId) return;
        const dropped = [canEdit && "Edit", offersEditWithAI && "Edit with AI", canViewAccess && "View Access", canDelete && "Delete"].filter(Boolean);
        if (dropped.length > 0) {
            console.warn(`WorkflowActionsPopover: no id resolved for workflow "${workflowName}"; dropping ${dropped.join(", ")}.`);
        }
    }, [canDelete, canEdit, canViewAccess, offersEditWithAI, workflowId, workflowName]);

    const menuActions = useMemo<MenuAction[]>(() => {
        const actions: MenuAction[] = onOpen ? [{ id: "openWorkflow", label: "Open Workflow", onClick: onOpen }] : [];
        if (!workflowId) return actions;
        if (canEdit) {
            actions.push({ id: "edit", label: "Edit", onClick: () => navigate(`/workflows/${workflowId}/edit`) });
        }
        if (onEditInBuilder) {
            actions.push({ id: "editWithAI", label: "Edit with AI", onClick: onEditInBuilder });
        }
        if (canViewAccess) {
            actions.push({
                id: "viewAccess",
                label: "View Access",
                iconPosition: "left",
                onClick: () => {
                    setViewAccessOpen(true);
                    setMenuOpen(false);
                },
            });
        }
        if (canDelete) {
            actions.push({ id: "delete", label: "Delete", divider: true, onClick: () => setConfirmDeleteOpen(true) });
        }
        return actions;
    }, [canDelete, canEdit, canViewAccess, workflowId, navigate, onOpen, onEditInBuilder]);

    if (menuActions.length === 0) return null;

    const handleDelete = async () => {
        if (!workflowId) return;
        try {
            await deleteWorkflow.mutateAsync(workflowId);
            addNotification("Workflow deleted successfully.");
        } catch (err) {
            setDeleteError(err instanceof Error ? err.message : "Failed to delete the workflow. Please try again.");
        }
    };

    return (
        <>
            <Popover open={menuOpen} onOpenChange={setMenuOpen}>
                <PopoverTrigger asChild>{children}</PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={menuActions} />
                </PopoverContent>
            </Popover>

            <DeleteWorkflowConfirmationDialog open={confirmDeleteOpen} onOpenChange={setConfirmDeleteOpen} workflowName={workflowName} onConfirm={handleDelete} />

            <UserAccessDialog target={viewAccessOpen && workflowId ? { workflowId } : null} name={workflowName} onOpenChange={setViewAccessOpen} />

            <ErrorDialog open={!!deleteError} title="Workflow Deletion Failed" error={deleteError ?? ""} errorSource="api" onOpenChange={open => !open && setDeleteError(null)} />
        </>
    );
}
