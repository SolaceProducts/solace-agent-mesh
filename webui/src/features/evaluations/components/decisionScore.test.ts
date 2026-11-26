// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { aggregateEvaluatorStats, aggregateEvaluatorStatsForModels, decisionChip, decisionChipLabel, decisionProbabilityBars, evaluatorStatsLine, isJudgedEvaluatorType, knownEvaluatorType, scoringFallbackNote } from "./decisionScore";

describe("scoringFallbackNote", () => {
    test("notes an answer scored as chosen under expected scoring", () => {
        expect(scoringFallbackNote({ choice: "pass", scoringApplied: "chosen" }, "expected")).toBe("Scored as chosen (probabilities incomplete)");
    });

    test("is empty when the configured scoring was applied, or either is unknown", () => {
        expect(scoringFallbackNote({ choice: "pass", scoringApplied: "expected" }, "expected")).toBeUndefined();
        expect(scoringFallbackNote({ choice: "pass", scoringApplied: "chosen" }, "chosen")).toBeUndefined();
        expect(scoringFallbackNote({ choice: "pass", scoringApplied: "chosen" }, undefined)).toBeUndefined();
        expect(scoringFallbackNote({ choice: "true", probability: 0.9 }, "expected")).toBeUndefined();
        expect(scoringFallbackNote(undefined, "expected")).toBeUndefined();
    });
});

describe("decisionChipLabel", () => {
    test("names the model and its confidence for an answer the decision model gave", () => {
        expect(decisionChipLabel({ decidedBy: "decision", model: "jev-1.13.0", answer: { choice: "pass", confidence: 0.934 } })).toBe("Decided by jev-1.13.0 · 93%");
        expect(decisionChip({ decidedBy: "decision", answer: { choice: "pass", confidence: 1 } })).toEqual({ label: "Decided by decision model · 100%", tone: "decided" });
    });

    test("says the confidence was not reported when the provider sent none, never NaN%", () => {
        expect(decisionChip({ decidedBy: "decision", model: "jev-1.13.0", answer: { choice: "pass", probabilities: { pass: 0.6, fail: 0.4 } } })).toEqual({ label: "Decided by jev-1.13.0 · confidence not reported", tone: "decided" });
        expect(decisionChipLabel({ decidedBy: "decision", answer: { choice: "pass", confidence: Number.NaN } })).toBe("Decided by decision model · confidence not reported");
    });

    test("an errored score that never escalated names the failure, not a decision", () => {
        expect(decisionChip({ providerError: "rate_limited", model: "jev-1.13.0", durationMs: 30 })).toEqual({ label: "Decision failed · rate limited", tone: "failed" });
        expect(decisionChipLabel({ providerError: "unauthorized" })).toBe("Decision failed · unauthorized");
        expect(decisionChipLabel({ decidedBy: "decision", providerError: "timeout" })).toBe("Decision failed · timeout");
    });

    test("an errored score without a failure category, as in a cancelled run, has no chip", () => {
        expect(decisionChip({ durationMs: 12 })).toBeUndefined();
        expect(decisionChip({ decidedBy: "decision", durationMs: 12 })).toBeUndefined();
    });

    test("says why a case was escalated", () => {
        expect(decisionChipLabel({ decidedBy: "llm_judge", escalated: true, escalationReason: "low_confidence" })).toBe("Escalated · low confidence");
        expect(decisionChipLabel({ decidedBy: "llm_judge", escalated: true, escalationReason: "provider_error" })).toBe("Escalated · provider error");
        expect(decisionChipLabel({ decidedBy: "llm_judge", escalated: true, escalationReason: "no_confidence", answer: { choice: "pass" } })).toBe("Escalated · no confidence");
        expect(decisionChip({ decidedBy: "llm_judge", escalated: true, escalationReason: "model_lookup_failed" })).toEqual({ label: "Escalated · model lookup failed", tone: "escalated" });
        expect(decisionChipLabel({ decidedBy: "llm_judge", escalated: true })).toBe("Escalated");
    });

    test("an escalated case whose LLM judge then failed still says why it escalated", () => {
        expect(decisionChip({ escalated: true, escalationReason: "low_confidence", answer: { choice: "pass", confidence: 0.4 } })).toEqual({ label: "Escalated · low confidence", tone: "escalated" });
        expect(decisionChipLabel({ escalated: true, escalationReason: "provider_error", providerError: "timeout" })).toBe("Escalated · provider error");
    });

    test("is empty for a plain LLM-judge score, or none", () => {
        expect(decisionChipLabel({ decidedBy: "llm_judge", usage: { llmJudge: { inputTokens: 1, outputTokens: 1 } } })).toBeUndefined();
        expect(decisionChipLabel(undefined)).toBeUndefined();
    });
});

describe("decisionProbabilityBars", () => {
    test("one bar per option, most likely first, the chosen one marked", () => {
        expect(decisionProbabilityBars({ choice: "b", confidence: 0.7, probabilities: { a: 0.2, b: 0.7, c: 0.1 } })).toEqual([
            { key: "b", percent: 70, chosen: true },
            { key: "a", percent: 20, chosen: false },
            { key: "c", percent: 10, chosen: false },
        ]);
    });

    test("a boolean answer becomes a true and a false bar", () => {
        expect(decisionProbabilityBars({ choice: "true", probability: 0.8, confidence: 0.8 })).toEqual([
            { key: "true", percent: 80, chosen: true },
            { key: "false", percent: 20, chosen: false },
        ]);
    });

    test("bars do not need a confidence", () => {
        expect(decisionProbabilityBars({ choice: "a", probabilities: { a: 0.6, b: 0.4 } })).toEqual([
            { key: "a", percent: 60, chosen: true },
            { key: "b", percent: 40, chosen: false },
        ]);
    });

    test("nothing without probabilities", () => {
        expect(decisionProbabilityBars({ confidence: 0.5 })).toEqual([]);
        expect(decisionProbabilityBars(undefined)).toEqual([]);
    });
});

describe("evaluator types", () => {
    test("judged types are llm_judge and decision", () => {
        expect(isJudgedEvaluatorType("llm_judge")).toBe(true);
        expect(isJudgedEvaluatorType("decision")).toBe(true);
        expect(isJudgedEvaluatorType("heuristic")).toBe(false);
        expect(isJudgedEvaluatorType(undefined)).toBe(false);
    });

    test("knownEvaluatorType narrows a snapshot's type", () => {
        expect(knownEvaluatorType("decision")).toBe("decision");
        expect(knownEvaluatorType("heuristic")).toBe("heuristic");
        expect(knownEvaluatorType("future")).toBeUndefined();
    });
});

describe("aggregateEvaluatorStatsForModels", () => {
    const stats = (scored: number, escalated: number) => ({ scored, escalated, judgeUsage: {} });
    const runs = [
        { model: "model-a", evaluatorStats: { ev: stats(10, 1) } },
        { model: "model-b", evaluatorStats: { ev: stats(10, 9) } },
    ];

    test("leaves out the runs of models that are filtered away", () => {
        const out = aggregateEvaluatorStatsForModels(runs, ["model-a"], "ev");
        expect(out?.scored).toBe(10);
        expect(out?.escalated).toBe(1);
    });

    test("covers every run when every model is on show", () => {
        const out = aggregateEvaluatorStatsForModels(runs, ["model-a", "model-b"], "ev");
        expect(out?.scored).toBe(20);
        expect(out?.escalated).toBe(10);
    });

    test("has no stats when no visible model's run carries any", () => {
        expect(aggregateEvaluatorStatsForModels(runs, ["model-c"], "ev")).toBeUndefined();
    });
});

describe("aggregateEvaluatorStats", () => {
    test("weights each run by the scores it counted and sums usage", () => {
        const stats = aggregateEvaluatorStats(
            [
                { evaluatorStats: { ev: { scored: 3, escalatedPercent: 0, meanConfidence: 0.9, judgeUsage: { decision: { inputTokens: 10, outputTokens: 1 } } } } },
                { evaluatorStats: { ev: { scored: 1, escalatedPercent: 100, meanConfidence: 0.5, judgeUsage: { decision: { inputTokens: 5, outputTokens: 1 }, llmJudge: { inputTokens: 100, outputTokens: 20 } } } } },
                { evaluatorStats: {} },
                {},
            ],
            "ev"
        );
        expect(stats?.scored).toBe(4);
        expect(stats?.escalatedPercent).toBeCloseTo(25);
        expect(stats?.meanConfidence).toBeCloseTo(0.8);
        expect(stats?.judgeUsage).toEqual({ decision: { inputTokens: 15, outputTokens: 2 }, llmJudge: { inputTokens: 100, outputTokens: 20 } });
    });

    test("rates escalation from the counts and weights confidence by the cases each run answered with a confidence", () => {
        const stats = aggregateEvaluatorStats(
            [
                { evaluatorStats: { ev: { scored: 10, escalated: 2, withConfidence: 4, escalatedPercent: 20, meanConfidence: 0.5, judgeUsage: {} } } },
                { evaluatorStats: { ev: { scored: 2, escalated: 1, withConfidence: 2, escalatedPercent: 50, meanConfidence: 0.8, judgeUsage: {} } } },
            ],
            "ev"
        );
        expect(stats?.scored).toBe(12);
        expect(stats?.escalated).toBe(3);
        expect(stats?.withConfidence).toBe(6);
        expect(stats?.escalatedPercent).toBeCloseTo(25);
        expect(stats?.meanConfidence).toBeCloseTo(0.6);
    });

    test("a run with no confidence, its zero count omitted, does not pull the mean confidence", () => {
        const stats = aggregateEvaluatorStats(
            [{ evaluatorStats: { ev: { scored: 5, escalated: 5, escalatedPercent: 100, judgeUsage: {} } } }, { evaluatorStats: { ev: { scored: 1, withConfidence: 1, escalatedPercent: 0, meanConfidence: 0.9, judgeUsage: {} } } }],
            "ev"
        );
        expect(stats?.escalated).toBe(5);
        expect(stats?.withConfidence).toBe(1);
        expect(stats?.escalatedPercent).toBeCloseTo(83.33, 1);
        expect(stats?.meanConfidence).toBeCloseTo(0.9);
    });

    test("reads an omitted count as zero, and leaves out a summed count that is zero", () => {
        const stats = aggregateEvaluatorStats(
            [
                { evaluatorStats: { ev: { scored: 3, withConfidence: 3, escalatedPercent: 0, meanConfidence: 0.9, judgeUsage: {} } } },
                { evaluatorStats: { ev: { scored: 1, withConfidence: 1, escalatedPercent: 0, meanConfidence: 0.5, judgeUsage: {} } } },
            ],
            "ev"
        );
        expect(stats?.escalated).toBeUndefined();
        expect(stats?.withConfidence).toBe(4);
        expect(stats?.escalatedPercent).toBe(0);
        expect(stats?.meanConfidence).toBeCloseTo(0.8);
    });

    test("an LLM judge's stats give it no escalation rate and no counts", () => {
        const stats = aggregateEvaluatorStats([{ evaluatorStats: { ev: { scored: 4, judgeUsage: {} } } }], "ev");
        expect(stats?.escalated).toBeUndefined();
        expect(stats?.withConfidence).toBeUndefined();
        expect(stats?.escalatedPercent).toBeUndefined();
        expect(stats?.meanConfidence).toBeUndefined();
    });

    test("is undefined when no run has stats for the evaluator", () => {
        expect(aggregateEvaluatorStats([{}, { evaluatorStats: { other: { scored: 1, judgeUsage: {} } } }], "ev")).toBeUndefined();
    });

    test("leaves out a figure no run reports", () => {
        const stats = aggregateEvaluatorStats([{ evaluatorStats: { ev: { scored: 2, judgeUsage: {} } } }], "ev");
        expect(stats?.escalatedPercent).toBeUndefined();
        expect(stats?.meanConfidence).toBeUndefined();
        expect(stats?.judgeUsage).toEqual({ decision: undefined, llmJudge: undefined });
    });
});

describe("evaluatorStatsLine", () => {
    test("reads escalation and mean confidence", () => {
        expect(evaluatorStatsLine({ scored: 4, escalatedPercent: 12.4, meanConfidence: 0.873, judgeUsage: {} })).toBe("Escalated 12% · Mean confidence 87%");
        expect(evaluatorStatsLine({ scored: 4, meanConfidence: 0.5, judgeUsage: {} })).toBe("Mean confidence 50%");
    });

    test("is empty for an LLM judge's stats, or none", () => {
        expect(evaluatorStatsLine({ scored: 4, judgeUsage: {} })).toBeUndefined();
        expect(evaluatorStatsLine(undefined)).toBeUndefined();
    });
});
