// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { TriangleAlert } from "lucide-react";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";

interface AgentNameDuplicateIconProps {
    show: boolean;
    tooltipMessage?: string;
    className?: string;
}

export function AgentNameDuplicateIcon({ show, tooltipMessage = "This agent's name is already being used in the agent mesh.", className = "size-4 shrink-0 text-(--warning-wMain)" }: AgentNameDuplicateIconProps) {
    if (!show) return null;

    return (
        <Tooltip>
            <TooltipTrigger asChild>
                <TriangleAlert className={className} />
            </TooltipTrigger>
            <TooltipContent>{tooltipMessage}</TooltipContent>
        </Tooltip>
    );
}
