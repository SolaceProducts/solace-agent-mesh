// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";

import { useChatContext, useConfigContext } from "@/providers/contexts";

import { resolveAgentSessionRoute } from "@/utils/agentUtils";
import { setLastUsedAgentName } from "@/utils/lastUsedAgent";

import { useCacheUserId } from "./useCacheUserId";

function isShownInAgentSelector(agent: { isInternal?: boolean; isWorkflow?: boolean }): boolean {
    return !agent.isInternal && !agent.isWorkflow;
}

export const useAgentSelection = () => {
    const { agents, messages, selectedAgentName, setSelectedAgentName, handleNewSession, navigate, agentSessionRoutes } = useChatContext();
    const { persistenceEnabled } = useConfigContext();
    const cacheUserId = useCacheUserId();

    const rememberAgent = useCallback(
        (agent: (typeof agents)[number] | undefined) => {
            if (!agent?.name || !isShownInAgentSelector(agent)) return;
            setLastUsedAgentName(cacheUserId, agent.name);
        },
        [cacheUserId]
    );

    const [switchConfirmOpen, setSwitchConfirmOpen] = useState(false);
    const [pendingAgentName, setPendingAgentName] = useState<string | null>(null);

    const executeSwitch = useCallback(
        (agentName: string) => {
            const selectedAgent = agents.find(agent => agent.name === agentName);
            if (!selectedAgent) {
                console.warn(`Selected agent not found: ${agentName}`);
                return;
            }
            handleNewSession(true);
            setSelectedAgentName(agentName);
            rememberAgent(selectedAgent);
        },
        [agents, handleNewSession, setSelectedAgentName, rememberAgent]
    );

    const retargetAgent = useCallback(
        (agentName: string) => {
            setSelectedAgentName(agentName);
            rememberAgent(agents.find(agent => agent.name === agentName));
        },
        [agents, setSelectedAgentName, rememberAgent]
    );

    const handleAgentSelection = useCallback(
        (agentName: string) => {
            if (!agentName || agentName === selectedAgentName) return;

            const hasConversation = messages.length > 1 || (messages.length === 1 && messages[0].isUser);

            if (hasConversation && !persistenceEnabled) {
                setPendingAgentName(agentName);
                setSwitchConfirmOpen(true);
            } else {
                executeSwitch(agentName);
            }
        },
        [selectedAgentName, messages, persistenceEnabled, executeSwitch]
    );

    const chatWithAgent = useCallback(
        (agentName: string) => {
            const dedicatedRoute = resolveAgentSessionRoute(agentName, agentSessionRoutes);
            if (dedicatedRoute) {
                navigate(dedicatedRoute);
                return;
            }
            handleAgentSelection(agentName);
            navigate("/chat");
        },
        [agentSessionRoutes, navigate, handleAgentSelection]
    );

    const confirmAgentSwitch = useCallback(() => {
        if (pendingAgentName) {
            executeSwitch(pendingAgentName);
            setPendingAgentName(null);
            setSwitchConfirmOpen(false);
        }
    }, [pendingAgentName, executeSwitch]);

    const cancelAgentSwitch = useCallback(() => {
        setPendingAgentName(null);
        setSwitchConfirmOpen(false);
    }, []);

    return {
        handleAgentSelection,
        retargetAgent,
        chatWithAgent,
        switchConfirmOpen,
        setSwitchConfirmOpen,
        confirmAgentSwitch,
        cancelAgentSwitch,
    };
};
