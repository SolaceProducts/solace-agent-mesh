// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { Pencil, Trash2 } from "lucide-react";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import { ConfirmationDialog } from "@/common";
import type { Toolset } from "@/types";

interface ToolsetMorePopoverProps {
    toolset: Toolset;
    children: React.ReactNode;
}

export const ToolsetMorePopover: React.FC<ToolsetMorePopoverProps> = ({ toolset, children }) => {
    const { handleDeleteCustomToolset, handleEditCustomToolset } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);

    const menuActions: MenuAction[] = [];
    if (userHasCapability(CAPABILITIES.updateToolsets)) {
        menuActions.push({
            id: "edit",
            label: "Edit",
            onClick: () => {
                handleEditCustomToolset(toolset);
            },
            icon: <Pencil />,
            iconPosition: "left",
        });
    }
    if (userHasCapability(CAPABILITIES.deleteToolsets)) {
        menuActions.push({
            id: "delete",
            label: "Delete",
            onClick: () => {
                setConfirmDeleteOpen(true);
            },
            icon: <Trash2 />,
            iconPosition: "left",
            divider: true,
        });
    }

    if (menuActions.length === 0) return null;

    return (
        <>
            <Popover>
                <PopoverTrigger data-testid="toolsetMorePopoverTrigger" onClick={e => e.stopPropagation()} asChild>
                    {children}
                </PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={menuActions} />
                </PopoverContent>
            </Popover>
            <ConfirmationDialog
                open={confirmDeleteOpen}
                title={`Delete ${toolset.name}`}
                content={
                    <div>
                        {toolset.agents && toolset.agents.length > 0 && (
                            <p className="mb-2">
                                This toolset is currently used by <span className="font-semibold">{toolset.agents.length} agent(s)</span>. Deleting it will remove the toolset from those agents.
                            </p>
                        )}
                        The toolset <span className="font-semibold">{toolset.name}</span> and all its associated tools will be permanently deleted.
                    </div>
                }
                onOpenChange={setConfirmDeleteOpen}
                onCancel={() => setConfirmDeleteOpen(false)}
                onConfirm={() => {
                    handleDeleteCustomToolset(toolset);
                    setConfirmDeleteOpen(false);
                }}
                actionLabels={{
                    confirm: "Delete",
                }}
            />
        </>
    );
};
