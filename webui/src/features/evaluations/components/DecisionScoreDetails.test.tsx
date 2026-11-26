// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { Dialog } from "@/ui/dialog";
import type { EvalEvaluator, EvalScoreDetails, ExperimentModelConfig } from "@/types/eval";

import { makeEvalRun } from "@/testing/helpers/evaluations";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { DecisionScoreDetails } from "./DecisionScoreDetails";
import { EvaluatorResultCard } from "./EvaluatorResultCard";
import { EvaluatorSidePanel } from "./EvaluatorSidePanel";
import { EvaluatorTile } from "./EvaluatorTile";
import { LatestReportSection } from "./LatestReportSection";
import { ReasoningRow } from "./ReasoningRow";
import { EvaluatorDetailBody } from "./RunDetailsAccordion";

const decided: EvalScoreDetails = {
    decidedBy: "decision",
    model: "jev-1.13.0",
    answer: { choice: "grounded", confidence: 0.93, probabilities: { grounded: 0.93, made_up: 0.07 } },
    usage: { decision: { inputTokens: 120, outputTokens: 4 } },
};

const escalated: EvalScoreDetails = {
    decidedBy: "llm_judge",
    escalated: true,
    escalationReason: "low_confidence",
    answer: { choice: "grounded", confidence: 0.55, probabilities: { grounded: 0.55, made_up: 0.45 } },
};

describe("DecisionScoreDetails", () => {
    test("shows who decided and how sure, with a bar per option", () => {
        render(<DecisionScoreDetails details={decided} />);
        expect(screen.getByTestId("decisionScoreChip")).toHaveTextContent("Decided by jev-1.13.0 · 93%");
        const bars = screen.getAllByRole("meter");
        expect(bars.map(b => b.getAttribute("aria-label"))).toEqual(["grounded probability", "made_up probability"]);
        expect(bars[0]).toHaveValue(93);
    });

    test("says why a case was escalated", () => {
        render(<DecisionScoreDetails details={{ decidedBy: "llm_judge", escalated: true, escalationReason: "provider_error", providerError: "timeout" }} />);
        expect(screen.getByTestId("decisionScoreChip")).toHaveTextContent("Escalated · provider error");
        expect(screen.queryByRole("meter")).toBeNull();
    });

    test("an errored decision score shows the failure, not a decision", () => {
        render(<DecisionScoreDetails details={{ providerError: "timeout", model: "jev-1.13.0", durationMs: 30000 }} />);
        const chip = screen.getByTestId("decisionScoreChip");
        expect(chip).toHaveTextContent("Decision failed · timeout");
        expect(chip).toHaveAttribute("data-tone", "failed");
        expect(screen.queryByRole("meter")).toBeNull();
    });

    test("an escalated case whose LLM judge failed keeps the escalation chip and the decision model's bars", () => {
        render(<DecisionScoreDetails details={{ escalated: true, escalationReason: "low_confidence", answer: { choice: "grounded", confidence: 0.4, probabilities: { grounded: 0.4, made_up: 0.6 } } }} />);
        expect(screen.getByTestId("decisionScoreChip")).toHaveTextContent("Escalated · low confidence");
        expect(screen.getAllByRole("meter")).toHaveLength(2);
    });

    test("an answer without a confidence says so instead of a percentage", () => {
        render(<DecisionScoreDetails details={{ decidedBy: "decision", model: "jev-1.13.0", answer: { choice: "grounded", probabilities: { grounded: 0.7, made_up: 0.3 } } }} />);
        const chip = screen.getByTestId("decisionScoreChip");
        expect(chip).toHaveTextContent("Decided by jev-1.13.0 · confidence not reported");
        expect(chip).not.toHaveTextContent("NaN");
    });

    test("names the new escalation reasons", () => {
        const { rerender } = render(<DecisionScoreDetails details={{ decidedBy: "llm_judge", escalated: true, escalationReason: "no_confidence", answer: { choice: "grounded" } }} />);
        expect(screen.getByTestId("decisionScoreChip")).toHaveTextContent("Escalated · no confidence");
        rerender(<DecisionScoreDetails details={{ decidedBy: "llm_judge", escalated: true, escalationReason: "model_lookup_failed" }} />);
        expect(screen.getByTestId("decisionScoreChip")).toHaveTextContent("Escalated · model lookup failed");
    });

    test("notes an answer scored as chosen although the evaluator weights by probability", () => {
        const fellBack: EvalScoreDetails = { decidedBy: "decision", model: "jev-1.13.0", answer: { choice: "grounded", confidence: 0.9, probabilities: { grounded: 0.9 }, scoringApplied: "chosen" } };
        const { rerender } = render(<DecisionScoreDetails details={fellBack} scoring="expected" />);
        expect(screen.getByTestId("scoringFallbackNote")).toHaveTextContent("Scored as chosen (probabilities incomplete)");
        rerender(<DecisionScoreDetails details={fellBack} scoring="chosen" />);
        expect(screen.queryByTestId("scoringFallbackNote")).toBeNull();
    });

    test("renders nothing for a plain LLM-judge score", () => {
        const { container } = render(<DecisionScoreDetails details={{ decidedBy: "llm_judge" }} />);
        expect(container).toBeEmptyDOMElement();
    });
});

describe("ReasoningRow", () => {
    test("a decision evaluator's row carries its chip and expands to the probabilities", async () => {
        const user = userEvent.setup();
        render(<ReasoningRow name="Grounded" reasoning="grounded · 93% confidence (jev-1.13.0)" details={decided} />);

        expect(screen.getByTestId("decisionScoreChip")).toHaveTextContent("Decided by jev-1.13.0 · 93%");
        expect(screen.queryByRole("meter")).toBeNull();
        await user.click(screen.getByRole("button", { name: /Grounded/ }));
        expect(screen.getAllByRole("meter")).toHaveLength(2);
        expect(screen.getByText("grounded · 93% confidence (jev-1.13.0)")).toBeInTheDocument();
    });

    test("an escalated row shows the LLM judge's reasoning below the decision model's probabilities", async () => {
        const user = userEvent.setup();
        render(<ReasoningRow name="Grounded" reasoning="Escalated (low confidence): cites the docs" details={escalated} />);

        expect(screen.getByTestId("decisionScoreChip")).toHaveTextContent("Escalated · low confidence");
        await user.click(screen.getByRole("button", { name: /Grounded/ }));
        expect(screen.getAllByRole("meter")).toHaveLength(2);
        expect(screen.getByText("Escalated (low confidence): cites the docs")).toBeInTheDocument();
    });

    test("a row whose answer fell back to chosen scoring expands to the note, even without bars", async () => {
        const user = userEvent.setup();
        render(<ReasoningRow name="Grounded" details={{ decidedBy: "decision", answer: { choice: "grounded", confidence: 0.9, scoringApplied: "chosen" } }} scoring="expected" />);

        expect(screen.queryByTestId("scoringFallbackNote")).toBeNull();
        await user.click(screen.getByRole("button", { name: /Grounded/ }));
        expect(screen.queryByRole("meter")).toBeNull();
        expect(screen.getByTestId("scoringFallbackNote")).toHaveTextContent("Scored as chosen (probabilities incomplete)");
    });

    test("an LLM judge's row without reasoning stays closed", () => {
        render(<ReasoningRow name="Judge" />);
        expect(screen.getByRole("button", { name: /Judge/ })).toBeDisabled();
        expect(screen.getByText("(no reasoning)")).toBeInTheDocument();
        expect(screen.queryByTestId("decisionScoreChip")).toBeNull();
    });
});

const stats = { scored: 10, escalatedPercent: 20, meanConfidence: 0.86, judgeUsage: {} };
const models: ExperimentModelConfig[] = [{ modelConfigId: "m1", model: "gpt-4", alias: "GPT-4" }];

describe("evaluator stats", () => {
    test("the report tile shows escalation and mean confidence", () => {
        render(<EvaluatorTile evaluatorName="Grounded" scoresByModel={{ m1: [1, 0] }} allModels={["m1"]} experimentModels={models} stats={stats} />);
        expect(screen.getByTestId("evaluatorStatsLine")).toHaveTextContent("Escalated 20% · Mean confidence 86%");
    });

    test("the result card shows them too, and an LLM judge's card shows none", () => {
        const { rerender } = render(<EvaluatorResultCard evaluatorName="Grounded" models={models} scoresByModel={{ m1: 0.5 }} modelColorIndex={new Map([["m1", 0]])} stats={stats} />);
        expect(screen.getByTestId("evaluatorStatsLine")).toHaveTextContent("Escalated 20% · Mean confidence 86%");
        rerender(<EvaluatorResultCard evaluatorName="Judge" models={models} scoresByModel={{ m1: 0.5 }} modelColorIndex={new Map([["m1", 0]])} stats={{ scored: 3, judgeUsage: {} }} />);
        expect(screen.queryByTestId("evaluatorStatsLine")).toBeNull();
    });

    test("the latest report aggregates the stats of its runs", () => {
        const runs = [makeEvalRun({ id: "r1", model: "m1", evaluatorStats: { dec: { scored: 2, escalatedPercent: 50, meanConfidence: 0.7, judgeUsage: {} } } })];
        render(<LatestReportSection groupId="g" runs={runs} status="completed" startedAt="2026-01-01T00:00:00Z" models={models} evaluatorIds={["dec"]} evaluatorNameMap={new Map([["dec", "Grounded"]])} onViewFullReport={vi.fn()} />);
        expect(screen.getByTestId("evaluatorStatsLine")).toHaveTextContent("Escalated 50% · Mean confidence 70%");
    });
});

describe("decision evaluator settings views", () => {
    test("a run's evaluator snapshot shows the frozen decision settings and model", () => {
        render(
            <Dialog open>
                <EvaluatorDetailBody
                    evaluator={{
                        id: "dec",
                        name: "Grounded",
                        type: "decision",
                        model: "jev",
                        choice_scores: { grounded: 1, made_up: 0 },
                        decision_config: {
                            question: { type: "choice", instructions: "Is it grounded?", criteria: [{ key: "grounded" }, { key: "made_up" }] },
                            scoring: "expected",
                            reads: ["response"],
                            min_confidence: 0.75,
                            escalate_to_evaluator_id: "judge-1",
                            resolved_model: { ref: "jev", provider: "typesafe", model: "jev-1.13.0" },
                            escalate_to: { id: "judge-1", name: "Strict Judge", type: "llm_judge" },
                        },
                    }}
                    onOpenModel={vi.fn()}
                    onClose={vi.fn()}
                />
            </Dialog>
        );
        expect(screen.getByText("Decision Model as a Judge")).toBeInTheDocument();
        const summary = screen.getByTestId("decisionEvaluatorSummary");
        expect(within(summary).getByText("typesafe / jev-1.13.0")).toBeInTheDocument();
        expect(within(summary).getByText("Weighted by probability")).toBeInTheDocument();
        expect(within(summary).getByText("Strict Judge, below 75% confidence or on provider errors other than a rejected or denied API key or a configuration error")).toBeInTheDocument();
    });

    test("the evaluators side panel shows a decision evaluator's settings", () => {
        const ev: EvalEvaluator = {
            id: "dec",
            name: "Grounded",
            type: "decision",
            isDefault: false,
            model: "jev",
            question: { type: "score", instructions: "How grounded?", criteria: [{ key: "low" }, { key: "high", description: "Fully cited" }] },
            scoring: "chosen",
            reads: ["prompt", "response"],
            escalateToEvaluatorId: "judge-1",
            experimentCount: 0,
            createdBy: "u",
            updatedBy: "u",
            createdAt: "",
            updatedAt: "",
        };
        renderWithProviders(<EvaluatorSidePanel evaluator={ev} onClose={vi.fn()} />);
        const summary = screen.getByTestId("decisionEvaluatorSummary");
        expect(within(summary).getByText("How grounded?")).toBeInTheDocument();
        expect(within(summary).getByText("Levels (lowest first)")).toBeInTheDocument();
        expect(within(summary).getByText("Fully cited")).toBeInTheDocument();
        expect(within(summary).getByText("judge-1, on provider errors other than a rejected or denied API key or a configuration error")).toBeInTheDocument();
        expect(within(summary).getByText("jev")).toBeInTheDocument();
    });
});
