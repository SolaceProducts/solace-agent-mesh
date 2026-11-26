// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { evaluationsApi } from "@/api/evaluations";

import type { EvalRun } from "@/types/eval";

const EVAL_RUNS_KEY = "evalRuns";
const EVAL_REPORTS_PAGINATED_KEY = "evalReportsPaginated";
const EVAL_REPORTS_AGENTS_KEY = "evalReportsAgents";

function invalidateRunsAndReports(queryClient: ReturnType<typeof useQueryClient>, experimentId: string | undefined) {
    if (experimentId) {
        queryClient.invalidateQueries({ queryKey: [EVAL_RUNS_KEY, experimentId] });
    }
    queryClient.invalidateQueries({ queryKey: [EVAL_REPORTS_PAGINATED_KEY] });
    queryClient.invalidateQueries({ queryKey: [EVAL_REPORTS_AGENTS_KEY] });
}

interface UseEvalRunsOptions {
    enableListQuery?: boolean;
}

export function useEvalRuns(experimentId: string | undefined, options: UseEvalRunsOptions = {}) {
    const { enableListQuery = true } = options;
    const queryClient = useQueryClient();

    const {
        data: runs = [],
        isLoading,
        error,
        refetch,
    } = useQuery<EvalRun[], Error>({
        queryKey: [EVAL_RUNS_KEY, experimentId],
        queryFn: () => evaluationsApi.getRuns(experimentId as string),
        enabled: !!experimentId && enableListQuery,
        refetchOnMount: "always",
        refetchInterval: query => {
            const data = query.state.data;
            if (!data) return false;
            const active = data.some(r => r.status === "running" || r.status === "pending");
            return active ? 5000 : false;
        },
    });

    const triggerMutation = useMutation<{ runGroupId?: string }, Error, string | undefined>({
        mutationFn: (runName?: string) => {
            if (!experimentId) throw new Error("No experiment ID");
            return evaluationsApi.triggerRun(experimentId, { runName });
        },
        onSuccess: () => invalidateRunsAndReports(queryClient, experimentId),
    });

    const cancelMutation = useMutation<EvalRun, Error, string>({
        mutationFn: (runId: string) => evaluationsApi.cancelRun(runId),
        onSuccess: () => invalidateRunsAndReports(queryClient, experimentId),
    });

    const deleteMutation = useMutation<void, Error, string>({
        mutationFn: (runId: string) => evaluationsApi.deleteRun(runId),
        onSuccess: () => invalidateRunsAndReports(queryClient, experimentId),
    });

    const triggerRun = useCallback((runName?: string) => triggerMutation.mutateAsync(runName), [triggerMutation]);
    const cancelRun = useCallback(
        async (runId: string) => {
            await cancelMutation.mutateAsync(runId);
        },
        [cancelMutation]
    );
    const deleteRun = useCallback(
        async (runId: string) => {
            await deleteMutation.mutateAsync(runId);
        },
        [deleteMutation]
    );
    const fetchRuns = useCallback(async () => {
        await refetch();
    }, [refetch]);

    const hasActiveRuns = runs.some(r => r.status === "running" || r.status === "pending");
    const loading = experimentId && enableListQuery ? isLoading : false;

    return {
        runs,
        loading,
        error: error ? String(error) : null,
        triggerRun,
        cancelRun,
        deleteRun,
        fetchRuns,
        hasActiveRuns,
    };
}
