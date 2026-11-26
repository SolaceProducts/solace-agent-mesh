// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ModelConfig } from "@/api/models/types";

import { getErrorMessage } from "@/utils/api";
import { uuid } from "@/utils/uuid";

import type { CreateEvaluatorRequest, EvalEvaluator, EvaluatorCriterion, EvaluatorDecisionConfigSnapshot, EvaluatorQuestion, EvaluatorQuestionType, EvaluatorRead, EvaluatorScoring, UpdateEvaluatorRequest } from "@/types/eval";

export interface ChoiceScore {
    id: string;
    choice: string;
    score: number;
}

export interface DecisionOption {
    id: string;
    key: string;
    description: string;
    score: number;
}

export interface DecisionLevel {
    id: string;
    key: string;
    description: string;
}

export interface DecisionFormValue {
    model: string;
    questionType: EvaluatorQuestionType;
    instructions: string;
    options: DecisionOption[];
    levels: DecisionLevel[];
    trueDescription: string;
    falseDescription: string;
    scoring: EvaluatorScoring;
    reads: EvaluatorRead[];
    escalateToEvaluatorId: string;
    minConfidencePercent: string;
}

export type EvaluatorFormType = "llm_judge" | "decision";

export const EVALUATOR_FORM_TYPE_LABELS: Record<EvaluatorFormType, string> = {
    llm_judge: "LLM as a Judge",
    decision: "Decision Model as a Judge",
};

export interface EvaluatorFormValue {
    type: EvaluatorFormType;
    name: string;
    description: string;
    model: string;
    promptTemplate: string;
    choiceScores: ChoiceScore[];
    decision: DecisionFormValue;
}

export type EvaluatorFormField =
    "type" | "name" | "description" | "model" | "promptTemplate" | "choiceScores" | "decisionModel" | "question" | "instructions" | "options" | "levels" | "booleanCriteria" | "scoring" | "reads" | "escalateToEvaluatorId" | "minConfidence";

export type EvaluatorFieldErrors = Partial<Record<EvaluatorFormField, string>>;

export const MIN_CHOICES = 2;
export const MAX_CHOICES = 26;
export const MIN_LEVELS = 2;
export const MAX_LEVELS = 10;

export const DEFAULT_DECISION_READS: readonly EvaluatorRead[] = ["prompt", "response", "expected_response"];
export const ALL_DECISION_READS: readonly EvaluatorRead[] = ["prompt", "response", "expected_response", "transcript", "artifacts"];

export const DECISION_READ_LABELS: Record<EvaluatorRead, string> = {
    prompt: "Prompt",
    response: "Response",
    expected_response: "Expected response",
    transcript: "Transcript (the agent's tool calls and results, or a workflow's node-by-node run)",
    artifacts: "Artifacts (files given to the agent, the expected file, and files it produced)",
};

export const DECISION_SCORING_LABELS: Record<EvaluatorScoring, string> = {
    chosen: "Chosen answer",
    expected: "Weighted by probability",
};

const newId = (): string => uuid();

export function defaultChoiceScores(): ChoiceScore[] {
    return [
        { id: newId(), choice: "Fail", score: 0 },
        { id: newId(), choice: "Partial", score: 0.5 },
        { id: newId(), choice: "Pass", score: 1 },
    ];
}

export function choiceScoresFromRecord(record?: Record<string, number>): ChoiceScore[] {
    if (!record || Object.keys(record).length === 0) {
        return defaultChoiceScores();
    }
    return Object.entries(record).map(([choice, score]) => ({ id: newId(), choice, score }));
}

export function choiceScoresToRecord(scores: ChoiceScore[]): Record<string, number> | undefined {
    const obj: Record<string, number> = {};
    for (const cs of scores) {
        if (cs.choice.trim()) obj[cs.choice.trim()] = cs.score;
    }
    return Object.keys(obj).length > 0 ? obj : undefined;
}

export function newDecisionOption(key = "", score = 0): DecisionOption {
    return { id: newId(), key, description: "", score };
}

export function newDecisionLevel(key = ""): DecisionLevel {
    return { id: newId(), key, description: "" };
}

export function emptyDecisionFormValue(): DecisionFormValue {
    return {
        model: "",
        questionType: "choice",
        instructions: "",
        options: [newDecisionOption("fail", 0), newDecisionOption("pass", 1)],
        levels: [newDecisionLevel("poor"), newDecisionLevel("fair"), newDecisionLevel("good")],
        trueDescription: "",
        falseDescription: "",
        scoring: "chosen",
        reads: [...DEFAULT_DECISION_READS],
        escalateToEvaluatorId: "",
        minConfidencePercent: "",
    };
}

export function emptyEvaluatorFormValue(): EvaluatorFormValue {
    return {
        type: "llm_judge",
        name: "",
        description: "",
        model: "",
        promptTemplate: "",
        choiceScores: defaultChoiceScores(),
        decision: emptyDecisionFormValue(),
    };
}

export function decisionFormFromEvaluator(ev: Pick<EvalEvaluator, "model" | "question" | "scoring" | "reads" | "minConfidence" | "escalateToEvaluatorId" | "choiceScores">): DecisionFormValue {
    const base = emptyDecisionFormValue();
    const question = ev.question;
    const criteria = question?.criteria ?? [];
    const out: DecisionFormValue = {
        ...base,
        model: ev.model ?? "",
        questionType: question?.type ?? base.questionType,
        instructions: question?.instructions ?? "",
        scoring: ev.scoring ?? "chosen",
        reads: ev.reads && ev.reads.length > 0 ? [...ev.reads] : base.reads,
        escalateToEvaluatorId: ev.escalateToEvaluatorId ?? "",
        minConfidencePercent: minConfidencePercentOf(ev.minConfidence),
    };
    switch (question?.type) {
        case "choice":
            out.options = criteria.map(c => ({ ...newDecisionOption(c.key, ev.choiceScores?.[c.key] ?? 0), description: c.description ?? "" }));
            break;
        case "score":
            out.levels = criteria.map(c => ({ ...newDecisionLevel(c.key), description: c.description ?? "" }));
            break;
        case "boolean":
            out.trueDescription = criteria.find(c => c.key === "true")?.description ?? "";
            out.falseDescription = criteria.find(c => c.key === "false")?.description ?? "";
            break;
    }
    return out;
}

export function evaluatorFormFromEvaluator(ev: EvalEvaluator): EvaluatorFormValue {
    const empty = emptyEvaluatorFormValue();
    if (ev.type === "decision") {
        return { ...empty, type: "decision", name: ev.name, description: ev.description ?? "", decision: decisionFormFromEvaluator(ev) };
    }
    return {
        ...empty,
        name: ev.name,
        description: ev.description ?? "",
        model: ev.model ?? "",
        promptTemplate: ev.promptTemplate ?? "",
        choiceScores: choiceScoresFromRecord(ev.choiceScores),
    };
}

function minConfidenceOf(d: DecisionFormValue): number | undefined {
    const raw = d.minConfidencePercent.trim();
    if (raw === "") return undefined;
    const pct = Number(raw);
    return Number.isFinite(pct) ? Number((pct / 100).toFixed(6)) : Number.NaN;
}

function minConfidencePercentOf(minConfidence: number | undefined): string {
    return minConfidence == null ? "" : String(Number((minConfidence * 100).toFixed(4)));
}

function duplicateKey(keys: string[]): string | undefined {
    const seen = new Set<string>();
    for (const k of keys) {
        if (seen.has(k)) return k;
        seen.add(k);
    }
    return undefined;
}

const hasText = (s: string): boolean => s.trim() !== "";

function validateKeyedList(keys: string[], noun: string, min: number, max: number): string | undefined {
    if (keys.length < min) return `Add at least ${min} ${noun}s.`;
    if (keys.length > max) return `A question takes at most ${max} ${noun}s.`;
    if (keys.includes("")) return `Every ${noun} needs a name.`;
    const dup = duplicateKey(keys);
    if (dup !== undefined) return `The ${noun} "${dup}" is listed more than once.`;
    return undefined;
}

export function validateDecisionForm(d: DecisionFormValue): EvaluatorFieldErrors {
    const errors: EvaluatorFieldErrors = {};
    if (!d.model.trim()) errors.decisionModel = "Select a decision model.";
    if (!d.instructions.trim()) errors.instructions = "Write the question the decision model answers.";

    if (d.questionType === "choice") {
        const keys = d.options.map(o => o.key.trim());
        const listError = validateKeyedList(keys, "option", MIN_CHOICES, MAX_CHOICES);
        if (listError) errors.options = listError;
        else if (d.options.some(o => !Number.isFinite(o.score) || o.score < 0 || o.score > 1)) errors.options = "Every option's score must be from 0% to 100%.";
    } else if (d.questionType === "score") {
        const listError = validateKeyedList(
            d.levels.map(l => l.key.trim()),
            "level",
            MIN_LEVELS,
            MAX_LEVELS
        );
        if (listError) errors.levels = listError;
    } else if (hasText(d.trueDescription) !== hasText(d.falseDescription)) {
        errors.booleanCriteria = "Describe both true and false, or neither.";
    }

    if (!d.reads.includes("response")) errors.reads = "The decision model must read the response.";

    const mc = minConfidenceOf(d);
    if (mc !== undefined) {
        if (Number.isNaN(mc) || mc <= 0 || mc > 1) errors.minConfidence = "Enter a confidence above 0% and at most 100%.";
        else if (!d.escalateToEvaluatorId) errors.minConfidence = "Choose an evaluator to escalate to, or clear the threshold.";
    }
    return errors;
}

const REQUIRED_MESSAGES: ReadonlyArray<[EvaluatorFormField, string]> = [
    ["name", "Name is required"],
    ["description", "Description is required"],
];

export function validateEvaluatorForm(value: EvaluatorFormValue): EvaluatorFieldErrors {
    const errors: EvaluatorFieldErrors = {};
    const text: Record<string, string> = { name: value.name, description: value.description };
    for (const [field, message] of REQUIRED_MESSAGES) {
        if (!text[field].trim()) errors[field] = message;
    }
    if (value.type === "decision") return { ...errors, ...validateDecisionForm(value.decision) };
    if (!value.model.trim()) errors.model = "Model is required";
    if (!value.promptTemplate.trim()) errors.promptTemplate = "Criteria is required";
    return errors;
}

export function isEvaluatorFormComplete(value: EvaluatorFormValue): boolean {
    if (!value.name.trim() || !value.description.trim()) return false;
    if (value.type === "decision") return Boolean(value.decision.model.trim() && value.decision.instructions.trim());
    return Boolean(value.model.trim() && value.promptTemplate.trim());
}

export const DEFAULT_JUDGE_MODEL_LABEL = "Default (general model)";

export const FIX_FIELDS_MESSAGE = "Some settings need attention; see the messages beside them.";

export function saveErrorState(err: unknown, type: EvaluatorFormType, fallback?: string): { fieldErrors: EvaluatorFieldErrors; message: string } {
    const details = validationDetailsOf(err);
    if (details && type === "decision") {
        const { fieldErrors, unmapped } = mapValidationDetails(details, type);
        return { fieldErrors, message: unmapped.length > 0 ? unmapped.join("; ") : FIX_FIELDS_MESSAGE };
    }
    return { fieldErrors: {}, message: getErrorMessage(err, fallback) };
}

export function preSaveErrorState(value: EvaluatorFormValue): { fieldErrors: EvaluatorFieldErrors; message: string } | null {
    const errors = validateEvaluatorForm(value);
    const first = firstFieldError(errors);
    if (first === undefined) return null;
    return value.type === "decision" ? { fieldErrors: errors, message: FIX_FIELDS_MESSAGE } : { fieldErrors: {}, message: first };
}

export function errorIds(prefix: string, errors: EvaluatorFieldErrors) {
    const id = (field: EvaluatorFormField) => `${prefix}-${field}-error`;
    return { id, describedBy: (field: EvaluatorFormField) => (errors[field] ? id(field) : undefined) };
}

export function firstFieldError(errors: EvaluatorFieldErrors): string | undefined {
    return Object.values(errors).find(Boolean);
}

function criteriaOf(d: DecisionFormValue): EvaluatorCriterion[] | undefined {
    const criterion = (key: string, description: string): EvaluatorCriterion => {
        const desc = description.trim();
        return desc ? { key: key.trim(), description: desc } : { key: key.trim() };
    };
    switch (d.questionType) {
        case "choice":
            return d.options.map(o => criterion(o.key, o.description));
        case "score":
            return d.levels.map(l => criterion(l.key, l.description));
        case "boolean":
            return d.trueDescription.trim() && d.falseDescription.trim() ? [criterion("true", d.trueDescription), criterion("false", d.falseDescription)] : undefined;
    }
}

function questionOf(d: DecisionFormValue): EvaluatorQuestion {
    const criteria = criteriaOf(d);
    return criteria ? { type: d.questionType, instructions: d.instructions.trim(), criteria } : { type: d.questionType, instructions: d.instructions.trim() };
}

function choiceScoresOf(d: DecisionFormValue): Record<string, number> | undefined {
    if (d.questionType !== "choice") return undefined;
    return Object.fromEntries(d.options.map(o => [o.key.trim(), o.score]));
}

export function buildCreateEvaluatorRequest(value: EvaluatorFormValue): CreateEvaluatorRequest {
    const name = value.name.trim();
    const description = value.description.trim();
    if (value.type !== "decision") {
        return { name, description, type: "llm_judge", model: value.model.trim(), promptTemplate: value.promptTemplate.trim(), choiceScores: choiceScoresToRecord(value.choiceScores) };
    }
    const d = value.decision;
    const req: CreateEvaluatorRequest = { name, description, type: "decision", model: d.model.trim(), question: questionOf(d), scoring: d.scoring, reads: [...d.reads] };
    const choiceScores = choiceScoresOf(d);
    if (choiceScores) req.choiceScores = choiceScores;
    if (d.escalateToEvaluatorId) req.escalateToEvaluatorId = d.escalateToEvaluatorId;
    const mc = minConfidenceOf(d);
    if (mc !== undefined && d.escalateToEvaluatorId) req.minConfidence = mc;
    return req;
}

export function buildUpdateEvaluatorRequest(value: EvaluatorFormValue): UpdateEvaluatorRequest {
    const name = value.name.trim();
    const description = value.description.trim();
    if (value.type !== "decision") {
        return { name, description, model: value.model.trim(), promptTemplate: value.promptTemplate.trim(), choiceScores: choiceScoresToRecord(value.choiceScores) };
    }
    const d = value.decision;
    const escalateTo = d.escalateToEvaluatorId || null;
    const mc = minConfidenceOf(d);
    return {
        name,
        description,
        model: d.model.trim(),
        question: questionOf(d),
        scoring: d.scoring,
        reads: [...d.reads],
        choiceScores: choiceScoresOf(d) ?? null,
        escalateToEvaluatorId: escalateTo,
        minConfidence: mc !== undefined && escalateTo ? mc : null,
    };
}

function formFieldFor(key: string, type: EvaluatorFormType): EvaluatorFormField | undefined {
    const decision = type === "decision";
    switch (key) {
        case "evaluatorType":
            return "type";
        case "name":
        case "description":
        case "type":
        case "question":
        case "scoring":
        case "reads":
        case "minConfidence":
        case "escalateToEvaluatorId":
            return key;
        case "model":
            return decision ? "decisionModel" : "model";
        case "promptTemplate":
        case "prompt_template":
            return "promptTemplate";
        case "choiceScores":
        case "choice_scores":
            return decision ? "options" : "choiceScores";
        default:
            return undefined;
    }
}

export function mapValidationDetails(details: Record<string, string[]>, type: EvaluatorFormType): { fieldErrors: EvaluatorFieldErrors; unmapped: string[] } {
    const fieldErrors: EvaluatorFieldErrors = {};
    const unmapped: string[] = [];
    for (const [key, messages] of Object.entries(details)) {
        const text = messages.join(" ");
        const field = formFieldFor(key, type);
        if (field) fieldErrors[field] = fieldErrors[field] ? `${fieldErrors[field]} ${text}` : text;
        else unmapped.push(`${key}: ${text}`);
    }
    return { fieldErrors, unmapped };
}

export function findModelByRef<T extends Pick<ModelConfig, "id" | "alias">>(models: readonly T[], ref: string | undefined): T | undefined {
    if (!ref) return undefined;
    return models.find(m => m.id === ref) ?? models.find(m => m.alias === ref);
}

export interface DecisionSettingsView {
    question?: EvaluatorQuestion;
    scoring?: EvaluatorScoring;
    reads?: EvaluatorRead[];
    minConfidence?: number;
    choiceScores?: Record<string, number>;
    escalateTo?: string;
}

export function decisionSettingsFromEvaluator(ev: EvalEvaluator, escalateToName?: string): DecisionSettingsView {
    return {
        question: ev.question,
        scoring: ev.scoring,
        reads: ev.reads,
        minConfidence: ev.minConfidence,
        choiceScores: ev.choiceScores,
        escalateTo: ev.escalateToEvaluatorId ? (escalateToName ?? ev.escalateToEvaluatorId) : undefined,
    };
}

export function decisionSettingsFromSnapshot(cfg: EvaluatorDecisionConfigSnapshot, choiceScores?: Record<string, number>): DecisionSettingsView {
    return {
        question: cfg.question,
        scoring: cfg.scoring,
        reads: cfg.reads,
        minConfidence: cfg.min_confidence,
        choiceScores,
        escalateTo: cfg.escalate_to_evaluator_id ? (cfg.escalate_to?.name ?? cfg.escalate_to_evaluator_id) : undefined,
    };
}

export function validationDetailsOf(err: unknown): Record<string, string[]> | undefined {
    if (!err || typeof err !== "object") return undefined;
    const details = (err as { validationDetails?: unknown }).validationDetails;
    return details && typeof details === "object" ? (details as Record<string, string[]>) : undefined;
}
