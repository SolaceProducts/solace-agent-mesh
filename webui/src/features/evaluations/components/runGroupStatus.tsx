// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import type { EvalRun, EvalRunStatus } from "@/types/eval";

export type RunGroupDisplayStatus = "complete" | "complete_with_warnings" | "partial" | "in_progress" | "pending" | "failed" | "cancelled";

export function runGroupDisplayStatus(aggregate: EvalRunStatus, runs: EvalRun[]): RunGroupDisplayStatus {
    if (aggregate === "running") return "in_progress";
    if (aggregate === "pending") return "pending";
    if (aggregate === "cancelled") return "cancelled";
    if (aggregate === "completed") return "complete";
    if (aggregate === "completed_with_warnings") return "complete_with_warnings";
    const hasSuccess = runs.some(r => r.status === "completed" || r.status === "completed_with_warnings");
    return hasSuccess ? "partial" : "failed";
}

const BADGE_CONFIG: Record<RunGroupDisplayStatus, { label: string; className: string; pulse: boolean }> = {
    complete: { label: "Complete", className: "bg-(--success-wMain)/25 text-(--success-wMain)", pulse: false },
    complete_with_warnings: { label: "Complete (Warnings)", className: "bg-(--warning-wMain)/25 text-(--warning-wMain)", pulse: false },
    partial: { label: "Partially Complete", className: "bg-(--success-wMain)/25 text-(--success-wMain)", pulse: false },
    in_progress: { label: "In Progress", className: "bg-(--brand-w100)/25 text-(--brand-w100)", pulse: true },
    pending: { label: "Pending", className: "bg-(--secondary-w10) text-(--secondary-text-wMain)", pulse: false },
    failed: { label: "Failed", className: "bg-(--error-wMain)/30 text-(--error-wMain) font-semibold", pulse: false },
    cancelled: { label: "Cancelled", className: "bg-(--warning-wMain)/25 text-(--warning-wMain)", pulse: false },
};

export interface RunGroupProgress {
    readonly current: number;
    readonly total: number;
}

export function calculateRunGroupProgress(runs: EvalRun[]): RunGroupProgress {
    if (runs.length === 0) {
        return { current: 0, total: 0 };
    }
    return {
        current: runs.reduce((sum, r) => sum + r.progressCurrent, 0),
        total: runs.reduce((sum, r) => sum + r.progressTotal, 0),
    };
}

export function runGroupAggregateStatusBadge(aggregate: EvalRunStatus, runs: EvalRun[], progress?: RunGroupProgress) {
    const display = runGroupDisplayStatus(aggregate, runs);
    const config = BADGE_CONFIG[display];
    const showProgress = display === "in_progress" && progress && progress.total > 0;
    const label = showProgress ? `${progress.current}/${progress.total} completed` : config.label;

    return (
        <span className={cn("inline-flex w-fit items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium", config.className)}>
            {config.pulse && <span data-testid="pulse-indicator" className="h-1.5 w-1.5 animate-pulse rounded-full bg-current" />}
            {label}
        </span>
    );
}

export function displayStatusLabel(aggregate: EvalRunStatus, runs: EvalRun[]): string {
    return BADGE_CONFIG[runGroupDisplayStatus(aggregate, runs)].label;
}
