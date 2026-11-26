// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { confidenceThresholdLabel, decisionQuestionTypeLabel } from "@/utils/decisionNode";

import type { EvaluatorQuestion } from "@/types/eval";

import { DECISION_READ_LABELS, DECISION_SCORING_LABELS, type DecisionSettingsView } from "./evaluatorForm";

function Row({ label, children }: { readonly label: string; readonly children: ReactNode }) {
    return (
        <div className="flex flex-col gap-1">
            <div className="text-sm font-semibold">{label}</div>
            <div className="text-sm">{children}</div>
        </div>
    );
}

function CriteriaList({ question, choiceScores }: { readonly question: EvaluatorQuestion; readonly choiceScores?: Record<string, number> }) {
    const criteria = question.criteria ?? [];
    if (criteria.length === 0) return null;
    const showScores = question.type === "choice";
    return (
        <div className="flex flex-col gap-2" data-testid="decisionCriteria">
            {criteria.map(c => (
                <div key={c.key} className="flex items-start gap-3 rounded-sm bg-(--secondary-w10) px-4 py-2">
                    <span className="w-32 shrink-0 font-medium break-words">{c.key}</span>
                    <span className="flex-1 text-(--secondary-text-wMain)">{c.description}</span>
                    {showScores && choiceScores?.[c.key] !== undefined && <span className="w-14 shrink-0 text-right font-mono">{Math.round(choiceScores[c.key] * 100)}%</span>}
                </div>
            ))}
        </div>
    );
}

const ESCALATES_ON_PROVIDER_ERRORS = "on provider errors other than a rejected or denied API key or a configuration error";

const CRITERIA_LABEL: Record<EvaluatorQuestion["type"], string> = {
    choice: "Options",
    score: "Levels (lowest first)",
    boolean: "Answers",
};

export function DecisionEvaluatorSummary({ settings, model }: { readonly settings: DecisionSettingsView; readonly model?: ReactNode }) {
    const { question, scoring, reads, minConfidence, choiceScores, escalateTo } = settings;
    let escalation = "None";
    if (escalateTo) {
        escalation = minConfidence === undefined ? `${escalateTo}, ${ESCALATES_ON_PROVIDER_ERRORS}` : `${escalateTo}, below ${confidenceThresholdLabel(minConfidence)} or ${ESCALATES_ON_PROVIDER_ERRORS}`;
    }
    return (
        <div className="flex max-w-2xl flex-col gap-6" data-testid="decisionEvaluatorSummary">
            {model !== undefined && <Row label="Decision Model">{model}</Row>}
            {question && (
                <>
                    <Row label="Question">
                        <span className="mb-1 block text-xs text-(--secondary-text-wMain)">{decisionQuestionTypeLabel(question.type)}</span>
                        <span className="whitespace-pre-wrap">{question.instructions}</span>
                    </Row>
                    {(question.criteria?.length ?? 0) > 0 && (
                        <Row label={CRITERIA_LABEL[question.type]}>
                            <CriteriaList question={question} choiceScores={choiceScores} />
                        </Row>
                    )}
                </>
            )}
            {scoring && question?.type !== "boolean" && <Row label="Scoring">{DECISION_SCORING_LABELS[scoring]}</Row>}
            {reads && reads.length > 0 && <Row label="Reads">{reads.map(r => DECISION_READ_LABELS[r] ?? r).join(", ")}</Row>}
            <Row label="Escalation">{escalation}</Row>
        </div>
    );
}
