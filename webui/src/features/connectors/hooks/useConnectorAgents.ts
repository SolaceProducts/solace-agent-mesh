// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";

import { agentsApi } from "@/api/agents";

import type { Agent, Connector } from "@/types";

interface UseConnectorAgentsReturn {
    agents: Agent[];
    isLoading: boolean;
    error: string | null;
    refetch: ({ onSuccess }: { onSuccess?: (agents: Agent[]) => void }) => Promise<void>;
}

export const useConnectorAgents = ({ connector }: { connector: Connector }): UseConnectorAgentsReturn => {
    const [agents, setAgents] = useState<Agent[]>([]);
    const [isLoading, setIsLoading] = useState<boolean>(false);
    const [error, setError] = useState<string | null>(null);

    const fetchAgents = useCallback(
        async ({ onSuccess }: { onSuccess?: (agents: Agent[]) => void }) => {
            if (isLoading || !connector?.id || !connector.agentCount || connector.agentCount === 0) return;

            setIsLoading(true);
            setError(null);

            try {
                const agents = await agentsApi.getByConnector(connector.id);
                setAgents(agents);
                onSuccess?.(agents);
            } catch (err: unknown) {
                setError(err instanceof Error ? err.message : "Could not load connector information.");
                setAgents([]);
            } finally {
                setIsLoading(false);
            }
        },
        [connector]
    );

    useEffect(() => {
        fetchAgents({});
    }, [fetchAgents]);

    return useMemo(() => {
        return {
            agents,
            isLoading,
            error,
            refetch: fetchAgents,
        };
    }, [agents, isLoading, error, fetchAgents]);
};
