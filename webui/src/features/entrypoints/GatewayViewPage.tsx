// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import type { ReactNode } from "react";

import { findGatewayBadge, useCapabilities } from "@/api/capabilities";
import { gatewaysApi, useDeleteGatewayMutation } from "@/api/gateways";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { PageContentWrapper, PageFooter } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { entityNeedsCleanupOnDelete, isDeployFailed, isEntityErrorState } from "@/utils/deployment";

import { Button } from "@/ui/button";
import { LifecycleBadge } from "@/ui/lifecycleBadge";
import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";
import type { Gateway } from "@/types/models";

import { ConfirmationDialog, EmptyState, ErrorDialog, Header, PageLayout } from "@/common";

import { ConsumeOnlyEntrypointNotice } from "./ConsumeOnlyEntrypointNotice";
import { googleChatPullsEvents } from "./customEndpoint";
import { GatewayDeploymentDialog } from "./GatewayDeploymentDialog";
import { GatewayDeploymentView } from "./GatewayDeploymentView";
import { GatewayNetworkView } from "./GatewayNetworkView";
import { GatewayView } from "./GatewayView";
import { useGatewayDeployment, useGatewayNetworkingDetails } from "./hooks";
import { isConsumeOnlyEventMeshEntrypoint } from "./isConsumeOnlyEventMeshEntrypoint";

const gatewayHasNetworkTab = (gateway: Pick<Gateway, "type" | "values">): boolean => {
    if (gateway.type === "google_chat") {
        return !googleChatPullsEvents(gateway.values);
    }
    return gateway.type === "teams" || gateway.type === "whatsapp";
};

export function GatewayViewPage() {
    const { navigate, addNotification, useLoaderData, gatewaySchemas } = useChatContext();
    const { data: capabilities } = useCapabilities();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { gatewayId } = useLoaderData();
    const { deploy, error, setError } = useGatewayDeployment();
    const { mutateAsync: deleteGatewayAsync } = useDeleteGatewayMutation();
    const networkingInfo = useGatewayNetworkingDetails(gatewayId);

    const [gateway, setGateway] = useState<Gateway | null>(null);
    const [isLoading, setIsLoading] = useState(true);
    const [loadError, setLoadError] = useState<{ title: string; message: string } | null>(null);
    const [deploymentDialogOpen, setDeploymentDialogOpen] = useState<boolean>(false);
    const [deploymentAction, setDeploymentAction] = useState<"deploy" | "undeploy" | "update" | null>(null);
    const [currentTab, setCurrentTab] = useState("config");
    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
    const [deleteError, setDeleteError] = useState<string | null>(null);

    const handleEdit = () => {
        navigate(`/entrypoints/${gatewayId}/edit`);
    };

    const handleDelete = useCallback(async () => {
        if (!gateway?.id) return;
        try {
            await deleteGatewayAsync(gateway.id);
            addNotification("Entrypoint deleted", "success");
            navigate("/entrypoints");
        } catch (err) {
            setDeleteError(getErrorMessage(err, "An error occurred while deleting the entrypoint"));
        } finally {
            setConfirmDeleteOpen(false);
        }
    }, [gateway?.id, deleteGatewayAsync, addNotification, navigate]);

    useEffect(() => {
        const loadGateway = async () => {
            if (!gatewayId) return;

            setIsLoading(true);
            try {
                const gateway = await gatewaysApi.get(gatewayId);
                setGateway(gateway);
            } catch (err) {
                setLoadError({
                    title: "Failed to Load Entrypoint",
                    message: getErrorMessage(err, "An error occurred while loading the entrypoint"),
                });
            } finally {
                setIsLoading(false);
            }
        };

        loadGateway();
    }, [gatewayId]);

    useEffect(() => {
        if (gateway && currentTab === "network" && !gatewayHasNetworkTab(gateway)) {
            setCurrentTab("config");
        }
    }, [gateway, currentTab]);

    const schemaInfo = useMemo(() => {
        if (!gateway || !gatewaySchemas) return null;
        return gatewaySchemas.find(s => s.type === gateway.type);
    }, [gateway, gatewaySchemas]);

    const openDeploymentDialog = useCallback((action: "deploy" | "undeploy" | "update") => {
        setDeploymentAction(action);
        setDeploymentDialogOpen(true);
    }, []);

    const handleDeploy = useCallback(async () => {
        if (gateway?.id && deploymentAction) {
            const actionLabel = deploymentAction === "deploy" ? "Deployment" : deploymentAction === "undeploy" ? "Undeployment" : "Update";

            await deploy({
                gatewayId: gateway.id,
                action: deploymentAction,
                onSuccess: async () => {
                    addNotification(`${actionLabel} started successfully.`);
                    navigate("/entrypoints");
                },
            });
        }
    }, [gateway, deploymentAction, deploy, addNotification, navigate]);

    const tabs = useMemo(() => {
        const baseTabs = [{ id: "config", label: "Configuration", isActive: currentTab === "config", onClick: () => setCurrentTab("config") }];

        if (gateway && gatewayHasNetworkTab(gateway)) {
            baseTabs.push({
                id: "network",
                label: "Network",
                isActive: currentTab === "network",
                onClick: () => setCurrentTab("network"),
            });
        }

        if (userHasCapability(CAPABILITIES.readGatewayDeployments)) {
            baseTabs.push({ id: "deployment", label: "Deployment History", isActive: currentTab === "deployment", onClick: () => setCurrentTab("deployment") });
        }

        return baseTabs;
    }, [currentTab, gateway, userHasCapability, CAPABILITIES]);

    const headerButtons = useMemo(() => {
        const buttons: ReactNode[] = [];

        if (gateway) {
            const isDeployFailedState = isDeployFailed(gateway?.deploymentStatus);
            if (userHasCapability(CAPABILITIES.deployGateways)) {
                let action: "deploy" | "undeploy" | "update";
                let title: string;

                if (isDeployFailedState) {
                    action = "update";
                    title = "Redeploy";
                } else if (gateway.syncStatus === "out_of_sync") {
                    action = "update";
                    title = "Deploy Updates";
                } else if (gateway.deployed) {
                    action = "undeploy";
                    title = "Undeploy";
                } else {
                    action = "deploy";
                    title = "Deploy";
                }

                buttons.push(
                    <Button key="deploy" variant="outline" onClick={() => openDeploymentDialog(action)} data-testid="deployGatewayButton" title={title}>
                        {title}
                    </Button>
                );
            }
            if (userHasCapability(CAPABILITIES.updateGateways)) {
                buttons.push(
                    <Button key="edit" variant="ghost" onClick={handleEdit} data-testid="editGatewayButton" title="Edit Entrypoint">
                        <Pencil />
                        Edit
                    </Button>
                );
            }

            const canDelete = userHasCapability(CAPABILITIES.deleteGateways) && (isEntityErrorState(gateway) || !gateway.deployed);
            if (canDelete) {
                const menuActions: MenuAction[] = [{ id: "delete", label: "Delete", onClick: () => setConfirmDeleteOpen(true), icon: <Trash2 />, iconPosition: "left" }];
                buttons.push(
                    <Popover key="more-actions">
                        <PopoverTrigger asChild>
                            <Button variant="ghost" size="icon" data-testid="moreActionsButton" title="More Actions">
                                <MoreHorizontal />
                            </Button>
                        </PopoverTrigger>
                        <PopoverContent align="end" side="bottom" className="w-auto" sideOffset={0}>
                            <Menu actions={menuActions} />
                        </PopoverContent>
                    </Popover>
                );
            }
        }

        return buttons;
    }, [gateway, CAPABILITIES, handleEdit, openDeploymentDialog, userHasCapability]);

    if (gateway && isConsumeOnlyEventMeshEntrypoint(gateway)) {
        return <ConsumeOnlyEntrypointNotice gatewayName={gateway.name} />;
    }

    if (gateway && schemaInfo && !(loadError || isLoading)) {
        const gatewayBadge = findGatewayBadge(capabilities, gateway.type);
        const viewTitle = gatewayBadge ? (
            <span className="flex items-center gap-2">
                {gateway.name}
                <LifecycleBadge>{gatewayBadge}</LifecycleBadge>
            </span>
        ) : (
            gateway.name
        );
        return (
            <PageLayout>
                <Header title={viewTitle} breadcrumbs={[{ label: "Entrypoints", onClick: () => navigate("/entrypoints") }, { label: gateway.name }]} tabs={tabs} buttons={headerButtons} />

                <div className="relative min-h-0 flex-1 overflow-y-auto">
                    {currentTab === "config" && (
                        <PageContentWrapper className="flex min-h-full flex-col" innerClassName="flex-1">
                            <GatewayView gateway={gateway} schemaInfo={schemaInfo} networkingInfo={gatewayHasNetworkTab(gateway) ? undefined : networkingInfo} addNotification={addNotification} />
                        </PageContentWrapper>
                    )}

                    {currentTab === "network" && gatewayHasNetworkTab(gateway) && (
                        <PageContentWrapper>
                            <GatewayNetworkView networkingInfo={networkingInfo} gatewayType={gateway.type} addNotification={addNotification} />
                        </PageContentWrapper>
                    )}

                    {currentTab === "deployment" && (
                        <PageContentWrapper>
                            <GatewayDeploymentView gateway={gateway} />
                        </PageContentWrapper>
                    )}
                </div>

                {error && <ErrorDialog title="Entrypoint Deployment Error" open={!!error} error={error} errorSource="api" onOpenChange={open => !open && setError(null)} />}

                <ConfirmationDialog
                    title="Delete Entrypoint"
                    content={
                        entityNeedsCleanupOnDelete(gateway) ? (
                            <span>
                                Are you sure you want to delete the entrypoint <span className="font-semibold">{gateway.name}</span>? A cleanup will be attempted to remove any remaining deployment resources. This action cannot be undone.
                            </span>
                        ) : (
                            <span>
                                Are you sure you want to delete the entrypoint <span className="font-semibold">{gateway.name}</span>? This action cannot be undone.
                            </span>
                        )
                    }
                    open={confirmDeleteOpen}
                    onOpenChange={setConfirmDeleteOpen}
                    onCancel={() => setConfirmDeleteOpen(false)}
                    onConfirm={handleDelete}
                    actionLabels={{ confirm: "Delete" }}
                />
                <ErrorDialog open={!!deleteError} title="Entrypoint Deletion Error" error={deleteError ?? ""} errorSource="api" onOpenChange={open => !open && setDeleteError(null)} />

                {gateway && deploymentAction && (
                    <GatewayDeploymentDialog
                        open={deploymentDialogOpen}
                        gatewayName={gateway.name}
                        gateway={gateway}
                        action={deploymentAction}
                        onConfirm={() => {
                            handleDeploy();
                            setDeploymentDialogOpen(false);
                        }}
                        onClose={() => setDeploymentDialogOpen(false)}
                    />
                )}

                <PageFooter>
                    <Button variant="outline" testid="closeButton" title="Close" onClick={() => navigate("/entrypoints")}>
                        Close
                    </Button>
                </PageFooter>
            </PageLayout>
        );
    } else {
        return (
            <PageLayout>
                <Header title="Entrypoint" breadcrumbs={[{ label: "Entrypoints", onClick: () => navigate("/entrypoints") }]} />
                {loadError && (
                    <EmptyState
                        variant="error"
                        title={loadError.title}
                        buttons={[
                            {
                                text: "Go To Entrypoints",
                                variant: "default",
                                onClick: () => navigate("/entrypoints"),
                            },
                        ]}
                    />
                )}
                {isLoading && <EmptyState variant="loading" title="Loading..." />}
                {gateway && gatewaySchemas.length > 0 && !schemaInfo && !loadError && !isLoading && (
                    <EmptyState
                        variant="notFound"
                        title="This entrypoint type is not available on this deployment."
                        buttons={[
                            {
                                text: "Go To Entrypoints",
                                variant: "default",
                                onClick: () => navigate("/entrypoints"),
                            },
                        ]}
                    />
                )}
            </PageLayout>
        );
    }
}
