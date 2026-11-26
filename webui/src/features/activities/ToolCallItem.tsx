// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Users, Wrench } from "lucide-react";

import { type ToolCallPerformance } from "@/types/activities";

import { formatMilliseconds } from "./format";

interface ToolCallItemProps {
    call: ToolCallPerformance;
}

export const ToolCallItem: React.FC<ToolCallItemProps> = ({ call }) => {
    const Icon = call.isPeer ? Users : Wrench;
    const title = call.isPeer ? `Peer: ${call.peerAgentName}` : `Tool: ${call.toolName}`;

    return (
        <div className="flex items-center justify-between rounded-md p-2 hover:bg-(--secondary-w10)">
            <div className="flex items-center">
                <Icon className="mr-2 h-4 w-4" />
                <span>{title}</span>
            </div>
            <span>{formatMilliseconds(call.durationMs)}</span>
        </div>
    );
};
