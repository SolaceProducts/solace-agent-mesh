// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { DecisionAnswer, EvalEvaluatorType, EvalRun, EvalScoreDetails, EvaluatorRunStats, EvaluatorScoring } from "@/types/eval";

const ESCALATION_REASON_LABELS: Record<string, string> = {
    low_confidence: "low confidence",
    no_confidence: "no confidence",
    provider_error: "provider error",
    model_lookup_failed: "model lookup failed",
};

function percent(fraction: number): number {
    return Math.round(Math.min(Math.max(fraction, 0), 1) * 100);
}

export type DecisionChipTone = "decided" | "escalated" | "failed";

export interface DecisionChip {
    label: string;
    tone: DecisionChipTone;
}

export function decisionChip(details: EvalScoreDetails | undefined): DecisionChip | undefined {
    if (!details) return undefined;
    if (details.escalated) {
        const reason = details.escalationReason ? ESCALATION_REASON_LABELS[details.escalationReason] : undefined;
        return { label: reason ? `Escalated · ${reason}` : "Escalated", tone: "escalated" };
    }
    if (!details.answer) {
        return details.providerError ? { label: `Decision failed · ${details.providerError.replaceAll("_", " ")}`, tone: "failed" } : undefined;
    }
    const { confidence } = details.answer;
    const confidenceLabel = typeof confidence === "number" && Number.isFinite(confidence) ? `${percent(confidence)}%` : "confidence not reported";
    return { label: `Decided by ${details.model || "decision model"} · ${confidenceLabel}`, tone: "decided" };
}

export function decisionChipLabel(details: EvalScoreDetails | undefined): string | undefined {
    return decisionChip(details)?.label;
}

export interface ProbabilityBar {
    key: string;
    percent: number;
    chosen: boolean;
}

export function decisionProbabilityBars(answer: DecisionAnswer | undefined): ProbabilityBar[] {
    if (!answer) return [];
    let probs = answer.probabilities;
    if ((!probs || Object.keys(probs).length === 0) && answer.probability !== undefined) {
        probs = { true: answer.probability, false: 1 - answer.probability };
    }
    if (!probs) return [];
    return Object.entries(probs)
        .map(([key, p]) => ({ key, percent: percent(p), chosen: key === answer.choice }))
        .sort((a, b) => b.percent - a.percent);
}

export function scoringFallbackNote(answer: DecisionAnswer | undefined, configuredScoring: EvaluatorScoring | undefined): string | undefined {
    return configuredScoring === "expected" && answer?.scoringApplied === "chosen" ? "Scored as chosen (probabilities incomplete)" : undefined;
}

export function isJudgedEvaluatorType(type: string | undefined): boolean {
    return type === "llm_judge" || type === "decision";
}

const EXPLAINED_HEURISTICS: ReadonlySet<string> = new Set(["workflow_path", "output_facts"]);

export function explainsItsScore(ev: { type?: string; heuristicType?: string }): boolean {
    return isJudgedEvaluatorType(ev.type) || EXPLAINED_HEURISTICS.has(ev.heuristicType ?? "");
}

export function reasoningHeading(evaluators: readonly { type?: string }[]): string {
    return evaluators.every(ev => isJudgedEvaluatorType(ev.type)) ? "Judge Reasoning" : "Reasoning";
}

export function knownEvaluatorType(type: string | undefined): EvalEvaluatorType | undefined {
    return type === "llm_judge" || type === "heuristic" || type === "decision" ? type : undefined;
}

export function aggregateEvaluatorStats(runs: readonly Pick<EvalRun, "evaluatorStats">[], evaluatorId: string): EvaluatorRunStats | undefined {
    const all = runs.map(r => r.evaluatorStats?.[evaluatorId]).filter((s): s is EvaluatorRunStats => !!s);
    if (all.length === 0) return undefined;
    const scored = all.reduce((n, s) => n + s.scored, 0);
    const weighted = (pick: (s: EvaluatorRunStats) => number | undefined, weightOf: (s: EvaluatorRunStats) => number): number | undefined => {
        const present = all.filter(s => pick(s) !== undefined && weightOf(s) > 0);
        const weight = present.reduce((n, s) => n + weightOf(s), 0);
        if (weight === 0) return undefined;
        return present.reduce((sum, s) => sum + (pick(s) as number) * weightOf(s), 0) / weight;
    };
    const sumOf = (pick: (s: EvaluatorRunStats) => number | undefined): number | undefined => all.reduce((n, s) => n + (pick(s) ?? 0), 0) || undefined;
    const sumUsage = (pick: (s: EvaluatorRunStats) => { inputTokens: number; outputTokens: number } | undefined) => {
        const parts = all.map(pick).filter((u): u is { inputTokens: number; outputTokens: number } => !!u);
        if (parts.length === 0) return undefined;
        return { inputTokens: parts.reduce((n, u) => n + u.inputTokens, 0), outputTokens: parts.reduce((n, u) => n + u.outputTokens, 0) };
    };
    return {
        scored,
        escalated: sumOf(s => s.escalated),
        withConfidence: sumOf(s => s.withConfidence),
        escalatedPercent: weighted(
            s => s.escalatedPercent,
            s => s.scored
        ),
        meanConfidence: weighted(
            s => s.meanConfidence,
            s => s.withConfidence ?? s.scored
        ),
        judgeUsage: { decision: sumUsage(s => s.judgeUsage?.decision), llmJudge: sumUsage(s => s.judgeUsage?.llmJudge) },
    };
}

export function aggregateEvaluatorStatsForModels(runs: readonly Pick<EvalRun, "model" | "evaluatorStats">[], models: readonly string[], evaluatorId: string): EvaluatorRunStats | undefined {
    return aggregateEvaluatorStats(
        runs.filter(r => models.includes(r.model)),
        evaluatorId
    );
}

export function evaluatorStatsLine(stats: EvaluatorRunStats | undefined): string | undefined {
    if (!stats) return undefined;
    const parts: string[] = [];
    if (stats.escalatedPercent !== undefined) parts.push(`Escalated ${Math.round(stats.escalatedPercent)}%`);
    if (stats.meanConfidence !== undefined) parts.push(`Mean confidence ${percent(stats.meanConfidence)}%`);
    return parts.length > 0 ? parts.join(" · ") : undefined;
}
