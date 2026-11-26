// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgents } from "@/api/agents";

export function useConnectorRuntimeToolNames(): Map<string, string[]> {
    const { data: agents } = useAgents();

    return useMemo(() => {
        const byConnector = new Map<string, string[]>();
        for (const agent of agents ?? []) {
            for (const reg of agent.toolRegistrations ?? []) {
                if (reg.status === "ready" && reg.connectorId && reg.toolNames?.length && !byConnector.has(reg.connectorId)) {
                    byConnector.set(reg.connectorId, reg.toolNames);
                }
            }
        }
        return byConnector;
    }, [agents]);
}
