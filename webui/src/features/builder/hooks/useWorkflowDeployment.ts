// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";

import { useWorkflowDeploymentMutation, type WorkflowDeploymentAction } from "@/api/workflows";

import { useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import { getErrorMessage } from "@/utils/api";

interface UseWorkflowDeploymentReturn {
    isLoading: boolean;
    error: string | null;
    setError: (error: string | null) => void;
    deploy: (params: { workflowId: string; action: WorkflowDeploymentAction; onSuccess?: () => void }) => Promise<void>;
}

const actionLabel = (action: WorkflowDeploymentAction) => (action === "deploy" ? "Deployment" : action === "undeploy" ? "Undeployment" : "Update");

export function useWorkflowDeployment(): UseWorkflowDeploymentReturn {
    const { addNotification } = useChatContext();
    const [error, setError] = useState<string | null>(null);

    const mutation = useWorkflowDeploymentMutation();

    const deploy = useCallback(
        async ({ workflowId, action, onSuccess }: { workflowId: string; action: WorkflowDeploymentAction; onSuccess?: () => void }) => {
            if (mutation.isPending) throw new Error(`${actionLabel(action)} is already in progress.`);
            setError(null);
            try {
                await mutation.mutateAsync({ workflowId, action });
                addNotification(`Workflow ${actionLabel(action).toLowerCase()} started.`, "info");
                onSuccess?.();
            } catch (err: unknown) {
                const errorMessage = getErrorMessage(err, "Failed to update workflow deployment.");
                setError(errorMessage);
                reportAndNotifyError(addNotification, `${actionLabel(action)} failed: ${errorMessage}`, "api");
                throw err instanceof Error ? err : new Error(errorMessage);
            }
        },
        [mutation, addNotification]
    );

    return useMemo(
        () => ({
            isLoading: mutation.isPending,
            error,
            setError,
            deploy,
        }),
        [mutation.isPending, error, deploy]
    );
}
