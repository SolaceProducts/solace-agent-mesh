// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { agentKeys } from "../agents/keys";

import { agentDeploymentKeys } from "./keys";
import { actionLabel, deployFailureMessage } from "./messaging";
import type { AgentDeploymentRequest, AgentDeploymentResult } from "./types";
import { agentDeploymentsApi } from "./versioned";

interface UseAgentDeploymentMutationOptions {
    onError?: (error: Error, request: AgentDeploymentRequest) => void;
}

export function useAgentDeploymentMutation(options: UseAgentDeploymentMutationOptions = {}) {
    const queryClient = useQueryClient();

    return useMutation<AgentDeploymentResult, Error, AgentDeploymentRequest>({
        mutationFn: async request => {
            const result = await agentDeploymentsApi.deploy(request);
            if (result?.status && result.status !== "success") {
                const msg = result.errorMessage ?? `Deployment reported status: ${result.status}`;
                throw new Error(msg);
            }
            return result;
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: agentKeys.all });
            queryClient.invalidateQueries({ queryKey: agentDeploymentKeys.all });
        },
        onError: options.onError,
    });
}

export type DeployActionFn<A extends string> = (agentId: string, action: A) => Promise<{ status?: string; errorMessage?: string } | undefined>;

export interface UseDeploymentRunnerOptions {
    resourceLabel: string;
    addNotification: (msg: string, type: "success" | "warning" | "info") => void;
}

export interface UseDeploymentRunnerReturn<A extends string> {
    isLoading: boolean;
    error: string | null;
    setError: (error: string | null) => void;
    run: (args: { agentId: string; action: A; onSuccess?: () => void }) => Promise<boolean>;
}

export function useDeploymentRunner<A extends string>(apiCall: DeployActionFn<A>, options: UseDeploymentRunnerOptions): UseDeploymentRunnerReturn<A> {
    const { resourceLabel, addNotification } = options;
    const [isLoading, setIsLoading] = useState<boolean>(false);
    const [error, setError] = useState<string | null>(null);

    const run = useCallback(
        async ({ agentId, action, onSuccess }: { agentId: string; action: A; onSuccess?: () => void }): Promise<boolean> => {
            if (isLoading) return false;

            setIsLoading(true);
            setError(null);

            const label = actionLabel(action as "deploy" | "undeploy" | "update");
            try {
                const resp = await apiCall(agentId, action);
                const failure = deployFailureMessage(resp, label);
                if (failure !== null) {
                    setError(failure);
                    addNotification(`${label} failed: ${failure}`, "warning");
                    return false;
                }
                onSuccess?.();
                return true;
            } catch (err: unknown) {
                const errorMessage = err instanceof Error ? err.message : `Failed to update ${resourceLabel}.`;
                setError(errorMessage);
                addNotification(`${label} failed: ${errorMessage}`, "warning");
                return false;
            } finally {
                setIsLoading(false);
            }
        },
        [apiCall, isLoading, addNotification, resourceLabel]
    );

    return useMemo(() => ({ isLoading, error, setError, run }), [isLoading, error, run]);
}
