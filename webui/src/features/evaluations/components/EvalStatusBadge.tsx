// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import type { EvalRunStatus } from "@/types/eval";

const STATUS_CONFIG: Record<EvalRunStatus, { label: string; className: string; pulse: boolean }> = {
    pending: { label: "Pending", className: "bg-(--secondary-w10) text-(--secondary-text-wMain)", pulse: false },
    running: { label: "Running", className: "bg-(--brand-w10) text-(--brand-w100)", pulse: true },
    completed: { label: "Completed", className: "bg-(--success-w10) text-(--success-wMain)", pulse: false },
    completed_with_warnings: { label: "Completed (Warnings)", className: "bg-(--warning-w10) text-(--warning-wMain)", pulse: false },
    failed: { label: "Failed", className: "bg-(--error-w10) text-(--error-wMain) font-semibold", pulse: false },
    cancelled: { label: "Cancelled", className: "bg-(--warning-w10) text-(--warning-wMain)", pulse: false },
};

interface EvalStatusBadgeProps {
    readonly status: EvalRunStatus;
}

export function EvalStatusBadge({ status }: EvalStatusBadgeProps) {
    const config = STATUS_CONFIG[status] ?? STATUS_CONFIG.pending;
    return (
        <span data-testid="evalStatusBadge" data-status={status} className={cn("inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium", config.className)}>
            {config.pulse && <span className="h-1.5 w-1.5 animate-pulse rounded-full bg-current" />}
            {config.label}
        </span>
    );
}
