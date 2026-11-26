// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { Workflow } from "@/types/workflow";

import { workflowKeys } from "./keys";
import type { CreateWorkflowRequest, UpdateWorkflowPayload, WorkflowDeploymentRequest, WorkflowDeploymentResult } from "./service";
import { workflowsApi } from "./versioned";

export function useWorkflowsQuery(options: { enabled?: boolean } = {}) {
    return useQuery({
        queryKey: workflowKeys.list(),
        queryFn: workflowsApi.getAll,
        enabled: options.enabled ?? true,
        refetchOnMount: "always",
        retry: 0,
    });
}

export function useCreateWorkflowMutation() {
    const queryClient = useQueryClient();
    return useMutation<{ id?: string } | null, Error, CreateWorkflowRequest>({
        mutationFn: workflowsApi.create,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: workflowKeys.lists() });
        },
    });
}

export function useUpdateWorkflowMutation() {
    const queryClient = useQueryClient();
    return useMutation<Workflow, Error, { id: string; payload: UpdateWorkflowPayload }>({
        mutationFn: ({ id, payload }) => workflowsApi.update(id, payload),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: workflowKeys.lists() });
        },
    });
}

export function useDeleteWorkflowMutation() {
    const queryClient = useQueryClient();
    return useMutation<void, Error, string>({
        mutationFn: workflowsApi.delete,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: workflowKeys.lists() });
        },
    });
}

interface UseWorkflowDeploymentMutationOptions {
    onError?: (error: Error, request: WorkflowDeploymentRequest) => void;
    onSuccess?: (request: WorkflowDeploymentRequest) => void;
}

export function useWorkflowDeploymentMutation(options: UseWorkflowDeploymentMutationOptions = {}) {
    const queryClient = useQueryClient();
    return useMutation<WorkflowDeploymentResult, Error, WorkflowDeploymentRequest>({
        mutationFn: async request => {
            const result = await workflowsApi.deploy(request);
            if (result?.status && result.status !== "success") {
                const msg = result.errorMessage ?? `Workflow deployment reported status: ${result.status}`;
                throw new Error(msg);
            }
            return result;
        },
        onSuccess: (_data, request) => {
            queryClient.invalidateQueries({ queryKey: workflowKeys.lists() });
            queryClient.invalidateQueries({ queryKey: workflowKeys.deployments() });
            options.onSuccess?.(request);
        },
        onError: options.onError,
    });
}
