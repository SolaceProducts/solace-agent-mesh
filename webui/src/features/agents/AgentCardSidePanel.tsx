// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useMemo, useState } from "react";
import { X } from "lucide-react";

import { useModelConfigs } from "@/api/models/hooks";
import { type ModelConfig } from "@/api/models/types";
import { useRemoteAgents } from "@/api/remote-agents";
import { useBuiltinSkills, useSkills } from "@/api/skills";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { useAgentSelection } from "@/hooks/useAgentSelection";
import { useInvokeScope } from "@/hooks/useInvokeScope";

import { isDeployFailed } from "@/utils/deployment";

import { AccordionCard } from "@/ui/accordion-card";
import { Button } from "@/ui/button";

import { DuplicateNameWarningBanner } from "@/features/agents/DuplicateNameWarningBanner";
import { useDuplicateDisplayName } from "@/features/agents/hooks";

import { ErrorDialog, MessageBanner, Metadata } from "@/common";
import type { AgentCardInfo, AgentState } from "@/types";

import { AgentAttachedSkillsSection } from "./AgentAttachedSkillsSection";
import { AgentCardComparisonDialog } from "./AgentCardComparisonDialog";
import { AgentAbilitiesSection, AgentConnectorsSection, AgentToolsetsSection } from "./AgentCardSections";
import { AgentCommunicationMode } from "./AgentCommunicationMode";
import { AgentDeploymentDialog } from "./AgentDeploymentDialog";
import { AgentModelCard } from "./AgentModelCard";
import { DeploymentStatus, getAgentToDisplay, RegisteredStatusWithTooltip } from "./common";
import { DeleteAgentConfirmationDialog } from "./DeleteAgentConfirmationDialog";
import { CardRefusedBanner, RemoteInterfaceInUse } from "./external/RemoteProtocolDetails";
import { useAgentDeployment, useAgentLookup, useManageAgentHandler, useRemoteAgentDeployment } from "./hooks";
import { getAgentTypeLabel } from "./utils/agentType";
import { isRemoteNotRunning } from "./utils/filters";

interface AgentCardSidePanelProps {
    agent: AgentCardInfo;
    selectedState?: AgentState;
    onClose?: () => void;
    onDeployed?: (agentName: string) => void;
    onDeploymentChanged?: () => void;
    onAgentDeleted?: () => void;
    viewOnly?: boolean;
}

export const AgentCardSidePanel: React.FC<AgentCardSidePanelProps> = ({ agent, selectedState, onClose, onDeployed, onDeploymentChanged, onAgentDeleted, viewOnly = false }) => {
    const { handleDeleteAgent, handleViewAgent } = useChatContext();
    const { data: remoteAgents = [] } = useRemoteAgents();
    const { data: customSkills } = useSkills();
    const { data: builtinSkills } = useBuiltinSkills();
    const { data: models } = useModelConfigs();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { chatWithAgent } = useAgentSelection();
    const { handleDeploy, error, setError } = useAgentDeployment();
    const { handleDeployRemoteAgent, error: remoteDeploymentError, setError: setRemoteDeploymentError } = useRemoteAgentDeployment();
    const [isComparisonOpen, setIsComparisonOpen] = useState(false);
    const [deploymentDialogOpen, setDeploymentDialogOpen] = useState<boolean>(false);
    const [deploymentAction, setDeploymentAction] = useState<"deploy" | "undeploy" | "update">("deploy");
    const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
    const canReadAgents = userHasCapability(CAPABILITIES.readAgents);
    const canDeployAgents = userHasCapability(CAPABILITIES.deployAgents);
    const canDeleteAgents = userHasCapability(CAPABILITIES.deleteAgents);

    const agentToDisplay = getAgentToDisplay(agent, selectedState ?? "inactive");
    const userAgent = useAgentLookup(agent.displayName);
    const handleManageAgent = useManageAgentHandler(userAgent?.id);
    const invokeScope = useInvokeScope("agent", userAgent?.id);

    const selectedModel = useMemo<ModelConfig | null>(() => {
        if (!models) return null;
        const ref = userAgent?.modelProvider?.[0];
        if (!ref) return null;
        return models.find(m => m.id === ref || m.alias === ref) ?? null;
    }, [userAgent?.modelProvider, models]);

    const remoteAgent = useMemo(() => {
        if (agent.type === "external" && userAgent?.id) {
            return remoteAgents.find(ra => ra.id === userAgent.id);
        }
        return undefined;
    }, [agent.type, userAgent?.id, remoteAgents]);

    const displayOutOfSyncInformation = useMemo(() => {
        return agentToDisplay.syncStatus === "out_of_sync" && canDeployAgents;
    }, [agentToDisplay, canDeployAgents]);

    const isDeployFailedState = useMemo(() => {
        return isDeployFailed(agentToDisplay.status);
    }, [agentToDisplay.status]);

    const isStoppedState = agentToDisplay.status === "stopped";

    const isErrorState = (isDeployFailedState || isStoppedState) && !isRemoteNotRunning(agentToDisplay);

    const { checkAgentDuplicates } = useDuplicateDisplayName();
    const duplicateInfo = checkAgentDuplicates(agent);

    const handleOpen = useCallback(() => {
        if (userAgent) {
            handleViewAgent(userAgent);
        }
    }, [userAgent, handleViewAgent]);

    const handleDelete = useCallback(async () => {
        if (!userAgent) return;
        const deleted = await handleDeleteAgent(userAgent);
        if (deleted) onAgentDeleted?.();
    }, [userAgent, handleDeleteAgent, onAgentDeleted]);

    const handleChatWithAgent = useCallback(() => {
        if (selectedState === "active" && agent) {
            chatWithAgent(agent.name);
        }
    }, [agent, selectedState, chatWithAgent]);

    const openDeploymentDialog = useCallback((action: "deploy" | "undeploy" | "update") => {
        setDeploymentAction(action);
        setDeploymentDialogOpen(true);
    }, []);

    const handleViewUpdate = useCallback(() => {
        if (agentToDisplay?.type === "user") {
            setIsComparisonOpen(true);
        } else if (agentToDisplay?.type === "external") {
            if (agent.deployed) {
                setIsComparisonOpen(true);
            } else {
                openDeploymentDialog("deploy");
            }
        }
    }, [agentToDisplay, agent.deployed, openDeploymentDialog]);

    const onDeploy = useCallback(
        async (action: "deploy" | "undeploy" | "update") => {
            if (!userAgent) return;
            let success = false;
            if (agent.type === "external") {
                const remoteAgent = remoteAgents.find(ra => ra.id === userAgent.id);
                if (remoteAgent) {
                    success = await handleDeployRemoteAgent(remoteAgent, action === "update" ? "deploy" : action);
                }
            } else {
                success = await handleDeploy(userAgent, action);
            }
            if (!success) return;
            if (action !== "undeploy") onDeployed?.(agent.name);
            onDeploymentChanged?.();
        },
        [userAgent, agent.type, agent.name, handleDeploy, handleDeployRemoteAgent, remoteAgents, onDeployed, onDeploymentChanged]
    );

    const renderLifecycleBanner = (agentToDisplay: AgentCardInfo) => {
        if (viewOnly) return null;
        if (isErrorState) {
            const errorWithOutOfSync = isDeployFailedState
                ? `A previous deployment failed for ${agentToDisplay.displayName}. There are also undeployed changes.`
                : `${agentToDisplay.displayName} is deployed but not running. There are also undeployed changes.`;
            const errorOnly = isDeployFailedState ? `A previous deployment failed for ${agentToDisplay.displayName}.` : `${agentToDisplay.displayName} is deployed but not running. You can redeploy to restart or delete to remove it.`;
            return (
                <div className="mb-4 pb-2">
                    <MessageBanner variant="warning" message={displayOutOfSyncInformation ? errorWithOutOfSync : errorOnly} />
                </div>
            );
        }
        if (displayOutOfSyncInformation) {
            return (
                <div className="mb-4 pb-2">
                    <MessageBanner variant="info" message={`There are undeployed changes for ${agent.deployed?.displayName ?? agentToDisplay.displayName}. Please review and deploy these updates.`} />
                </div>
            );
        }
        return null;
    };

    const renderViewOnlyActions = () => (
        <>
            {handleManageAgent ? (
                <Button data-testid="manageAgent" title="Manage Agent" variant="outline" onClick={handleManageAgent}>
                    Manage Agent
                </Button>
            ) : null}
            <Button data-testid="chatWithAgent" title="Chat With Agent" onClick={() => handleChatWithAgent()}>
                Chat With Agent
            </Button>
        </>
    );

    const renderActiveActions = (agentToDisplay: AgentCardInfo) => (
        <>
            {displayOutOfSyncInformation ? (
                <Button data-testid="previewUpdates" title="Preview and Deploy" variant="outline" onClick={handleViewUpdate}>
                    Preview and Deploy
                </Button>
            ) : null}
            {canDeployAgents && agentToDisplay.type !== "discovered" ? (
                <Button data-testid="undeployAgent" title="Undeploy" variant="outline" onClick={() => openDeploymentDialog("undeploy")}>
                    Undeploy
                </Button>
            ) : null}
            <Button data-testid="chatWithAgent" title="Chat With Agent" onClick={() => handleChatWithAgent()}>
                Chat With Agent
            </Button>
        </>
    );

    const renderInactiveErrorActions = () => (
        <>
            {canDeployAgents &&
                (displayOutOfSyncInformation ? (
                    <Button data-testid="previewUpdates" title="Preview and Deploy" onClick={handleViewUpdate}>
                        Preview and Deploy
                    </Button>
                ) : (
                    <Button data-testid="redeployAgent" title="Redeploy" onClick={() => openDeploymentDialog("update")}>
                        Redeploy
                    </Button>
                ))}
            {canDeleteAgents && (
                <Button data-testid="deleteAgent" title="Delete" variant="outline" onClick={() => setConfirmDeleteOpen(true)}>
                    Delete
                </Button>
            )}
        </>
    );

    const renderInactiveDeployAction = () => {
        if (!canDeployAgents) return null;
        if (displayOutOfSyncInformation) {
            return (
                <Button data-testid="previewUpdates" title="Preview and Deploy" onClick={handleViewUpdate} className="w-full">
                    Preview and Deploy
                </Button>
            );
        }
        return (
            <Button data-testid="deployAgent" title="Deploy" onClick={() => openDeploymentDialog("deploy")}>
                Deploy
            </Button>
        );
    };

    const renderInactiveActions = () => (
        <>
            {isErrorState ? renderInactiveErrorActions() : renderInactiveDeployAction()}
            <Button data-testid="openAgent" title="Open Agent" variant="outline" onClick={handleOpen}>
                Open Agent
            </Button>
        </>
    );

    const renderActions = (agentToDisplay: AgentCardInfo) => {
        if (viewOnly) return renderViewOnlyActions();
        if (selectedState === "active") return renderActiveActions(agentToDisplay);
        if (selectedState === "inactive") return renderInactiveActions();
        return null;
    };

    const getAgentContent = (agentToDisplay: AgentCardInfo) => {
        const isRegisteredExternal = agentToDisplay.type === "external" && agentToDisplay.agentCardLocation === "none" && agentToDisplay.status === "deployed";

        return (
            <div className="flex-1 overflow-y-auto p-4">
                {duplicateInfo.hasDuplicate && duplicateInfo.displayName && (
                    <DuplicateNameWarningBanner agentName={duplicateInfo.displayName} count={duplicateInfo.duplicateCount} duplicateAgentIds={duplicateInfo.duplicateAgentIds} dismissible className="mb-4 pb-2" />
                )}

                {renderLifecycleBanner(agentToDisplay)}
                {!viewOnly && <CardRefusedBanner status={agentToDisplay.remoteProtocol} className="mb-4" />}
                <div className="mb-4 flex flex-col gap-2 rounded-sm bg-(--secondary-w10) p-4">
                    <div className="text-base font-semibold">Agent Details</div>
                    <div className="line-clamp-3">{agentToDisplay.description ?? "N/A"}</div>
                    <div className="grid grid-cols-2 gap-4 p-4">
                        <div>
                            <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Status</div>
                            {isRegisteredExternal ? <RegisteredStatusWithTooltip /> : <DeploymentStatus agent={agentToDisplay} selectedState={selectedState ?? "inactive"} showSyncStatus={canReadAgents} />}
                        </div>
                        <div>
                            <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Type</div>
                            <div>{getAgentTypeLabel(agentToDisplay.type)}</div>
                        </div>
                        <RemoteInterfaceInUse status={agentToDisplay.remoteProtocol} />
                    </div>
                    {renderActions(agentToDisplay)}
                </div>
                <div className="flex flex-col gap-2">
                    <AgentAttachedSkillsSection skillIds={agentToDisplay?.skillIds} skillRefs={agentToDisplay?.skillRefs} customSkills={customSkills} builtinSkills={builtinSkills} resolvedSkills={agentToDisplay?.resolvedSkills} />
                    <AgentToolsetsSection tools={agentToDisplay?.tools} agentType={agentToDisplay.type} />
                    <AgentConnectorsSection connectors={agentToDisplay?.connectorObjects} showTools={true} toolRegistrations={agentToDisplay?.toolRegistrations} />
                    {canReadAgents && selectedModel && (
                        <>
                            <div className="py-2 font-semibold">Model</div>
                            <AgentModelCard model={selectedModel} className="w-full" />
                        </>
                    )}
                    <AccordionCard variant="borderless" value="agent-card" className="mt-4" trigger={<span className="font-semibold">Agent Card</span>}>
                        <AgentAbilitiesSection skills={agentToDisplay?.skills} />
                        <div className="py-2 font-semibold">Communication Modes</div>
                        <div className="grid grid-cols-[100px_1fr] gap-4 rounded-sm bg-(--secondary-w10) p-4">
                            <AgentCommunicationMode label="Input Modes" modes={agentToDisplay?.defaultInputModes ?? []} />
                            <AgentCommunicationMode label="Output Modes" modes={agentToDisplay?.defaultOutputModes ?? []} />
                        </div>
                    </AccordionCard>
                </div>
                {userAgent?.id ? <Metadata metadata={userAgent} scope={invokeScope} /> : null}
            </div>
        );
    };

    return agentToDisplay ? (
        <div className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between gap-2 border-b px-4">
                <div className="truncate text-xl font-semibold" title={agentToDisplay.displayName}>
                    {agentToDisplay.displayName}
                </div>
                <div className="flex gap-2">
                    <Button variant="ghost" tooltip="Close" onClick={onClose}>
                        <X />
                    </Button>
                </div>
            </div>

            {getAgentContent(agentToDisplay)}
            {isComparisonOpen && agent.deployed ? (
                <AgentCardComparisonDialog deployedAgent={agent.deployed} draftAgent={agent} userAgent={agent.type === "external" ? remoteAgent : userAgent} open={isComparisonOpen} onClose={() => setIsComparisonOpen(false)} />
            ) : null}
            <ErrorDialog open={!!error} title="Agent Deployment Error" error={error ?? ""} errorSource="api" onOpenChange={open => !open && setError(null)} />
            <ErrorDialog open={!!remoteDeploymentError} title="Remote Agent Deployment Error" error={remoteDeploymentError ?? ""} errorSource="api" onOpenChange={open => !open && setRemoteDeploymentError(null)} />
            <DeleteAgentConfirmationDialog open={confirmDeleteOpen} onOpenChange={setConfirmDeleteOpen} agentName={agent.displayName ?? ""} needsCleanup onConfirm={handleDelete} />
            {userAgent && (
                <AgentDeploymentDialog
                    open={deploymentDialogOpen}
                    agentName={agentToDisplay.displayName || agentToDisplay.name}
                    agent={userAgent}
                    action={deploymentAction}
                    onConfirm={() => {
                        onDeploy(deploymentAction);
                        setDeploymentDialogOpen(false);
                    }}
                    onClose={() => setDeploymentDialogOpen(false)}
                />
            )}
        </div>
    ) : null;
};
