// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { useAgents } from "@/api/agents";
import { useBuiltinSkills, useSkills } from "@/api/skills";

import { useChatContext } from "@/providers/contexts";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";

import { MessageBanner } from "@/common";
import type { Agent, AgentCardInfo, AgentState, RemoteAgent } from "@/types";

import { AgentAttachedSkillsSection } from "./AgentAttachedSkillsSection";
import { AgentAbilitiesSection, AgentConnectorsSection, AgentToolsetsSection } from "./AgentCardSections";
import { DeploymentStatus } from "./common";
import { useAgentDeployment, useRemoteAgentDeployment } from "./hooks";
import { getAgentTypeLabel } from "./utils/agentType";
import { agentManagementUrl } from "./utils/filters";

interface AgentCardComparisonDialogProps {
    deployedAgent?: AgentCardInfo;
    draftAgent?: AgentCardInfo;
    userAgent?: Agent | RemoteAgent;
    open: boolean;
    onClose: (e?: React.MouseEvent) => void;
}

export const AgentCardComparisonDialog: React.FC<AgentCardComparisonDialogProps> = ({ deployedAgent, draftAgent, userAgent, open, onClose }) => {
    const { addNotification, handleViewAgent, navigate } = useChatContext();
    const { refetch: userAgentsRefetch } = useAgents();
    const { data: customSkills } = useSkills();
    const { data: builtinSkills } = useBuiltinSkills();
    const { isLoading, error, setError, deploy } = useAgentDeployment();
    const { isLoading: isRemoteLoading, error: remoteError, setError: setRemoteError, handleDeployRemoteAgent } = useRemoteAgentDeployment();

    if (!deployedAgent || !draftAgent) {
        return null;
    }

    const isExternalAgent = draftAgent.type === "external";
    const currentIsLoading = isExternalAgent ? isRemoteLoading : isLoading;
    const currentError = isExternalAgent ? remoteError : error;
    const clearError = isExternalAgent ? () => setRemoteError(null) : () => setError(null);

    const onOpen = (e: React.MouseEvent) => {
        e.stopPropagation();
        onClose(e);
        if (userAgent && !isExternalAgent) {
            handleViewAgent(userAgent as Agent);
        }
    };

    const buildPostDeployUrl = (agentId: string) => agentManagementUrl(agentId, "deployed");

    const onDeploy = async (e: React.MouseEvent) => {
        e.stopPropagation();
        const agentId = userAgent?.id;
        if (agentId) {
            if (isExternalAgent) {
                const success = await handleDeployRemoteAgent(userAgent as RemoteAgent, "deploy");
                if (success) {
                    navigate(buildPostDeployUrl(agentId));
                    onClose(e);
                }
            } else {
                await deploy({
                    agentId: agentId,
                    action: "update",
                    onSuccess: async () => {
                        setError(null);
                        await userAgentsRefetch().then(() => {
                            addNotification("Update started successfully.");
                            navigate(buildPostDeployUrl(agentId));
                            onClose(e);
                        });
                    },
                });
            }
        }
    };

    const getAgentContent = (agentCard: AgentCardInfo, selectedState: AgentState) => {
        const isExternal = agentCard.type === "external";

        return (
            <div className="flex-1 overflow-y-auto p-4">
                <div className="mb-4 flex flex-col gap-2 rounded-sm bg-(--secondary-w10) p-4">
                    <div className="line-clamp-3">{agentCard.description ?? "N/A"}</div>
                    <div className="grid grid-cols-2 gap-4 p-4">
                        <div>
                            <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Status</div>
                            <DeploymentStatus agent={agentCard} selectedState={selectedState ?? "inactive"} />
                        </div>
                        <div>
                            <div className="mb-2 text-sm font-medium text-(--secondary-text-wMain)">Type</div>
                            <div>{getAgentTypeLabel(agentCard.type)}</div>
                        </div>
                    </div>
                    {isExternal && agentCard.url && (
                        <div className="border-t pt-2">
                            <div className="mb-1 text-sm font-medium text-(--secondary-text-wMain)">Agent URL</div>
                            <div className="text-sm break-all">{agentCard.url}</div>
                        </div>
                    )}
                </div>
                {!isExternal && (
                    <>
                        <AgentAttachedSkillsSection skillIds={agentCard?.skillIds} skillRefs={agentCard?.skillRefs} customSkills={customSkills} builtinSkills={builtinSkills} />
                        <AgentToolsetsSection tools={agentCard?.tools} agentType={agentCard.type} />
                        <AgentConnectorsSection connectors={agentCard?.connectorObjects} />
                    </>
                )}
                <AgentAbilitiesSection skills={agentCard?.skills} />
                {isExternal && (
                    <div className="grid grid-cols-2 gap-4 py-2">
                        <div>
                            <div className="mb-1 text-sm font-medium text-(--secondary-text-wMain)">Input Modes</div>
                            <div className="text-sm">{agentCard.defaultInputModes?.join(", ") || "text"}</div>
                        </div>
                        <div>
                            <div className="mb-1 text-sm font-medium text-(--secondary-text-wMain)">Output Modes</div>
                            <div className="text-sm">{agentCard.defaultOutputModes?.join(", ") || "text"}</div>
                        </div>
                    </div>
                )}
            </div>
        );
    };

    return (
        <Dialog
            open={open}
            onOpenChange={isOpen => {
                if (!isOpen) onClose();
            }}
        >
            <DialogContent className="flex max-h-[80vh] flex-col sm:max-w-6xl">
                <DialogHeader>
                    <DialogTitle>Preview Updates</DialogTitle>
                </DialogHeader>
                {currentError && <MessageBanner variant="error" message={`Agent Update Failed: ${currentError}`} dismissible onDismiss={clearError} />}
                <div className="grid min-h-0 flex-1 grid-cols-2 gap-0">
                    <div className="flex min-h-0 flex-col border-r">
                        <div className="flex justify-between gap-2 border-b px-4 py-3">
                            <div className="truncate font-semibold" title={deployedAgent.displayName}>
                                {deployedAgent.displayName}
                            </div>
                            <div className="text-(--info-wMain)">Deployed</div>
                        </div>
                        {getAgentContent({ ...deployedAgent, status: "deployed" }, "active")}
                    </div>

                    <div className="flex min-h-0 flex-col">
                        <div className="flex justify-between gap-2 border-b px-4 py-3">
                            <div className="truncate font-semibold" title={draftAgent.displayName}>
                                {draftAgent.displayName}
                            </div>
                            <div className="text-(--info-wMain)">Not Deployed</div>
                        </div>
                        {getAgentContent({ ...draftAgent, status: "not_deployed" }, "inactive")}
                    </div>
                </div>
                <div className="flex justify-end gap-2">
                    <Button variant="ghost" testid="closeButton" title="Close" onClick={onClose}>
                        Close
                    </Button>
                    {userAgent && (
                        <>
                            {!isExternalAgent && (
                                <Button variant="outline" testid="openDraftButton" title="Open" onClick={onOpen} disabled={currentIsLoading}>
                                    Open Agent
                                </Button>
                            )}
                            <Button testid="deployUpdateButton" title="Deploy Updates" onClick={onDeploy} disabled={currentIsLoading}>
                                Deploy Updates
                            </Button>
                        </>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
};
