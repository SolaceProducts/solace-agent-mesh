// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Brain } from "lucide-react";

import { type LlmCallPerformance } from "@/types/activities";

import { formatMilliseconds } from "./format";

interface LlmCallItemProps {
    call: LlmCallPerformance;
}

export const LlmCallItem: React.FC<LlmCallItemProps> = ({ call }) => {
    const title = `Model: ${call.modelName}`;

    return (
        <div className="flex items-center justify-between rounded-md p-2 hover:bg-(--secondary-w10)">
            <div className="flex items-center">
                <Brain className="mr-2 h-4 w-4" />
                <span>{title}</span>
            </div>
            <span>{formatMilliseconds(call.durationMs)}</span>
        </div>
    );
};
