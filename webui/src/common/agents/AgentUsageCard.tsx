// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Bot, ChevronUp } from "lucide-react";

import { NewTabLink } from "@/common/NewTabLink";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { useDescriptionTruncation } from "@/hooks";

interface AgentUsageCardProps {
    agentId: string;
    agentName: string;
    agentDescription?: string;
}

export const AgentUsageCard: React.FC<AgentUsageCardProps> = ({ agentId, agentName, agentDescription }) => {
    const { isExpanded, isTruncated, descriptionRef, toggleExpanded } = useDescriptionTruncation(agentDescription ?? "", !!agentDescription);

    return (
        <div className="flex items-center gap-2 rounded-sm border p-4">
            <div className="mr-2 flex h-12 w-12 shrink-0 items-center justify-center rounded-full bg-(--secondary-w10)">
                <Bot className="size-5 text-(--brand-wMain)" />
            </div>
            <div className="relative flex min-w-0 flex-1 flex-col">
                <div className="flex items-center justify-between gap-2">
                    <NewTabLink to={`/agent-mesh/${agentId}`} className="p-0 has-[>svg]:px-0" label={agentName} />
                    {agentDescription && isTruncated && (
                        <Button
                            variant="ghost"
                            onClick={e => {
                                e.stopPropagation();
                                toggleExpanded();
                            }}
                            tooltip={isExpanded ? "Collapse description" : "Expand description"}
                            className="-mt-1 h-6 w-6"
                        >
                            {isExpanded ? <ChevronUp className="size-4" /> : <ChevronUp className="size-4 rotate-180 transform" />}
                        </Button>
                    )}
                </div>
                {agentDescription && (
                    <div ref={descriptionRef} className={cn("text-small text-(--secondary-text-wMain)", !isExpanded && "truncate")}>
                        {agentDescription}
                    </div>
                )}
            </div>
        </div>
    );
};
