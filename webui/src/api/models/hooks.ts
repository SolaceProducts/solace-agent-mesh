// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { hashKey, queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { modelKeys } from "./keys";
import type { CreateModelData, ModelData, TestConnectionRequest } from "./service";
import { chatModelsOnly, decisionModelsOnly, type ModelType } from "./types";
import { modelsApi } from "./versioned";

const CREDENTIAL_PROBE_STALE_MS = 0;

export interface SupportedModelsQueryParams {
    provider: string;
    modelId?: string;
    apiBase?: string;
    authConfig?: Record<string, unknown>;
    modelParams?: Record<string, unknown>;
    modelType?: ModelType;
}

export function useModelById(id: string | undefined) {
    return useQuery({
        queryKey: modelKeys.detail(id!),
        queryFn: () => modelsApi.get(id!),
        enabled: !!id,
        retry: 0,
    });
}

export function useModelConfigs(enabled = true) {
    return useQuery(modelListQueryOptions(enabled));
}

export function useChatModelConfigs(enabled = true) {
    return useQuery({ ...modelListQueryOptions(enabled), select: chatModelsOnly });
}

export function useDecisionModelConfigs(enabled = true) {
    return useQuery({ ...modelListQueryOptions(enabled), select: decisionModelsOnly });
}

function modelListQueryOptions(enabled: boolean) {
    return queryOptions({
        queryKey: modelKeys.list(),
        queryFn: modelsApi.getAll,
        refetchOnMount: "always",
        retry: 0,
        enabled,
    });
}

export function useDeleteModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (id: string) => modelsApi.delete(id),
        onSuccess: (_data, id) => {
            queryClient.invalidateQueries({ queryKey: modelKeys.lists() });
            queryClient.invalidateQueries({ queryKey: modelKeys.status() });
            queryClient.invalidateQueries({ queryKey: modelKeys.detail(id) });
        },
    });
}

export function isSameSupportedModelsQuery(a: SupportedModelsQueryParams | null, b: SupportedModelsQueryParams | null): boolean {
    return hashKey(modelKeys.supportedModels(a)) === hashKey(modelKeys.supportedModels(b));
}

export function useSupportedModels(params: SupportedModelsQueryParams | null) {
    return useQuery({
        queryKey: modelKeys.supportedModels(params),
        queryFn: () =>
            modelsApi.getSupportedModels(params!.provider, params?.modelId, {
                apiBase: params?.apiBase,
                authConfig: params?.authConfig,
                modelParams: params?.modelParams,
                modelType: params?.modelType,
            }),
        enabled: !!params,
        retry: 0,
        staleTime: CREDENTIAL_PROBE_STALE_MS,
        refetchOnWindowFocus: false,
    });
}

export function useModelConfigStatus() {
    return useQuery({
        queryKey: modelKeys.status(),
        queryFn: modelsApi.getStatus,
        refetchOnWindowFocus: false,
        retry: 1,
    });
}

export function useAzureWorkloadIdentity() {
    return useQuery({
        queryKey: modelKeys.azureWorkloadIdentity(),
        queryFn: modelsApi.getAzureWorkloadIdentity,
        staleTime: Infinity,
        retry: 0,
    });
}

export function useCreateModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (data: CreateModelData) => modelsApi.create(data),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: modelKeys.lists() });
            queryClient.invalidateQueries({ queryKey: modelKeys.status() });
        },
    });
}

export function useUpdateModel() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: ({ id, data }: { id: string; data: ModelData }) => modelsApi.update(id, data),
        onSuccess: (_data, { id }) => {
            queryClient.invalidateQueries({ queryKey: modelKeys.lists() });
            queryClient.invalidateQueries({ queryKey: modelKeys.status() });
            queryClient.invalidateQueries({ queryKey: modelKeys.detail(id) });
        },
    });
}

export function useTestModelConnection() {
    return useMutation({
        mutationFn: (data: TestConnectionRequest) => modelsApi.testConnection(data),
    });
}
