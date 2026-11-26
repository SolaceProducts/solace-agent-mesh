// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useModelConfigs } from "@/api/models/hooks";

import type { ExperimentModelConfig } from "@/types/eval";

import { useAgentLookup } from "@/features/agents/hooks";

const DEFAULT_MODEL_ALIAS = "general";

export interface AgentActiveModel {
    activeModelId: string | undefined;
    activeModel: ExperimentModelConfig | undefined;
}

export function useAgentActiveModel(agentName: string | undefined): AgentActiveModel {
    const { data: modelConfigs = [] } = useModelConfigs();
    const agent = useAgentLookup(agentName);

    return useMemo(() => {
        if (!agentName || modelConfigs.length === 0) {
            return { activeModelId: undefined, activeModel: undefined };
        }
        const alias = agent?.modelProvider?.[0] ?? DEFAULT_MODEL_ALIAS;
        const lower = alias.toLowerCase();
        const match = modelConfigs.find(m => m.alias.toLowerCase() === lower || m.id === alias);
        if (!match) return { activeModelId: undefined, activeModel: undefined };
        return {
            activeModelId: match.id,
            activeModel: { modelConfigId: match.id, model: match.modelName ?? "", alias: match.alias },
        };
    }, [agentName, agent, modelConfigs]);
}
