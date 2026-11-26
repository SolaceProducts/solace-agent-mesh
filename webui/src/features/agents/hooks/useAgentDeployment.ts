// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";
import { actionLabel, agentDeploymentsApi, useDeploymentRunner } from "@/api/agent-deployments";
import { useAgents } from "@/api/agents";

import { useChatContext } from "@/providers/contexts";

import type { Agent } from "@/types";

export type DeploymentAction = "deploy" | "undeploy" | "update";

interface UseAgentDeploymentReturn {
    isLoading: boolean;
    error: string | null;
    setError: (error: string | null) => void;
    deploy: (args: { agentId: string; action: DeploymentAction; onSuccess?: () => void }) => Promise<boolean>;
    handleDeploy: (agent: Agent, action: DeploymentAction, nextPath?: string) => Promise<boolean>;
}

export const useAgentDeployment = (): UseAgentDeploymentReturn => {
    const { addNotification } = useChatContext();
    const { refetch: userAgentsRefetch } = useAgents();
    const { refetch: yamlAgentCardsRefetch } = useAgentCards();

    const { isLoading, error, setError, run } = useDeploymentRunner<DeploymentAction>((agentId, action) => agentDeploymentsApi.deploy({ agentId, action }), {
        resourceLabel: "agent deployment",
        addNotification,
    });

    const handleDeploy = useCallback(
        async (userAgent: Agent, action: DeploymentAction): Promise<boolean> => {
            if (!userAgent?.id) return false;
            return run({
                agentId: userAgent.id,
                action,
                onSuccess: async () => {
                    await Promise.all([userAgentsRefetch(), yamlAgentCardsRefetch()]);
                    addNotification(`${actionLabel(action, "past")} successfully.`, "success");
                },
            });
        },
        [run, userAgentsRefetch, yamlAgentCardsRefetch, addNotification]
    );

    return useMemo(() => ({ isLoading, error, setError, deploy: run, handleDeploy }), [isLoading, error, setError, run, handleDeploy]);
};
