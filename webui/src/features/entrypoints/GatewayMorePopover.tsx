// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";

import { gatewaysApi } from "@/api/gateways";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";
import { entityNeedsCleanupOnDelete, getEntityDeploymentActions } from "@/utils/deployment";

import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";
import type { Gateway } from "@/types/models";

import { ConfirmationDialog, ErrorDialog } from "@/common";

import { GatewayDeploymentDialog } from "./GatewayDeploymentDialog";
import { useGatewayDeployment } from "./hooks";

interface GatewayMorePopoverProps {
    gateway: Gateway;
    onEdit: () => void;
    onOpenGateway: () => void;
    onUpdate: () => void;
    onGatewayDeleted?: () => void;
    children: React.ReactNode;
}

export const GatewayMorePopover = ({ gateway, onEdit, onOpenGateway, onUpdate, onGatewayDeleted, children }: GatewayMorePopoverProps) => {
    const { addNotification } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { deploy, error, setError } = useGatewayDeployment();

    const canUpdateGateways = userHasCapability(CAPABILITIES.updateGateways);
    const canDeleteGateways = userHasCapability(CAPABILITIES.deleteGateways);
    const canDeployGateways = userHasCapability(CAPABILITIES.deployGateways);

    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
    const [deploymentDialogOpen, setDeploymentDialogOpen] = useState<boolean>(false);
    const [deploymentAction, setDeploymentAction] = useState<"deploy" | "undeploy" | "update" | null>(null);
    const [menuOpen, setMenuOpen] = useState<boolean>(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);

    const openDeploymentDialog = useCallback((action: "deploy" | "undeploy" | "update") => {
        setDeploymentAction(action);
        setDeploymentDialogOpen(true);
        setMenuOpen(false);
    }, []);

    const handleDeploy = useCallback(async () => {
        if (gateway?.id && deploymentAction) {
            const successLabel = deploymentAction === "deploy" ? "Deployed" : deploymentAction === "undeploy" ? "Undeployed" : "Updated";

            await deploy({
                gatewayId: gateway.id,
                action: deploymentAction,
                onSuccess: async () => {
                    addNotification(`${successLabel} successfully.`, "success");
                    setDeploymentDialogOpen(false);
                    onUpdate?.();
                },
            });
        }
    }, [gateway, deploymentAction, deploy, addNotification, onUpdate]);

    const handleDelete = async () => {
        try {
            await gatewaysApi.delete(gateway.id!);
            addNotification("Entrypoint deleted", "success");
            if (onGatewayDeleted) {
                onGatewayDeleted();
            } else {
                onUpdate();
            }
        } catch (err) {
            setDeleteError(getErrorMessage(err, "An error occurred while deleting the entrypoint"));
        } finally {
            setConfirmDeleteOpen(false);
        }
    };

    const menuActions = useMemo((): MenuAction[] => {
        const actions: MenuAction[] = [];

        actions.push({ id: "openGateway", label: "Open Entrypoint", onClick: onOpenGateway });

        if (canUpdateGateways) {
            actions.push({ id: "edit", label: "Edit", onClick: onEdit });
        }

        if (canDeployGateways) {
            for (const { action, label } of getEntityDeploymentActions(gateway)) {
                actions.push({ id: action, label, onClick: () => openDeploymentDialog(action), divider: true });
            }
        }

        if (canDeleteGateways) {
            actions.push({ id: "delete", label: "Delete", onClick: () => setConfirmDeleteOpen(true), divider: true });
        }

        return actions;
    }, [gateway, canUpdateGateways, canDeleteGateways, canDeployGateways, onOpenGateway, onEdit, openDeploymentDialog]);

    if (menuActions.length === 0) {
        return null;
    }

    return (
        <>
            <Popover open={menuOpen} onOpenChange={setMenuOpen}>
                <PopoverTrigger onClick={e => e.stopPropagation()} asChild>
                    {children}
                </PopoverTrigger>
                <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0} onClick={e => e.stopPropagation()}>
                    <Menu actions={menuActions} />
                </PopoverContent>
            </Popover>

            {gateway && confirmDeleteOpen && (
                <ConfirmationDialog
                    open={confirmDeleteOpen}
                    title="Delete Entrypoint"
                    content={
                        <div>
                            {entityNeedsCleanupOnDelete(gateway) ? (
                                <div data-testid="gatewayStuckDeleteMessage">
                                    Are you sure you want to delete the entrypoint <span className="font-semibold">{gateway.name}</span>? A cleanup will be attempted to remove any remaining deployment resources. This action cannot be undone.
                                </div>
                            ) : (
                                <div data-testid="gatewayDeleteMessage">
                                    Are you sure you want to delete the entrypoint <span className="font-semibold">{gateway.name}</span>? This action cannot be undone.
                                </div>
                            )}
                        </div>
                    }
                    onOpenChange={setConfirmDeleteOpen}
                    onCancel={() => setConfirmDeleteOpen(false)}
                    onConfirm={handleDelete}
                    actionLabels={{
                        confirm: "Delete",
                    }}
                />
            )}

            {gateway && deploymentAction && (
                <GatewayDeploymentDialog
                    open={deploymentDialogOpen}
                    gatewayName={gateway.name}
                    gateway={gateway}
                    action={deploymentAction}
                    onConfirm={() => {
                        handleDeploy();
                    }}
                    onClose={() => setDeploymentDialogOpen(false)}
                />
            )}

            <ErrorDialog open={!!deleteError} title="Entrypoint Deletion Error" error={deleteError ?? ""} errorSource="api" onOpenChange={open => !open && setDeleteError(null)} />
            <ErrorDialog open={!!error} title="Entrypoint Deployment Error" error={error ?? ""} errorSource="api" onOpenChange={open => !open && setError(null)} />
        </>
    );
};
