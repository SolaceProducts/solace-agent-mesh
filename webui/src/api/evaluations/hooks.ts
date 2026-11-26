// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { skipToken, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { CreateDatasetRequest, CreateEvaluatorRequest, CreateExperimentRequest, UpdateDatasetRequest, UpdateEvaluatorRequest, UpdateExperimentRequest } from "@/types/eval";

import { evaluationKeys } from "./keys";
import { evaluationsApi } from "./versioned";

export function useEvalDatasets() {
    return useQuery({
        queryKey: evaluationKeys.datasetsList(),
        queryFn: () => evaluationsApi.getDatasets(),
    });
}

export function useEvalDataset(id: string | undefined) {
    return useQuery({
        queryKey: id ? evaluationKeys.dataset(id) : evaluationKeys.datasetDetails(),
        queryFn: id ? () => evaluationsApi.getDataset(id) : skipToken,
    });
}

export function useCreateDataset() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: CreateDatasetRequest) => evaluationsApi.createDataset(data),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.datasets() }),
    });
}

export function useUpdateDataset() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ id, data }: { id: string; data: UpdateDatasetRequest }) => evaluationsApi.updateDataset(id, data),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.datasets() }),
    });
}

export function useDeleteDataset() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => evaluationsApi.deleteDataset(id),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.datasets() }),
    });
}

export function useEvalEvaluators() {
    return useQuery({
        queryKey: evaluationKeys.evaluatorsList(),
        queryFn: () => evaluationsApi.getEvaluators(),
    });
}

export function useEvalEvaluator(id: string | undefined) {
    return useQuery({
        queryKey: id ? evaluationKeys.evaluator(id) : evaluationKeys.evaluatorDetails(),
        queryFn: id ? () => evaluationsApi.getEvaluator(id) : skipToken,
    });
}

export function useCreateEvaluator() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: CreateEvaluatorRequest) => evaluationsApi.createEvaluator(data),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.evaluators() }),
    });
}

export function useUpdateEvaluator() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ id, data }: { id: string; data: UpdateEvaluatorRequest }) => evaluationsApi.updateEvaluator(id, data),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.evaluators() }),
    });
}

export function useDeleteEvaluator() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => evaluationsApi.deleteEvaluator(id),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.evaluators() }),
    });
}

export function useEvalExperiments(params?: { datasetId?: string; evaluatorId?: string; targetAgent?: string }, options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: evaluationKeys.experimentsList(params),
        queryFn: () => evaluationsApi.getExperiments(params),
        enabled: options?.enabled,
    });
}

export function useEvalExperiment(id: string | undefined) {
    return useQuery({
        queryKey: id ? evaluationKeys.experiment(id) : evaluationKeys.experimentDetails(),
        queryFn: id ? () => evaluationsApi.getExperiment(id) : skipToken,
    });
}

export function useSampleExperiment() {
    return useQuery({
        queryKey: evaluationKeys.experimentSample(),
        queryFn: evaluationsApi.getSampleExperiment,
    });
}

export function useCreateExperiment() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (data: CreateExperimentRequest) => evaluationsApi.createExperiment(data),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.experiments() }),
    });
}

export function useUpdateExperiment() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ id, data }: { id: string; data: UpdateExperimentRequest }) => evaluationsApi.updateExperiment(id, data),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.experiments() }),
    });
}

export function useDeleteExperiment() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: string) => evaluationsApi.deleteExperiment(id),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.experiments() }),
    });
}

export function useExperimentToolAuth(experimentId: string | undefined, options?: { pollWhilePending?: boolean; principal?: string }) {
    return useQuery({
        queryKey: experimentId ? evaluationKeys.experimentToolAuthFor(experimentId, options?.principal ?? "") : evaluationKeys.experimentDetails(),
        queryFn: experimentId ? () => evaluationsApi.getExperimentToolAuth(experimentId) : skipToken,
        retry: 0,
        refetchOnWindowFocus: "always",
        refetchInterval: options?.pollWhilePending ? 3000 : false,
    });
}

export function useToolAuthCandidateAgents(experimentId: string | undefined, options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: experimentId ? evaluationKeys.experimentToolAuthCandidates(experimentId) : evaluationKeys.experimentDetails(),
        queryFn: experimentId ? () => evaluationsApi.listToolAuthCandidateAgents(experimentId) : skipToken,
        enabled: options?.enabled ?? false,
        retry: 0,
        staleTime: 30_000,
    });
}

interface ToolAuthCredentialVariables {
    experimentId: string;
    agentName: string;
    credentialKey: string;
}

export function useBeginExperimentToolAuth() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ experimentId, agentName, credentialKey }: ToolAuthCredentialVariables) => evaluationsApi.beginExperimentToolAuth(experimentId, agentName, credentialKey),
        onSuccess: (_data, { experimentId }) => queryClient.invalidateQueries({ queryKey: evaluationKeys.experimentToolAuth(experimentId) }),
    });
}

export function useDisconnectExperimentToolAuth() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ experimentId, agentName, credentialKey }: ToolAuthCredentialVariables) => evaluationsApi.disconnectExperimentToolAuth(experimentId, agentName, credentialKey),
        onSuccess: (_data, { experimentId }) => queryClient.invalidateQueries({ queryKey: evaluationKeys.experimentToolAuth(experimentId) }),
    });
}

interface ToolAuthAgentVariables {
    experimentId: string;
    agentName: string;
}

export function useAddToolAuthAgent() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ experimentId, agentName }: ToolAuthAgentVariables) => evaluationsApi.addExperimentToolAuthAgent(experimentId, agentName),
        onSuccess: (_data, { experimentId }) => queryClient.invalidateQueries({ queryKey: evaluationKeys.experimentToolAuth(experimentId) }),
    });
}

export function useRemoveToolAuthAgent() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ experimentId, agentName }: ToolAuthAgentVariables) => evaluationsApi.removeExperimentToolAuthAgent(experimentId, agentName),
        onSuccess: (_data, { experimentId }) => queryClient.invalidateQueries({ queryKey: evaluationKeys.experimentToolAuth(experimentId) }),
    });
}

export function useEvalRun(runId: string | undefined) {
    return useQuery({
        queryKey: runId ? evaluationKeys.run(runId) : evaluationKeys.runDetails(),
        queryFn: runId ? () => evaluationsApi.getRun(runId) : skipToken,
    });
}

export function useEvalRunGroup(groupId: string | undefined) {
    return useQuery({
        queryKey: groupId ? evaluationKeys.runGroup(groupId) : evaluationKeys.runGroupDetails(),
        queryFn: groupId ? () => evaluationsApi.getRunGroup(groupId) : skipToken,
    });
}

export function useDeleteRunGroup() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (groupId: string) => evaluationsApi.deleteRunGroup(groupId),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.runGroups() }),
    });
}

export function useEvalWatchlist() {
    return useQuery({
        queryKey: evaluationKeys.watchlistList(),
        queryFn: evaluationsApi.getWatchlist,
        refetchOnMount: "always",
    });
}

export function useEvalWatchlistChartData() {
    return useQuery({
        queryKey: evaluationKeys.watchlistChart(),
        queryFn: evaluationsApi.getWatchlistChartData,
        staleTime: 30_000,
        refetchOnMount: "always",
    });
}

export function useSetEvalWatchlist() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (agentNames: string[]) => evaluationsApi.setWatchlist(agentNames),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: evaluationKeys.watchlist() }),
    });
}
