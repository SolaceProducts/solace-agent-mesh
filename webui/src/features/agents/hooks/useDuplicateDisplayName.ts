// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";
import { useUserAgentCards } from "@/api/agents";
import { useRemoteAgentCards } from "@/api/remote-agents";

import { useUserContext } from "@/providers/contexts";

import type { AgentCardInfo } from "@/types";

const EMPTY_REMOTE_AGENT_CARDS: AgentCardInfo[] = [];
const EMPTY_USER_AGENT_CARDS: AgentCardInfo[] = [];
const EMPTY_YAML_AGENT_CARDS: AgentCardInfo[] = [];

interface UseDuplicateDisplayNameReturn {
    hasDuplicates: boolean;
    firstDuplicate: { name: string; count: number; duplicateAgentIds: string[] } | null;
    allDuplicateGroups: Array<{ name: string; count: number; duplicateAgentIds: string[] }>;
    checkAgentDuplicates: (agent: AgentCardInfo) => { hasDuplicate: boolean; duplicateCount: number; displayName: string | null; duplicateAgentIds: string[] };
    hasDuplicateDisplayName: (agentName: string) => boolean;
}

export function useDuplicateDisplayName(agents?: AgentCardInfo[]): UseDuplicateDisplayNameReturn {
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { data: yamlAgentCards = EMPTY_YAML_AGENT_CARDS } = useAgentCards();
    const { data: userAgentCards = EMPTY_USER_AGENT_CARDS } = useUserAgentCards();
    const { data: remoteAgentCards = EMPTY_REMOTE_AGENT_CARDS } = useRemoteAgentCards();

    const canSeeDuplicateWarnings = userHasCapability(CAPABILITIES.createAgents);

    const allAgents = useMemo(() => {
        if (!canSeeDuplicateWarnings) return [];
        if (agents !== undefined) return agents;
        return [...(userAgentCards || []), ...(yamlAgentCards || []), ...(remoteAgentCards || [])];
    }, [agents, userAgentCards, yamlAgentCards, remoteAgentCards, canSeeDuplicateWarnings]);

    const duplicateData = useMemo(() => {
        if (!canSeeDuplicateWarnings) {
            return {
                duplicateGroups: new Map<string, AgentCardInfo[]>(),
                firstDuplicate: null as { name: string; count: number; duplicateAgentIds: string[] } | null,
                hasDuplicates: false,
                duplicateLookup: new Map<string, boolean>(),
                allDuplicateGroups: [],
            };
        }

        const nameGroups = new Map<string, AgentCardInfo[]>();
        const seenByDisplayName = new Map<string, Set<string>>();

        for (const agent of allAgents) {
            const displayName = agent.displayName?.trim();
            if (!displayName) continue;

            if (!nameGroups.has(displayName)) {
                nameGroups.set(displayName, []);
                seenByDisplayName.set(displayName, new Set());
            }

            const seen = seenByDisplayName.get(displayName)!;
            if (seen.has(agent.name)) continue;
            seen.add(agent.name);

            nameGroups.get(displayName)!.push(agent);
        }

        const duplicateGroups = new Map<string, AgentCardInfo[]>();
        for (const [name, group] of nameGroups) {
            if (group.length > 1) {
                duplicateGroups.set(name, group);
            }
        }

        const hasDuplicates = duplicateGroups.size > 0;
        const firstEntry = duplicateGroups.entries().next().value;
        const firstDuplicate = firstEntry
            ? {
                  name: firstEntry[0],
                  count: firstEntry[1].length,
                  duplicateAgentIds: firstEntry[1].map((agent: AgentCardInfo) => agent.name),
              }
            : null;

        const allDuplicateGroups = Array.from(duplicateGroups.entries()).map(([name, group]) => ({
            name,
            count: group.length,
            duplicateAgentIds: group.map((agent: AgentCardInfo) => agent.name),
        }));

        const duplicateLookup = new Map<string, boolean>();
        for (const group of duplicateGroups.values()) {
            for (const agent of group) {
                duplicateLookup.set(agent.name, true);
            }
        }

        return { duplicateGroups, firstDuplicate, hasDuplicates, duplicateLookup, allDuplicateGroups };
    }, [allAgents, canSeeDuplicateWarnings]);

    const checkAgentDuplicates = (agent: AgentCardInfo) => {
        if (!canSeeDuplicateWarnings) {
            return { hasDuplicate: false, duplicateCount: 0, displayName: null, duplicateAgentIds: [] };
        }
        const displayName = agent.displayName?.trim() || null;
        if (!displayName) {
            return { hasDuplicate: false, duplicateCount: 0, displayName: null, duplicateAgentIds: [] };
        }
        const group = duplicateData.duplicateGroups.get(displayName);
        const hasDuplicate = !!group && group.some(a => a.name !== agent.name);
        const duplicateAgentIds = group ? group.map(a => a.name) : [];
        return {
            hasDuplicate,
            duplicateCount: group ? group.length : 0,
            displayName,
            duplicateAgentIds,
        };
    };

    const hasDuplicateDisplayName = (agentName: string): boolean => duplicateData.duplicateLookup.get(agentName) || false;

    return {
        hasDuplicates: duplicateData.hasDuplicates,
        firstDuplicate: duplicateData.firstDuplicate,
        allDuplicateGroups: duplicateData.allDuplicateGroups,
        checkAgentDuplicates,
        hasDuplicateDisplayName,
    };
}
