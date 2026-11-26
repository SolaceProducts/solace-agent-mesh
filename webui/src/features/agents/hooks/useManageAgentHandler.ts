// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";

import { useChatContext, useUserContext } from "@/providers/contexts";

export function useManageAgentHandler(agentId: string | undefined): (() => void) | undefined {
    const { navigate } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const userCanEditAgents = userHasCapability(CAPABILITIES.updateAgents);

    const handler = useCallback(() => {
        if (!agentId) return;
        navigate(`/agents?id=${encodeURIComponent(agentId)}`);
    }, [agentId, navigate]);

    if (!userCanEditAgents || !agentId) return undefined;
    return handler;
}
