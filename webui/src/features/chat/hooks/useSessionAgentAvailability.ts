// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useUserAgentCards } from "@/api/agents";
import { useRemoteAgentCards } from "@/api/remote-agents";

import { useBuilderContext, useChatContext, useUIModeContext } from "@/providers/contexts";
import { useUserContext } from "@/providers/contexts/UserContext";

import type { AgentCardInfo } from "@/types";

export interface SessionAgentAvailability {
    unavailable: boolean;
    bannerMessage: string | null;
}

export function deriveSessionAgentAvailability(params: { active: boolean; selectedAgentName: string; agents: AgentCardInfo[]; knownAgentNames: Set<string>; agentDefinitionsKnown: boolean }): SessionAgentAvailability {
    const { active, selectedAgentName, agents, knownAgentNames, agentDefinitionsKnown } = params;

    const unavailable = active && !!selectedAgentName && agents.length > 0 && !agents.some(a => a.name === selectedAgentName);
    if (!unavailable) return { unavailable: false, bannerMessage: null };

    const deleted = agentDefinitionsKnown && !knownAgentNames.has(selectedAgentName);
    const state = deleted ? "has been deleted" : "is not available";
    return { unavailable: true, bannerMessage: `This chat's agent ${state}. Select an agent to continue the conversation.` };
}

const EMPTY_CARDS: AgentCardInfo[] = [];

export function useSessionAgentAvailability(agents: AgentCardInfo[], allowAgentSelection: boolean): SessionAgentAvailability {
    const { selectedAgentName } = useChatContext();
    const { builderMode } = useBuilderContext();
    const { showAgentSelector } = useUIModeContext();
    const { userHasCapability, CAPABILITIES, capabilitiesLoaded } = useUserContext();
    const agentBuilderEnabled = userHasCapability(CAPABILITIES.readAgents);

    const { data: userAgentCards = EMPTY_CARDS } = useUserAgentCards({ enabled: agentBuilderEnabled });
    const { data: remoteAgentCards = EMPTY_CARDS } = useRemoteAgentCards();

    const knownAgentNames = useMemo(() => new Set([...userAgentCards, ...remoteAgentCards].map(a => a.name).filter(Boolean)), [userAgentCards, remoteAgentCards]);
    const agentDefinitionsKnown = agentBuilderEnabled && capabilitiesLoaded && knownAgentNames.size > 0;
    const active = showAgentSelector && !builderMode && allowAgentSelection;

    return useMemo(() => deriveSessionAgentAvailability({ active, selectedAgentName, agents, knownAgentNames, agentDefinitionsKnown }), [active, selectedAgentName, agents, knownAgentNames, agentDefinitionsKnown]);
}
