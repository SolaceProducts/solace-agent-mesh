// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type DecisionEscalationReason = "low_confidence" | "provider_error";

export interface DecisionAnswer {
    key: string;
    type: string;
    choice?: string;
    confidence?: number;
    value?: boolean;
    probability?: number;
    score?: number;
    level?: string;
}

export interface DecisionResult {
    answers: DecisionAnswer[];
    selectedBranch?: string;
    routedBranch?: string;
    escalated: boolean;
    escalationReason?: DecisionEscalationReason;
    providerError?: string;
    model?: string;
    durationMs?: number;
}

type Metadata = Record<string, unknown> | undefined | null;

const isRecord = (value: unknown): value is Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value);
const optionalNumber = (value: unknown) => (typeof value === "number" ? value : undefined);
const optionalString = (value: unknown) => (typeof value === "string" && value !== "" ? value : undefined);

function scoreLevel(score: number | undefined, legend: unknown): string | undefined {
    if (score === undefined || !isRecord(legend)) return undefined;
    return optionalString(legend[String(Math.round(score))]);
}

function parseAnswer(key: string, raw: unknown): DecisionAnswer | null {
    if (!isRecord(raw)) return null;
    const score = optionalNumber(raw.score);
    return {
        key,
        type: typeof raw.type === "string" ? raw.type : "",
        choice: optionalString(raw.choice),
        confidence: optionalNumber(raw.confidence),
        value: typeof raw.value === "boolean" ? raw.value : undefined,
        probability: optionalNumber(raw.probability),
        score,
        level: scoreLevel(score, raw.legend),
    };
}

function escalationReasonOf(value: unknown): DecisionEscalationReason | undefined {
    return value === "low_confidence" || value === "provider_error" ? value : undefined;
}

export function decisionResultOf(metadata: Metadata): DecisionResult | null {
    if (!metadata || !isRecord(metadata.answers) || typeof metadata.escalated !== "boolean") return null;
    const answers = Object.entries(metadata.answers)
        .map(([key, raw]) => parseAnswer(key, raw))
        .filter((answer): answer is DecisionAnswer => answer !== null);
    return {
        answers,
        selectedBranch: optionalString(metadata.selected_branch),
        routedBranch: optionalString(metadata.routed_branch),
        escalated: metadata.escalated,
        escalationReason: escalationReasonOf(metadata.escalation_reason),
        providerError: optionalString(metadata.provider_error),
        model: optionalString(metadata.model),
        durationMs: optionalNumber(metadata.duration_ms),
    };
}

export function decisionRoute(result: DecisionResult): string | undefined {
    return result.routedBranch ?? result.selectedBranch;
}

export function decisionEscalationLabel(result: DecisionResult): string {
    switch (result.escalationReason) {
        case "low_confidence":
            return "escalated · low confidence";
        case "provider_error":
            return "escalated · provider error";
        default:
            return "escalated";
    }
}

export function formatProbability(value: number): string {
    return `${Math.round(value * 100)}%`;
}

function booleanAnswerLabel(value: boolean | undefined): string {
    if (value === undefined) {
        return "";
    }
    return value ? "yes" : "no";
}

export function decisionAnswerSummary(answer: DecisionAnswer): string {
    const withConfidence = (text: string) => (answer.confidence === undefined ? text : `${text} · ${formatProbability(answer.confidence)} confidence`);
    switch (answer.type) {
        case "boolean": {
            const value = booleanAnswerLabel(answer.value);
            if (answer.probability === undefined) {
                return value;
            }
            const shownAnswerProbability = answer.value === false ? 1 - answer.probability : answer.probability;
            return `${value} · ${formatProbability(shownAnswerProbability)} likely`;
        }
        case "score": {
            const score = answer.score === undefined ? "" : String(answer.score);
            return withConfidence(answer.level ? `${score} (${answer.level})` : score);
        }
        default:
            return withConfidence(answer.choice ?? "");
    }
}

export function decisionHeadlineAnswer(result: DecisionResult): DecisionAnswer | undefined {
    const choices = result.answers.filter(answer => answer.type === "choice");
    return choices.length === 1 ? choices[0] : undefined;
}
