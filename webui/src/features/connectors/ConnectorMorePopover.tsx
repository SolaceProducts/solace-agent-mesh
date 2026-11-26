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
import type { Connector } from "@/types";

interface ConnectorMorePopoverProps {
    connector: Connector;
    children: React.ReactNode;
}

export const ConnectorMorePopover: React.FC<ConnectorMorePopoverProps> = ({ connector, children }) => {
    const { handleDeleteConnector, handleEditConnector } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);

    const menuActions: MenuAction[] = [];
    if (userHasCapability(CAPABILITIES.updateConnectors)) {
        menuActions.push({
            id: "edit",
            label: "Edit",
            onClick: () => {
                handleEditConnector(connector);
            },
            icon: <Pencil />,
            iconPosition: "left",
        });
    }
    if (userHasCapability(CAPABILITIES.deleteConnectors)) {
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
                <PopoverTrigger onClick={e => e.stopPropagation()} asChild>
                    {children}
                </PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={menuActions} />
                </PopoverContent>
            </Popover>
            <ConfirmationDialog
                open={confirmDeleteOpen}
                title={`Delete ${connector.name}`}
                content={
                    <div>
                        {connector.agentCount && connector.agentCount > 0 && (
                            <p className="mb-2">
                                This connector is currently used by <span className="font-semibold">{connector.agentCount} agent(s)</span>. Deleting it will remove the connector from those agents and trigger a redeployment for any that are currently
                                deployed.
                            </p>
                        )}
                        Are you sure you want to delete the connector <span className="font-semibold">{connector.name}</span>? This action cannot be undone.
                    </div>
                }
                onOpenChange={setConfirmDeleteOpen}
                onCancel={() => setConfirmDeleteOpen(false)}
                onConfirm={async () => {
                    await handleDeleteConnector(connector);
                }}
                actionLabels={{
                    confirm: "Delete",
                }}
            />
        </>
    );
};
