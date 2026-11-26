// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import { MoreHorizontal, Pencil, Trash2 } from "lucide-react";
import type { ReactNode } from "react";

import type { AgentCardResponse, RemoteAgentResponse } from "@/api/remote-agents";
import { remoteAgentsApi, transformRemoteAgent, useRemoteAgents } from "@/api/remote-agents";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { PageContentWrapper, PageFooter } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Menu, type MenuAction } from "@/ui/menu";
import { Popover, PopoverContent, PopoverTrigger } from "@/ui/popover";

import { DeleteAgentConfirmationDialog } from "@/features/agents/DeleteAgentConfirmationDialog";
import { DuplicateNameWarningBanner } from "@/features/agents/DuplicateNameWarningBanner";
import { useDuplicateDisplayName } from "@/features/agents/hooks";

import { EmptyState, ErrorDialog, Header, MessageBanner, PageLayout } from "@/common";

import { AgentDeploymentDialog } from "./AgentDeploymentDialog";
import { DeploymentStatus, getAgentToDisplay, SyncStatusTooltip } from "./common";
import { transformRemoteAgentToFormData } from "./external/agentDataTransform";
import { RemoteAgentViewContent } from "./external/RemoteAgentViewContent";
import { CardRefusedBanner } from "./external/RemoteProtocolDetails";
import { useRemoteAgentDeployment } from "./hooks/useRemoteAgentDeployment";
import { agentManagementTabFor, agentManagementUrl } from "./utils/filters";

export function RemoteAgentViewPage() {
    const { navigate, useLoaderData, handleDeleteAgent } = useChatContext();
    const { data: remoteAgents = [] } = useRemoteAgents();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { remoteAgentId } = useLoaderData();
    const { handleDeployRemoteAgent, error: deployError, setError: setDeployError } = useRemoteAgentDeployment();

    const [isLoadingAgent, setIsLoadingAgent] = useState<boolean>(true);
    const [remoteAgent, setRemoteAgent] = useState<RemoteAgentResponse | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [deploymentDialogOpen, setDeploymentDialogOpen] = useState<boolean>(false);
    const [deploymentAction, setDeploymentAction] = useState<"deploy" | "undeploy">("deploy");
    const [fetchedAgentCard, setFetchedAgentCard] = useState<AgentCardResponse | null>(null);
    const [isLoadingAgentCard, setIsLoadingAgentCard] = useState<boolean>(false);
    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState<boolean>(false);
    const [cardFetchError, setCardFetchError] = useState<string | null>(null);

    useEffect(() => {
        if (remoteAgentId) {
            setIsLoadingAgent(true);
            setIsLoadingAgentCard(true);
            setFetchedAgentCard(null);
            setCardFetchError(null);
            remoteAgentsApi
                .get(remoteAgentId, true)
                .then(response => {
                    if (!response) {
                        throw new Error("Remote agent data is undefined");
                    }
                    setRemoteAgent(response);

                    if (response.cardData) {
                        setFetchedAgentCard(response.cardData);
                    } else if (response.cardFetchError) {
                        console.warn("Failed to fetch agent card (external name may not be available):", response.cardFetchError);
                        setCardFetchError(response.cardFetchError);
                        setFetchedAgentCard(null);
                    }
                })
                .catch(err => {
                    console.error("Failed to load remote agent:", err);
                    setError(getErrorMessage(err, "Could not load remote agent data."));
                })
                .finally(() => {
                    setIsLoadingAgent(false);
                    setIsLoadingAgentCard(false);
                });
        }
    }, [remoteAgentId]);

    const handleEdit = useCallback(() => {
        if (remoteAgentId) {
            navigate(`/agents/external/${remoteAgentId}/edit`);
        }
    }, [remoteAgentId, navigate]);

    const remoteAgentForActions = useMemo(() => {
        return remoteAgents.find(ra => ra.id === remoteAgentId);
    }, [remoteAgents, remoteAgentId]);

    const isDeployed = remoteAgentForActions?.deploymentStatus === "deployed";
    const remoteProtocol = isDeployed && remoteAgent?.deploymentStatus === "deployed" ? (remoteAgent.remoteProtocol ?? undefined) : undefined;

    const handleAgentDeleted = useCallback(() => {
        navigate("/agents");
    }, [navigate]);

    const handleDelete = useCallback(async () => {
        if (!remoteAgentForActions) return;
        const { deployed, ...agentProps } = remoteAgentForActions;
        const agent = {
            ...agentProps,
            description: remoteAgentForActions.description || "",
        };
        const deleted = await handleDeleteAgent(agent);
        if (deleted) handleAgentDeleted();
    }, [remoteAgentForActions, handleDeleteAgent, handleAgentDeleted]);

    const agentCardInfo = useMemo(() => {
        if (!remoteAgentForActions) return null;
        return transformRemoteAgent(remoteAgentForActions);
    }, [remoteAgentForActions]);

    const { checkAgentDuplicates } = useDuplicateDisplayName();
    const duplicateInfo = agentCardInfo ? checkAgentDuplicates(agentCardInfo) : { hasDuplicate: false, displayName: null, duplicateCount: 0, duplicateAgentIds: [] };

    const openDeploymentDialog = useCallback(
        (action: "deploy" | "undeploy") => {
            if (remoteAgentForActions) {
                setDeploymentAction(action);
                setDeploymentDialogOpen(true);
            }
        },
        [remoteAgentForActions]
    );

    const onDeploy = useCallback(async () => {
        if (remoteAgentForActions) {
            const success = await handleDeployRemoteAgent(remoteAgentForActions, deploymentAction);
            if (!success) return;
            setDeploymentDialogOpen(false);
            const tab = deploymentAction === "undeploy" ? "undeployed" : "deployed";
            navigate(agentManagementUrl(remoteAgentForActions.id, tab));
        }
    }, [remoteAgentForActions, deploymentAction, handleDeployRemoteAgent, navigate]);

    const handleClose = useCallback(() => {
        if (remoteAgentForActions) {
            navigate(agentManagementUrl(remoteAgentForActions.id, agentManagementTabFor(remoteAgentForActions.deploymentStatus, remoteAgentForActions.runtimeStatus, "external")));
        } else {
            navigate("/agents");
        }
    }, [remoteAgentForActions, navigate]);

    const { connectFormData, additionalInfoFormData } = useMemo(() => {
        if (!remoteAgent) {
            return { connectFormData: {}, additionalInfoFormData: {} };
        }
        try {
            return transformRemoteAgentToFormData(remoteAgent);
        } catch (err) {
            console.error("Failed to transform remote agent data:", err);
            return { connectFormData: {}, additionalInfoFormData: {} };
        }
    }, [remoteAgent]);

    const deployButtons = useMemo<ReactNode[]>(() => {
        if (!remoteAgentForActions || !userHasCapability(CAPABILITIES.deployAgents)) {
            return [];
        }

        const isDeployed = remoteAgentForActions.deploymentStatus === "deployed";
        const isOutOfSync = remoteAgentForActions.syncStatus === "out_of_sync";

        if (!isDeployed) {
            const isFailedState = remoteAgentForActions.deploymentStatus === "deploy_failed";
            const deployTitle = (() => {
                if (isFailedState) return "Redeploy";
                if (isOutOfSync) return "Deploy Updates";
                return "Deploy";
            })();
            return [
                <Button key="deploy" variant="outline" onClick={() => openDeploymentDialog("deploy")} data-testid="deployRemoteAgentButton" title={deployTitle}>
                    {deployTitle}
                </Button>,
            ];
        }

        const buttons: ReactNode[] = [];
        if (isOutOfSync) {
            buttons.push(
                <Button key="update" variant="outline" onClick={() => openDeploymentDialog("deploy")} data-testid="deployRemoteAgentButton" title="Deploy Updates">
                    Deploy Updates
                </Button>
            );
        }
        buttons.push(
            <Button key="undeploy" variant="outline" onClick={() => openDeploymentDialog("undeploy")} data-testid="undeployRemoteAgentButton" title="Undeploy">
                Undeploy
            </Button>
        );
        return buttons;
    }, [remoteAgentForActions, openDeploymentDialog, userHasCapability, CAPABILITIES]);

    const headerButtons = useMemo(() => {
        if (!remoteAgent || !remoteAgentForActions) return [];

        const isDeployed = remoteAgentForActions.deploymentStatus === "deployed";
        const canDelete = !isDeployed && userHasCapability(CAPABILITIES.deleteAgents);

        const buttons: ReactNode[] = [...deployButtons];
        if (userHasCapability(CAPABILITIES.updateAgents)) {
            buttons.push(
                <Button key="edit" variant="ghost" onClick={handleEdit} data-testid="editRemoteAgentButton" title="Edit Agent">
                    <Pencil />
                    Edit
                </Button>
            );
        }

        if (canDelete) {
            const menuActions: MenuAction[] = [
                {
                    id: "delete",
                    label: "Delete",
                    onClick: () => setConfirmDeleteOpen(true),
                    icon: <Trash2 />,
                    iconPosition: "left",
                },
            ];

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

        return buttons;
    }, [remoteAgent, remoteAgentForActions, deployButtons, handleEdit, userHasCapability, CAPABILITIES]);

    return (
        <PageLayout>
            <Header
                title={remoteAgent?.name || "N/A"}
                breadcrumbs={[
                    {
                        label: "Agents",
                        onClick: handleClose,
                    },
                    { label: remoteAgent?.name || "N/A" },
                ]}
                buttons={headerButtons}
            />
            {remoteAgent ? (
                <>
                    <PageContentWrapper>
                        {remoteAgentForActions?.syncStatus === "out_of_sync" && <MessageBanner variant="info" className="mb-6" message="There are undeployed changes for this agent. Please review and deploy these updates." />}
                        {cardFetchError && (
                            <MessageBanner
                                variant="warning"
                                className="mb-6"
                                message="Agent card metadata could not be loaded from the external source. The agent display name and description may not be available."
                                onDismiss={() => setCardFetchError(null)}
                            />
                        )}
                        {duplicateInfo.hasDuplicate && duplicateInfo.displayName && (
                            <DuplicateNameWarningBanner agentName={duplicateInfo.displayName} count={duplicateInfo.duplicateCount} duplicateAgentIds={duplicateInfo.duplicateAgentIds} dismissible className="mb-6" />
                        )}
                        <CardRefusedBanner status={remoteProtocol} className="mb-6" />
                        {agentCardInfo && (
                            <div className="mb-6 flex items-center gap-2">
                                <span className="text-sm font-medium">Status:</span>
                                <DeploymentStatus agent={isDeployed ? getAgentToDisplay(agentCardInfo, "active") : agentCardInfo} selectedState={isDeployed ? "active" : "inactive"} />
                                {userHasCapability(CAPABILITIES.readAgents) && <SyncStatusTooltip deployedAgent={agentCardInfo} />}
                            </div>
                        )}
                        <RemoteAgentViewContent
                            formData={connectFormData}
                            additionalInfoFormData={additionalInfoFormData}
                            fetchedAgentCard={fetchedAgentCard}
                            isLoadingAgentCard={isLoadingAgentCard}
                            remoteProtocol={remoteProtocol}
                            showDeployedStatus={isDeployed}
                        />
                    </PageContentWrapper>
                    {error && <ErrorDialog title="Remote Agent Error" open={!!error} error={error} errorSource="api" onOpenChange={open => !open && setError(null)} />}
                    {deployError && <ErrorDialog title="Deployment Error" open={!!deployError} error={deployError} errorSource="api" onOpenChange={open => !open && setDeployError(null)} />}
                    <DeleteAgentConfirmationDialog open={confirmDeleteOpen} onOpenChange={setConfirmDeleteOpen} agentName={remoteAgent.name} title="Delete Agent" onConfirm={handleDelete} />
                    {remoteAgentForActions && (
                        <AgentDeploymentDialog
                            open={deploymentDialogOpen}
                            agentName={remoteAgent.name}
                            agent={{
                                id: remoteAgentForActions.id,
                                name: remoteAgentForActions.name,
                                description: remoteAgentForActions.description || "",
                                skills: remoteAgentForActions.skills,
                                inputModes: remoteAgentForActions.inputModes,
                                outputModes: remoteAgentForActions.outputModes,
                                deploymentStatus: remoteAgentForActions.deploymentStatus,
                                syncStatus: remoteAgentForActions.syncStatus,
                            }}
                            action={deploymentAction}
                            onConfirm={() => {
                                onDeploy();
                            }}
                            onClose={() => setDeploymentDialogOpen(false)}
                        />
                    )}
                </>
            ) : null}
            {isLoadingAgent && <EmptyState variant="loading" title="Loading..." />}
            {!isLoadingAgent && !remoteAgent && <EmptyState variant="error" title="Remote Agent Not Found" buttons={[{ text: "Go To Agents", variant: "default", onClick: handleClose }]} />}
            <PageFooter>
                <Button variant="outline" testid="closeButton" title="Close" onClick={handleClose}>
                    Close
                </Button>
            </PageFooter>
        </PageLayout>
    );
}
