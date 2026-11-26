// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { AlertTriangle, CircleCheckBig, RefreshCw, X } from "lucide-react";

import { Button } from "@/ui/button";
import type { EvalRunStatus } from "@/types/eval";

import { ProgressBar } from "./ProgressBar";

interface ExperimentReportBannerProps {
    readonly status: EvalRunStatus;
    readonly progressCurrent: number;
    readonly progressTotal: number;
    readonly onRunNew?: () => void;
    readonly onCancel?: () => void;
    readonly cancelling?: boolean;
}

function InProgressBanner({ status, progressCurrent, progressTotal, onCancel, cancelling }: ExperimentReportBannerProps) {
    return (
        <div data-testid="experimentReportBanner" data-banner-status={status} className="flex items-center gap-3 rounded-xs border border-(--brand-w100)/30 bg-(--brand-w100)/5 px-4 py-3">
            <RefreshCw className="size-4 shrink-0 animate-spin text-(--brand-w100)" />
            <span className="text-sm">
                <span data-testid="experimentReportStatus" className="font-semibold">
                    {status === "pending" ? "Experiment starting:" : "Experiment in Progress:"}
                </span>{" "}
                <span className="text-(--secondary-text-wMain)">
                    {progressCurrent}/{progressTotal} examples completed
                </span>
            </span>
            <div className="ml-auto max-w-[420px] min-w-[200px] flex-1">
                <ProgressBar current={progressCurrent} total={progressTotal} showLabel={false} />
            </div>
            {onCancel && (
                <Button size="sm" variant="outline" onClick={onCancel} disabled={cancelling}>
                    {cancelling ? "Cancelling…" : "Cancel"}
                </Button>
            )}
        </div>
    );
}

function SuccessBanner({ status, onDismiss }: { readonly status: EvalRunStatus; readonly onDismiss: () => void }) {
    const withWarnings = status === "completed_with_warnings";
    const title = withWarnings ? "Experiment complete with warnings" : "Experiment complete";
    const body = withWarnings ? "Some examples reported warnings during evaluation. Review the results below." : "All examples were evaluated successfully.";
    return (
        <div data-testid="experimentReportBanner" data-banner-status={status} className="flex items-start gap-3 rounded-xs border border-(--success-wMain)/30 bg-(--success-wMain)/5 p-4">
            <CircleCheckBig className="mt-0.5 size-5 shrink-0 text-(--success-wMain)" />
            <div className="flex-1 text-sm">
                <div data-testid="experimentReportStatus" className="font-semibold">
                    {title}
                </div>
                <div className="text-xs text-(--secondary-text-wMain)">{body}</div>
            </div>
            <Button onClick={onDismiss} aria-label="Dismiss" variant="ghost">
                <X className="size-4" />
            </Button>
        </div>
    );
}

function TerminalBanner({ status, onRunNew }: { readonly status: EvalRunStatus; readonly onRunNew?: () => void }) {
    const label = status === "failed" ? "Experiment partially completed with failures" : "Experiment was cancelled";
    const reason = status === "failed" ? "One or more runs failed. Results below may be partial." : "The experiment was cancelled mid-run. Results below may be partial.";
    return (
        <div data-testid="experimentReportBanner" data-banner-status={status} className="flex items-start gap-3 rounded-xs border border-(--warning-w100)/40 bg-(--warning-w100)/10 p-4">
            <AlertTriangle className="mt-0.5 size-5 shrink-0 text-(--warning-w100)" />
            <div className="flex-1 text-sm">
                <div data-testid="experimentReportStatus" className="font-semibold">
                    {label}
                </div>
                <div className="mt-0.5 text-xs text-(--secondary-text-wMain)">{reason}</div>
            </div>
            {onRunNew && (
                <Button size="sm" onClick={onRunNew}>
                    Run New Experiment
                </Button>
            )}
        </div>
    );
}

export function ExperimentReportBanner(props: ExperimentReportBannerProps) {
    const [successDismissed, setSuccessDismissed] = useState(false);

    if (props.status === "pending" || props.status === "running") {
        return <InProgressBanner {...props} />;
    }
    if ((props.status === "completed" || props.status === "completed_with_warnings") && !successDismissed) {
        return <SuccessBanner status={props.status} onDismiss={() => setSuccessDismissed(true)} />;
    }
    if (props.status === "failed" || props.status === "cancelled") {
        return <TerminalBanner status={props.status} onRunNew={props.onRunNew} />;
    }
    return null;
}
