// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback } from "react";
import { Bot, MessageSquare, MoreHorizontal, Workflow } from "lucide-react";

import { useAgentSelection } from "@/hooks/useAgentSelection";

import { Button } from "@/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/ui/card";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

import { AgentNameDuplicateIcon } from "@/features/agents/AgentNameDuplicateIcon";

import type { AgentCardInfo } from "@/types";

import { AgentActionsPopover } from "./AgentActionsPopover";
import { DeploymentStatus, getAgentToDisplay, RegisteredStatusWithTooltip } from "./common";
import { useAgentLookup } from "./hooks";

interface AgentDisplayCardProps {
    agent: AgentCardInfo;
    onSelect?: () => void;
    selected?: boolean;
    onAgentDeleted?: () => void;
    hasDuplicate?: boolean;
    onDeployed?: (agentName: string) => void;
}

const AgentDisplayCardComponent: React.FC<AgentDisplayCardProps> = ({ agent, onSelect, selected = false, onAgentDeleted, hasDuplicate = false, onDeployed }) => {
    const { chatWithAgent } = useAgentSelection();

    const agentToDisplay = getAgentToDisplay(agent, "active");
    const userAgent = useAgentLookup(agent.displayName);

    const isRegistered = agentToDisplay.type === "external" && agentToDisplay.agentCardLocation === "none" && agentToDisplay.status === "deployed";

    const handleChatWithAgent = useCallback(
        (e: React.MouseEvent) => {
            e.stopPropagation();
            chatWithAgent(agent.name);
        },
        [agent, chatWithAgent]
    );

    return (
        <Card noPadding className="flex h-[200px] w-full shrink-0 py-4" onCardSelect={onSelect} isCardSelected={selected} data-testid={userAgent?.id ? `agent-display-card-${userAgent.id}` : undefined}>
            <CardHeader className="gap-0">
                <div className="flex w-full min-w-0 items-start justify-between gap-2 overflow-hidden">
                    <div className="flex min-w-0 flex-1 items-center gap-2">
                        {agent.isWorkflow ? <Workflow className="h-6 w-6 shrink-0 text-(--brand-wMain)" /> : <Bot className="h-6 w-6 shrink-0 text-(--brand-wMain)" />}
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <CardTitle className="min-w-0 truncate text-lg font-semibold text-(--primary-text-wMain)">{agentToDisplay.displayName}</CardTitle>
                            </TooltipTrigger>
                            <TooltipContent side="top">{agentToDisplay.displayName}</TooltipContent>
                        </Tooltip>
                        <AgentNameDuplicateIcon show={hasDuplicate} />
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                        {!agent.isWorkflow && (
                            <Button variant="ghost" size="icon" className="h-8 w-8" tooltip="Chat With Agent" onClick={e => handleChatWithAgent(e)} onKeyDown={e => e.stopPropagation()}>
                                <MessageSquare />
                            </Button>
                        )}
                        <AgentActionsPopover agent={agent} userAgent={userAgent} selectedState="active" viewOnly onAgentDeleted={onAgentDeleted} onDeployed={onDeployed}>
                            <Button variant="ghost" size="icon" className="h-8 w-8" tooltip="More options" onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()}>
                                <MoreHorizontal className="h-4 w-4" />
                            </Button>
                        </AgentActionsPopover>
                    </div>
                </div>
            </CardHeader>

            <CardContent className="flex flex-1 flex-col justify-between gap-4">
                <CardDescription className="line-clamp-3">{agentToDisplay.description || "No description provided."}</CardDescription>
                <div className="flex w-full items-center justify-between">{isRegistered ? <RegisteredStatusWithTooltip /> : <DeploymentStatus agent={agentToDisplay} selectedState="active" />}</div>
            </CardContent>
        </Card>
    );
};

export const AgentDisplayCard = React.memo(AgentDisplayCardComponent);
