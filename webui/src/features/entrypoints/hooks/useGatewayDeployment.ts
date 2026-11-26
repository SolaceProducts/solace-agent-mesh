// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";

import { deployFailureMessage } from "@/api/agent-deployments";
import { gatewaysApi } from "@/api/gateways";

import { getErrorMessage } from "@/utils/api";

interface UseGatewayDeploymentReturn {
    isLoading: boolean;
    error: string | null;
    setError: (error: string | null) => void;
    deploy: ({ gatewayId, action, onSuccess }: { gatewayId: string; action: "deploy" | "undeploy" | "update"; onSuccess?: () => void }) => Promise<void>;
}

export const useGatewayDeployment = (): UseGatewayDeploymentReturn => {
    const [isLoading, setIsLoading] = useState<boolean>(false);
    const [error, setError] = useState<string | null>(null);

    const deployAction = useCallback(
        async ({ gatewayId, action, onSuccess }: { gatewayId: string; action: "deploy" | "undeploy" | "update"; onSuccess?: () => void }) => {
            if (isLoading) return;

            setIsLoading(true);
            setError(null);

            try {
                const resp = await gatewaysApi.deploy({ gatewayId, action });
                const failure = deployFailureMessage(resp, "Deployment");
                if (failure !== null) {
                    setError(failure);
                    return;
                }
                onSuccess?.();
            } catch (err: unknown) {
                setError(getErrorMessage(err, "Failed to update entrypoint deployment. Verify the deployment service is running."));
            } finally {
                setIsLoading(false);
            }
        },
        [isLoading]
    );

    return useMemo(() => {
        return {
            isLoading,
            error,
            setError,
            deploy: deployAction,
        };
    }, [isLoading, error, deployAction]);
};
