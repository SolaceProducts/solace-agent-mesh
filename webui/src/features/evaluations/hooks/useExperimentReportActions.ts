// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";
import type { NavigateFunction } from "react-router-dom";

import { type EvaluationsApi, evaluationsApi, type useDeleteRunGroup } from "@/api/evaluations";

import type { EvalExperiment, EvalRunGroup } from "@/types/eval";

import { EVAL_ROUTES } from "../constants";

type DeleteRunGroupMutation = ReturnType<typeof useDeleteRunGroup>;

type UseExperimentReportActionsParams = {
    group: EvalRunGroup | null;
    experiment: EvalExperiment | null;
    deleteRunGroupMutation: DeleteRunGroupMutation;
    cancelRun: (runId: string) => Promise<unknown>;
    navigate: NavigateFunction;
};

function buildCancelErrorMessage(failureCount: number, activeCount: number): string {
    if (failureCount === activeCount) {
        return "Failed to cancel the experiment. Please try again.";
    }
    return `Cancelled ${activeCount - failureCount} of ${activeCount} runs; the rest failed to cancel.`;
}

async function exportGroupResults(group: EvalRunGroup, format: "csv" | "json", exportRunResults: EvaluationsApi["exportRunResults"]): Promise<void> {
    for (const run of group.runs) {
        const blob = await exportRunResults(run.id, format);
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `run-${run.model}-${run.id}-results.${format}`;
        a.click();
        URL.revokeObjectURL(url);
    }
}

export function useExperimentReportActions({ group, experiment, deleteRunGroupMutation, cancelRun, navigate }: UseExperimentReportActionsParams) {
    const [deleteOpen, setDeleteOpen] = useState(false);
    const [cancelOpen, setCancelOpen] = useState(false);
    const [cancelling, setCancelling] = useState(false);
    const [cancelError, setCancelError] = useState<string | null>(null);

    const openDelete = useCallback(() => setDeleteOpen(true), []);
    const closeDelete = useCallback(() => setDeleteOpen(false), []);

    const openCancel = useCallback(() => {
        setCancelError(null);
        setCancelOpen(true);
    }, []);
    const closeCancel = useCallback(() => setCancelOpen(false), []);

    const confirmDelete = useCallback(async () => {
        if (!group) return;
        await deleteRunGroupMutation.mutateAsync(group.id);
        setDeleteOpen(false);
        navigate(experiment ? EVAL_ROUTES.experiment(experiment.id) : EVAL_ROUTES.LAB_EXPERIMENTS_TAB);
    }, [group, experiment, deleteRunGroupMutation, navigate]);

    const confirmCancel = useCallback(async () => {
        if (!group) return;
        setCancelOpen(false);
        setCancelling(true);
        setCancelError(null);
        const activeRuns = group.runs.filter(r => r.status === "running" || r.status === "pending");
        const results = await Promise.allSettled(activeRuns.map(r => cancelRun(r.id)));
        setCancelling(false);
        const failures = results.filter((r): r is PromiseRejectedResult => r.status === "rejected");
        if (failures.length === 0) return;
        setCancelError(buildCancelErrorMessage(failures.length, activeRuns.length));
    }, [group, cancelRun]);

    const handleExport = useCallback(
        async (format: "csv" | "json") => {
            if (!group) return;
            await exportGroupResults(group, format, evaluationsApi.exportRunResults);
        },
        [group]
    );

    return {
        deleteOpen,
        setDeleteOpen,
        openDelete,
        closeDelete,
        confirmDelete,
        cancelOpen,
        setCancelOpen,
        openCancel,
        closeCancel,
        confirmCancel,
        cancelling,
        cancelError,
        handleExport,
    };
}
