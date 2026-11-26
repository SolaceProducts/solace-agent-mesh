// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import type { EvalScoreDetails, EvaluatorScoring } from "@/types/eval";

import { decisionProbabilityBars, scoringFallbackNote } from "./decisionScore";
import { DecisionProbabilityBars, DecisionScoreChip, ScoringFallbackNote } from "./DecisionScoreDetails";

interface ReasoningRowProps {
    readonly name: string;
    readonly reasoning?: string;
    readonly details?: EvalScoreDetails;
    readonly scoring?: EvaluatorScoring;
}

export function ReasoningRow({ name, reasoning, details, scoring }: ReasoningRowProps) {
    const [open, setOpen] = useState(false);
    const hasReasoning = !!reasoning && reasoning.trim().length > 0;
    const hasBars = decisionProbabilityBars(details?.answer).length > 0;
    const hasNote = !!scoringFallbackNote(details?.answer, scoring);
    const expandable = hasReasoning || hasBars || hasNote;
    return (
        <div className="flex w-full min-w-0 flex-col">
            <button
                type="button"
                onClick={() => expandable && setOpen(prev => !prev)}
                disabled={!expandable}
                aria-expanded={expandable ? open : undefined}
                className={cn("flex w-full min-w-0 items-center gap-1.5 px-3 py-2 text-left text-sm", expandable && "hover:bg-(--stateLayer-w10)", !expandable && "cursor-default opacity-60")}
            >
                {open ? <ChevronDown className="size-3.5 shrink-0" aria-hidden /> : <ChevronRight className="size-3.5 shrink-0" aria-hidden />}
                <span className="min-w-0 truncate" title={name}>
                    {name}
                </span>
                <DecisionScoreChip details={details} className="ml-auto" />
                {!expandable && <span className="shrink-0 text-xs text-(--secondary-text-wMain)">(no reasoning)</span>}
            </button>
            {open && (hasBars || hasNote) && (
                <div className="flex flex-col gap-1 border-t px-3 py-2">
                    <DecisionProbabilityBars details={details} />
                    <ScoringFallbackNote details={details} scoring={scoring} />
                </div>
            )}
            {open && hasReasoning && <blockquote className="border-t border-l-2 border-l-(--primary-wMain) px-3 py-2 text-sm break-words whitespace-pre-wrap text-(--secondary-text-wMain) italic">{reasoning}</blockquote>}
        </div>
    );
}
