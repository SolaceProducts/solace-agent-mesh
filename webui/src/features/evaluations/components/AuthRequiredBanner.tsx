// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { AlertTriangle } from "lucide-react";

import { Button } from "@/ui/button";
import type { EvalRunGroupResults } from "@/types/eval";

export interface FailureSummary {
    readonly authRequired: number;
    readonly other: number;
}

export function summarizeFailures(results: EvalRunGroupResults | null | undefined): FailureSummary {
    const authRequired = new Set<string>();
    const other = new Set<string>();
    for (const trials of Object.values(results?.resultsByModel ?? {})) {
        for (const trial of trials) {
            if (trial.failureKind === "auth_required") {
                authRequired.add(trial.exampleId);
            } else if (trial.errorMessage) {
                other.add(trial.exampleId);
            }
        }
    }
    return { authRequired: authRequired.size, other: other.size };
}

interface AuthRequiredBannerProps {
    readonly count: number;
    readonly onConnectTools?: () => void;
    readonly targetIsWorkflow?: boolean;
}

export function AuthRequiredBanner({ count, onConnectTools, targetIsWorkflow = false }: AuthRequiredBannerProps) {
    if (count === 0) return null;
    return (
        <div data-testid="authRequiredBanner" className="flex items-start gap-3 rounded-xs border border-(--warning-w100)/40 bg-(--warning-w100)/10 p-4">
            <AlertTriangle className="mt-0.5 size-5 shrink-0 text-(--warning-w100)" />
            <div className="flex-1 text-sm">
                <div className="font-semibold">{count === 1 ? "1 example blocked on tool authorization" : `${count} examples blocked on tool authorization`}</div>
                <div className="mt-0.5 text-xs text-(--secondary-text-wMain)">
                    The {targetIsWorkflow ? "workflow" : "agent"} needed a tool login that the experiment's service account does not hold. Connect the tools, then run the experiment again.
                </div>
            </div>
            {onConnectTools && (
                <Button size="sm" onClick={onConnectTools}>
                    Connect Tools
                </Button>
            )}
        </div>
    );
}
