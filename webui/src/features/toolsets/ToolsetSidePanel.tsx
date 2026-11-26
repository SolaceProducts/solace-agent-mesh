// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useState } from "react";
import { X } from "lucide-react";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { Button } from "@/ui/button";

import { AgentUsageCard, MessageBanner, Metadata } from "@/common";
import type { Toolset } from "@/types";

import { ToolsetAddToAgentDialog } from "./ToolsetAddToAgentDialog";

interface ToolsetSidePanelProps {
    toolset: Toolset;
    onClose?: () => void;
    initialOpenAddToAgent?: boolean;
    onAddToAgentOpened?: () => void;
}

export const ToolsetSidePanel: React.FC<ToolsetSidePanelProps> = ({ toolset, onClose, initialOpenAddToAgent = false, onAddToAgentOpened }) => {
    const { handleViewCustomToolset } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canAddToAgent = userHasCapability(CAPABILITIES.updateAgents);
    const [isAddToAgentOpen, setIsAddToAgentOpen] = useState(initialOpenAddToAgent && canAddToAgent);

    useEffect(() => {
        if (initialOpenAddToAgent) {
            onAddToAgentOpened?.();
        }
    }, []);

    const handleOpen = () => {
        handleViewCustomToolset(toolset);
    };

    return toolset ? (
        <div className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            <div className="flex h-16 items-center justify-between border-b px-4">
                <div className="truncate text-xl font-semibold" title={toolset.name}>
                    {toolset.name}
                </div>
                <Button variant="ghost" tooltip="Close" onClick={onClose} data-testid="sidePanelCloseButton">
                    <X />
                </Button>
            </div>

            <div className="flex-1 overflow-y-auto p-4">
                {toolset.type !== "builtin" && (toolset.discoveryStatus === "failed" || toolset.discoveryStatus === "removed") && toolset.discoveryErrors && toolset.discoveryErrors.length > 0 && (
                    <div className="mb-4">
                        <MessageBanner
                            variant="error"
                            message={
                                <div className="flex flex-col gap-1">
                                    <span>Discovery Errors</span>
                                    <ul className="flex flex-col gap-0.5 font-normal">
                                        {toolset.discoveryErrors.map(err => (
                                            <li key={err} className="font-mono text-xs">
                                                {err}
                                            </li>
                                        ))}
                                    </ul>
                                </div>
                            }
                        />
                    </div>
                )}
                <div className="mb-4 flex flex-col gap-4 rounded-sm bg-(--secondary-w10) p-4">
                    <div className="text-base font-semibold">Toolset Details</div>
                    <div className="break-word">{toolset.description ?? "No description provided"}</div>
                    {toolset.type !== "builtin" && (
                        <>
                            {canAddToAgent && (
                                <Button onClick={() => setIsAddToAgentOpen(true)} data-testid="addToAgentButton">
                                    Add to Agent
                                </Button>
                            )}
                            <Button variant="outline" onClick={handleOpen} data-testid="openToolsetButton">
                                Open Toolset
                            </Button>
                        </>
                    )}
                </div>

                <div className="mt-4 py-4 font-semibold">Agent Usage ({toolset.agents?.length ?? 0})</div>

                <div className="flex flex-col gap-2">
                    {(toolset.agents ?? []).length > 0 ? (
                        (toolset.agents ?? []).map(agent => <AgentUsageCard key={agent.id} agentId={agent.id} agentName={agent.name} agentDescription={agent.description} />)
                    ) : (
                        <div className="text-sm text-(--secondary-text-wMain)">No agents are currently using this tool</div>
                    )}
                </div>

                {toolset?.id ? <Metadata metadata={toolset} /> : null}
            </div>

            <ToolsetAddToAgentDialog isOpen={isAddToAgentOpen} toolset={toolset} onClose={() => setIsAddToAgentOpen(false)} />
        </div>
    ) : null;
};
