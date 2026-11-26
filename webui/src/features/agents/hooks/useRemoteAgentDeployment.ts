// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo } from "react";

import { actionLabel, useDeploymentRunner } from "@/api/agent-deployments";
import { remoteAgentsApi, useRemoteAgents } from "@/api/remote-agents";

import { useChatContext } from "@/providers/contexts";

import type { RemoteAgent } from "@/types";

type RemoteAgentDeploymentAction = "deploy" | "undeploy";

interface UseRemoteAgentDeploymentReturn {
    isLoading: boolean;
    error: string | null;
    setError: (error: string | null) => void;
    deployRemoteAgent: ({ agentId, action, onSuccess }: { agentId: string; action: RemoteAgentDeploymentAction; onSuccess?: () => void }) => Promise<boolean>;
    handleDeployRemoteAgent: (remoteAgent: RemoteAgent, action: RemoteAgentDeploymentAction) => Promise<boolean>;
}

export const useRemoteAgentDeployment = (): UseRemoteAgentDeploymentReturn => {
    const { addNotification } = useChatContext();
    const { refetch: remoteAgentsRefetch } = useRemoteAgents();

    const { isLoading, error, setError, run } = useDeploymentRunner<RemoteAgentDeploymentAction>(remoteAgentsApi.deploy, { resourceLabel: "remote agent deployment", addNotification });

    const handleDeployRemoteAgent = useCallback(
        async (remoteAgent: RemoteAgent, action: RemoteAgentDeploymentAction): Promise<boolean> => {
            if (!remoteAgent?.id) return false;
            return run({
                agentId: remoteAgent.id,
                action,
                onSuccess: async () => {
                    await remoteAgentsRefetch();
                    addNotification(`${actionLabel(action, "past")} successfully.`, "success");
                },
            });
        },
        [run, remoteAgentsRefetch, addNotification]
    );

    return useMemo(
        () => ({
            isLoading,
            error,
            setError,
            deployRemoteAgent: run,
            handleDeployRemoteAgent,
        }),
        [isLoading, error, setError, run, handleDeployRemoteAgent]
    );
};
