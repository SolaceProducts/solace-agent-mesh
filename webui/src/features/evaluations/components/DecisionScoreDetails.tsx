// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import type { EvalScoreDetails, EvaluatorScoring } from "@/types/eval";

import { decisionChip, decisionChipLabel, type DecisionChipTone, decisionProbabilityBars, scoringFallbackNote } from "./decisionScore";

export function DecisionScoreChip({ details, className }: { readonly details?: EvalScoreDetails; readonly className?: string }) {
    const chip = decisionChip(details);
    if (!chip) return null;
    return (
        <span className={cn("inline-flex shrink-0 items-center rounded-sm px-1.5 py-0.5 text-xs font-medium whitespace-nowrap", CHIP_TONE_CLASSES[chip.tone], className)} data-testid="decisionScoreChip" data-tone={chip.tone}>
            {chip.label}
        </span>
    );
}

const CHIP_TONE_CLASSES: Record<DecisionChipTone, string> = {
    decided: "bg-(--info-w10) text-(--info-w100)",
    escalated: "bg-(--warning-w10) text-(--warning-w100)",
    failed: "bg-(--error-wMain)/10 text-(--error-wMain)",
};

export function DecisionProbabilityBars({ details }: { readonly details?: EvalScoreDetails }) {
    const bars = decisionProbabilityBars(details?.answer);
    if (bars.length === 0) return null;
    return (
        <div className="flex flex-col gap-1" data-testid="decisionProbabilityBars">
            {bars.map(bar => (
                <div key={bar.key} className="flex items-center gap-2 text-xs" title={`${bar.key}: ${bar.percent}%`}>
                    <span className={cn("w-24 shrink-0 truncate", bar.chosen ? "font-semibold text-(--primary-text-wMain)" : "text-(--secondary-text-wMain)")}>{bar.key}</span>
                    <div className="relative h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-(--secondary-w20)">
                        <meter className="sr-only" min={0} max={100} value={bar.percent} aria-label={`${bar.key} probability`}>
                            {bar.percent}%
                        </meter>
                        <div aria-hidden className={cn("h-full rounded-full", bar.chosen ? "bg-(--primary-wMain)" : "bg-(--secondary-w40)")} style={{ width: `${bar.percent}%` }} />
                    </div>
                    <span className="w-9 shrink-0 text-right font-mono text-(--secondary-text-wMain)">{bar.percent}%</span>
                </div>
            ))}
        </div>
    );
}

export function ScoringFallbackNote({ details, scoring }: { readonly details?: EvalScoreDetails; readonly scoring?: EvaluatorScoring }) {
    const note = scoringFallbackNote(details?.answer, scoring);
    if (!note) return null;
    return (
        <p className="text-xs text-(--secondary-text-wMain)" data-testid="scoringFallbackNote">
            {note}
        </p>
    );
}

export function DecisionScoreDetails({ details, scoring }: { readonly details?: EvalScoreDetails; readonly scoring?: EvaluatorScoring }) {
    if (!decisionChipLabel(details)) return null;
    return (
        <div className="flex flex-col gap-2">
            <DecisionScoreChip details={details} className="self-start" />
            <DecisionProbabilityBars details={details} />
            <ScoringFallbackNote details={details} scoring={scoring} />
        </div>
    );
}
