// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { cn } from "@/utils/cnTailwind";

import { useClampOverflow } from "@/ui/hooks";
import type { EvalRunResult } from "@/types/eval";

import { trialOutcomeText, trialProblem } from "../utils";

import { TrialOutcomeNote, WorkflowTrialDetails } from "./WorkflowTrialDetails";

interface TrialResponseProps {
    readonly result: EvalRunResult | undefined;
    readonly inset?: boolean;
}

export function TrialResponse({ result, inset = true }: TrialResponseProps) {
    const [expanded, setExpanded] = useState(false);
    const text = result?.agentResponse ?? "";
    const { ref: responseRef, isOverflowing } = useClampOverflow<HTMLDivElement>(!expanded, [text]);
    if (!result) {
        return <div className={cn("text-sm text-(--secondary-text-wMain)", inset && "px-4 py-3")}>No result for this trial.</div>;
    }
    if (trialProblem(result) === "ended") {
        return (
            <>
                <TrialOutcomeNote className={cn(inset && "m-4")} {...trialOutcomeText(result.failureKind ?? "", result.errorMessage ?? "")} testId="trialEndedEarly" />
                <WorkflowTrialDetails result={result} />
            </>
        );
    }
    const partialFailure = result.failureKind === "partial_failure";
    if (result.errorMessage && !partialFailure) {
        return <div className={cn("rounded-xs bg-(--error-wMain)/5 p-3 text-sm break-words whitespace-pre-wrap text-(--error-wMain)", inset && "m-4")}>{result.errorMessage}</div>;
    }
    return (
        <div className={cn("flex flex-col gap-1", inset && "px-4 py-3")}>
            {partialFailure && <TrialOutcomeNote className="mb-2" {...trialOutcomeText("partial_failure", result.errorMessage ?? "")} testId="trialPartialFailure" />}
            <div ref={responseRef} className={cn("text-sm break-words whitespace-pre-wrap text-(--primary-text-wMain)", !expanded && "line-clamp-6")}>
                {text || "—"}
            </div>
            {(isOverflowing || expanded) && (
                <button type="button" onClick={() => setExpanded(prev => !prev)} className="self-start text-xs font-bold text-(--primary-wMain) hover:underline">
                    {expanded ? "Read Less" : "Read More"}
                </button>
            )}
            <WorkflowTrialDetails result={result} />
        </div>
    );
}
