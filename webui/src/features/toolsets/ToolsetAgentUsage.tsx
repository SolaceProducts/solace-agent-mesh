// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { useChatContext } from "@/providers/contexts";

import { AgentUsageList } from "@/common";
import type { Toolset } from "@/types";

interface ToolsetAgentUsageProps {
    toolset: Toolset;
}

export const ToolsetAgentUsage: React.FC<ToolsetAgentUsageProps> = ({ toolset }) => {
    const { navigate } = useChatContext();

    return <AgentUsageList agents={toolset.agents ?? []} emptyTitle="No agents use this toolset" onGoToAgents={() => navigate("/agents")} />;
};
