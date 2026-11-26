// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { EvalEvaluator } from "@/types/eval";

import {
    buildCreateEvaluatorRequest,
    buildUpdateEvaluatorRequest,
    decisionFormFromEvaluator,
    type DecisionFormValue,
    decisionSettingsFromEvaluator,
    decisionSettingsFromSnapshot,
    emptyDecisionFormValue,
    emptyEvaluatorFormValue,
    evaluatorFormFromEvaluator,
    type EvaluatorFormValue,
    findModelByRef,
    FIX_FIELDS_MESSAGE,
    isEvaluatorFormComplete,
    mapValidationDetails,
    newDecisionLevel,
    newDecisionOption,
    preSaveErrorState,
    saveErrorState,
    validateDecisionForm,
    validateEvaluatorForm,
    validationDetailsOf,
} from "./evaluatorForm";

function decisionValue(d: Partial<DecisionFormValue> = {}): EvaluatorFormValue {
    return {
        ...emptyEvaluatorFormValue(),
        type: "decision",
        name: " Grounded ",
        description: " Checks grounding ",
        decision: { ...emptyDecisionFormValue(), model: "decision-1", instructions: " Is it grounded? ", ...d },
    };
}

const baseEvaluator: EvalEvaluator = {
    id: "ev-1",
    name: "Grounded",
    description: "Checks grounding",
    type: "decision",
    isDefault: false,
    experimentCount: 0,
    createdBy: "u",
    updatedBy: "u",
    createdAt: "",
    updatedAt: "",
};

describe("validateDecisionForm", () => {
    test("accepts the defaults once a model and a question are set", () => {
        expect(validateDecisionForm(decisionValue().decision)).toEqual({});
    });

    test("requires a decision model and a question", () => {
        const errors = validateDecisionForm({ ...emptyDecisionFormValue() });
        expect(errors.decisionModel).toBeDefined();
        expect(errors.instructions).toBeDefined();
    });

    test("checks a choice question's options: count, names, duplicates and scores", () => {
        expect(validateDecisionForm({ ...decisionValue().decision, options: [newDecisionOption("a", 1)] }).options).toMatch(/at least 2 options/);
        expect(validateDecisionForm({ ...decisionValue().decision, options: [newDecisionOption("a", 1), newDecisionOption(" ", 0)] }).options).toMatch(/needs a name/);
        expect(validateDecisionForm({ ...decisionValue().decision, options: [newDecisionOption("a", 1), newDecisionOption("a ", 0)] }).options).toMatch(/"a" is listed more than once/);
        expect(validateDecisionForm({ ...decisionValue().decision, options: [newDecisionOption("a", 1.5), newDecisionOption("b", 0)] }).options).toMatch(/0% to 100%/);
        const tooMany = Array.from({ length: 27 }, (_, i) => newDecisionOption(`o${i}`, 0));
        expect(validateDecisionForm({ ...decisionValue().decision, options: tooMany }).options).toMatch(/at most 26/);
    });

    test("checks a score question's levels", () => {
        const levels = Array.from({ length: 11 }, (_, i) => newDecisionLevel(`l${i}`));
        expect(validateDecisionForm({ ...decisionValue().decision, questionType: "score", levels }).levels).toMatch(/at most 10 levels/);
        expect(validateDecisionForm({ ...decisionValue().decision, questionType: "score", levels: [newDecisionLevel("low")] }).levels).toMatch(/at least 2 levels/);
        expect(validateDecisionForm({ ...decisionValue().decision, questionType: "score" }).levels).toBeUndefined();
    });

    test("a boolean question describes both answers or neither", () => {
        const d = { ...decisionValue().decision, questionType: "boolean" as const };
        expect(validateDecisionForm(d).booleanCriteria).toBeUndefined();
        expect(validateDecisionForm({ ...d, trueDescription: "grounded" }).booleanCriteria).toMatch(/both/);
        expect(validateDecisionForm({ ...d, trueDescription: "grounded", falseDescription: "made up" }).booleanCriteria).toBeUndefined();
    });

    test("must read the response", () => {
        expect(validateDecisionForm({ ...decisionValue().decision, reads: ["prompt"] }).reads).toBeDefined();
    });

    test("a confidence threshold needs an escalation target and a value in (0, 100]", () => {
        const d = decisionValue().decision;
        expect(validateDecisionForm({ ...d, minConfidencePercent: "80" }).minConfidence).toMatch(/escalate to/);
        expect(validateDecisionForm({ ...d, minConfidencePercent: "0", escalateToEvaluatorId: "judge" }).minConfidence).toMatch(/above 0%/);
        expect(validateDecisionForm({ ...d, minConfidencePercent: "abc", escalateToEvaluatorId: "judge" }).minConfidence).toBeDefined();
        expect(validateDecisionForm({ ...d, minConfidencePercent: "80", escalateToEvaluatorId: "judge" }).minConfidence).toBeUndefined();
    });
});

describe("validateEvaluatorForm and isEvaluatorFormComplete", () => {
    test("an LLM judge keeps its original messages, in order", () => {
        const errors = validateEvaluatorForm(emptyEvaluatorFormValue());
        expect(Object.values(errors)).toEqual(["Name is required", "Description is required", "Model is required", "Criteria is required"]);
        expect(isEvaluatorFormComplete(emptyEvaluatorFormValue())).toBe(false);
        expect(isEvaluatorFormComplete({ ...emptyEvaluatorFormValue(), name: "n", description: "d", model: "m", promptTemplate: "{{Response}}" })).toBe(true);
    });

    test("a decision evaluator needs a decision model and a question, not a prompt", () => {
        expect(isEvaluatorFormComplete(decisionValue())).toBe(true);
        expect(isEvaluatorFormComplete(decisionValue({ model: "" }))).toBe(false);
        expect(validateEvaluatorForm(decisionValue())).toEqual({});
    });

    test("preSaveErrorState shows an LLM judge's first problem, and a decision evaluator's beside each field", () => {
        expect(preSaveErrorState(emptyEvaluatorFormValue())).toEqual({ fieldErrors: {}, message: "Name is required" });
        const refused = preSaveErrorState(decisionValue({ model: "" }));
        expect(refused?.message).toBe(FIX_FIELDS_MESSAGE);
        expect(refused?.fieldErrors.decisionModel).toBeDefined();
        expect(preSaveErrorState(decisionValue())).toBeNull();
    });
});

describe("building requests", () => {
    test("an LLM judge's create and update bodies are unchanged", () => {
        const value: EvaluatorFormValue = { ...emptyEvaluatorFormValue(), name: " Judge ", description: " d ", model: " m ", promptTemplate: " {{Response}} " };
        expect(buildCreateEvaluatorRequest(value)).toEqual({ name: "Judge", description: "d", type: "llm_judge", model: "m", promptTemplate: "{{Response}}", choiceScores: { Fail: 0, Partial: 0.5, Pass: 1 } });
        expect(buildUpdateEvaluatorRequest(value)).toEqual({ name: "Judge", description: "d", model: "m", promptTemplate: "{{Response}}", choiceScores: { Fail: 0, Partial: 0.5, Pass: 1 } });
    });

    test("a choice question sends its options as criteria and their scores as choiceScores", () => {
        const options = [{ ...newDecisionOption(" grounded ", 1), description: " cites the source " }, newDecisionOption("made_up", 0)];
        const req = buildCreateEvaluatorRequest(decisionValue({ options, scoring: "expected", reads: ["prompt", "response", "transcript"] }));
        expect(req).toEqual({
            name: "Grounded",
            description: "Checks grounding",
            type: "decision",
            model: "decision-1",
            question: { type: "choice", instructions: "Is it grounded?", criteria: [{ key: "grounded", description: "cites the source" }, { key: "made_up" }] },
            scoring: "expected",
            reads: ["prompt", "response", "transcript"],
            choiceScores: { grounded: 1, made_up: 0 },
        });
        expect(req).not.toHaveProperty("promptTemplate");
    });

    test("a boolean question sends no choiceScores, and criteria only when both answers are described", () => {
        const plain = buildCreateEvaluatorRequest(decisionValue({ questionType: "boolean" }));
        expect(plain.question).toEqual({ type: "boolean", instructions: "Is it grounded?" });
        expect(plain).not.toHaveProperty("choiceScores");
        const described = buildCreateEvaluatorRequest(decisionValue({ questionType: "boolean", trueDescription: "yes", falseDescription: "no" }));
        expect(described.question?.criteria).toEqual([
            { key: "true", description: "yes" },
            { key: "false", description: "no" },
        ]);
    });

    test("a score question sends its levels lowest first", () => {
        const req = buildCreateEvaluatorRequest(decisionValue({ questionType: "score", levels: [newDecisionLevel("low"), newDecisionLevel("high")] }));
        expect(req.question).toEqual({ type: "score", instructions: "Is it grounded?", criteria: [{ key: "low" }, { key: "high" }] });
    });

    test("escalation is sent only with a target, and the threshold only with it", () => {
        expect(buildCreateEvaluatorRequest(decisionValue({ minConfidencePercent: "80" }))).not.toHaveProperty("minConfidence");
        const req = buildCreateEvaluatorRequest(decisionValue({ escalateToEvaluatorId: "judge-1", minConfidencePercent: "80" }));
        expect(req.escalateToEvaluatorId).toBe("judge-1");
        expect(req.minConfidence).toBeCloseTo(0.8);
        expect(buildCreateEvaluatorRequest(decisionValue({ escalateToEvaluatorId: "judge-1" }))).not.toHaveProperty("minConfidence");
    });

    test("a decision update sends null for every optional setting the form leaves unset", () => {
        const req = buildUpdateEvaluatorRequest(decisionValue({ questionType: "score" }));
        expect(req.choiceScores).toBeNull();
        expect(req.escalateToEvaluatorId).toBeNull();
        expect(req.minConfidence).toBeNull();
        expect(req).not.toHaveProperty("promptTemplate");
        expect(JSON.parse(JSON.stringify(req))).toMatchObject({ choiceScores: null, escalateToEvaluatorId: null, minConfidence: null });

        const escalating = buildUpdateEvaluatorRequest(decisionValue({ escalateToEvaluatorId: "judge-1", minConfidencePercent: "75" }));
        expect(escalating.choiceScores).toEqual({ fail: 0, pass: 1 });
        expect(escalating.escalateToEvaluatorId).toBe("judge-1");
        expect(escalating.minConfidence).toBeCloseTo(0.75);
    });
});

describe("loading an evaluator into the form", () => {
    test("a choice evaluator's options carry their scores", () => {
        const ev: EvalEvaluator = {
            ...baseEvaluator,
            model: "jev",
            question: { type: "choice", instructions: "Q", criteria: [{ key: "a", description: "first" }, { key: "b" }] },
            choiceScores: { a: 1, b: 0.25 },
            scoring: "expected",
            reads: ["response"],
            minConfidence: 0.8,
            escalateToEvaluatorId: "judge-1",
        };
        const value = evaluatorFormFromEvaluator(ev);
        expect(value.type).toBe("decision");
        expect(value.decision).toMatchObject({ model: "jev", questionType: "choice", instructions: "Q", scoring: "expected", reads: ["response"], escalateToEvaluatorId: "judge-1", minConfidencePercent: "80" });
        expect(value.decision.options.map(o => [o.key, o.description, o.score])).toEqual([
            ["a", "first", 1],
            ["b", "", 0.25],
        ]);
        expect(buildUpdateEvaluatorRequest(value).question).toEqual({ type: "choice", instructions: "Q", criteria: [{ key: "a", description: "first" }, { key: "b" }] });
    });

    test("a fractional threshold or option score round-trips exactly", () => {
        const ev: EvalEvaluator = {
            ...baseEvaluator,
            model: "jev",
            question: { type: "choice", instructions: "Q", criteria: [{ key: "a" }, { key: "b" }] },
            choiceScores: { a: 0.875, b: 0.07 },
            minConfidence: 0.875,
            escalateToEvaluatorId: "judge-1",
        };
        const value = evaluatorFormFromEvaluator(ev);
        expect(value.decision.minConfidencePercent).toBe("87.5");
        const req = buildUpdateEvaluatorRequest({ ...value, name: "Renamed" });
        expect(req.minConfidence).toBe(0.875);
        expect(req.choiceScores).toEqual({ a: 0.875, b: 0.07 });
        expect(buildUpdateEvaluatorRequest(evaluatorFormFromEvaluator({ ...ev, minConfidence: 0.07 })).minConfidence).toBe(0.07);
    });

    test("score and boolean evaluators, and defaults for missing settings", () => {
        const score = decisionFormFromEvaluator({ question: { type: "score", instructions: "Q", criteria: [{ key: "low" }, { key: "high", description: "great" }] } });
        expect(score.levels.map(l => l.key)).toEqual(["low", "high"]);
        expect(score.scoring).toBe("chosen");
        expect(score.reads).toEqual(["prompt", "response", "expected_response"]);

        const bool = decisionFormFromEvaluator({
            question: {
                type: "boolean",
                instructions: "Q",
                criteria: [
                    { key: "true", description: "yes" },
                    { key: "false", description: "no" },
                ],
            },
        });
        expect([bool.trueDescription, bool.falseDescription]).toEqual(["yes", "no"]);
    });

    test("an LLM judge loads as before", () => {
        const value = evaluatorFormFromEvaluator({ ...baseEvaluator, type: "llm_judge", model: "m", promptTemplate: "p", choiceScores: { ok: 1, bad: 0 } });
        expect(value).toMatchObject({ type: "llm_judge", model: "m", promptTemplate: "p" });
        expect(value.choiceScores.map(c => c.choice)).toEqual(["ok", "bad"]);
    });
});

describe("platform validation errors", () => {
    test("map onto the form's fields, decision-specific where the type says so", () => {
        const { fieldErrors, unmapped } = mapValidationDetails(
            { model: ["needs a decision model"], choiceScores: ["missing b"], question: ["bad"], minConfidence: ["too low"], escalateToEvaluatorId: ["not a judge"], reads: ["x"], scoring: ["y"], name: ["taken"], other: ["?"] },
            "decision"
        );
        expect(fieldErrors).toMatchObject({ decisionModel: "needs a decision model", options: "missing b", question: "bad", minConfidence: "too low", escalateToEvaluatorId: "not a judge", reads: "x", scoring: "y", name: "taken" });
        expect(unmapped).toEqual(["other: ?"]);

        const judge = mapValidationDetails({ model: ["m"], prompt_template: ["p"], choice_scores: ["c"], choiceScores: ["d"] }, "llm_judge").fieldErrors;
        expect(judge).toEqual({ model: "m", promptTemplate: "p", choiceScores: "c d" });
    });

    test("shows a refused evaluatorType on the type field, as the v2 API names it", () => {
        expect(mapValidationDetails({ evaluatorType: ["cannot be changed"] }, "decision")).toEqual({ fieldErrors: { type: "cannot be changed" }, unmapped: [] });
    });

    test("saveErrorState shows a decision evaluator's beside their fields and anything else as before", () => {
        const err = Object.assign(new Error("Validation failed: question: bad"), { validationDetails: { question: ["bad"] } });
        expect(validationDetailsOf(err)).toEqual({ question: ["bad"] });
        expect(validationDetailsOf("nope")).toBeUndefined();
        expect(saveErrorState(err, "decision")).toEqual({ fieldErrors: { question: "bad" }, message: FIX_FIELDS_MESSAGE });
        expect(saveErrorState(Object.assign(new Error("x"), { validationDetails: { zzz: ["?"] } }), "decision").message).toBe("zzz: ?");
        expect(saveErrorState(err, "llm_judge")).toEqual({ fieldErrors: {}, message: "Validation failed: question: bad" });
    });
});

describe("helpers", () => {
    test("findModelByRef matches an id first, then an alias", () => {
        const models = [
            { id: "1", alias: "jev" },
            { id: "jev", alias: "other" },
        ];
        expect(findModelByRef(models, "jev")?.alias).toBe("other");
        expect(findModelByRef(models, "1")?.alias).toBe("jev");
        expect(findModelByRef(models, undefined)).toBeUndefined();
    });

    test("decision settings read from the live evaluator and from a run snapshot", () => {
        const ev: EvalEvaluator = { ...baseEvaluator, escalateToEvaluatorId: "judge-1", minConfidence: 0.7 };
        expect(decisionSettingsFromEvaluator(ev, "Strict Judge").escalateTo).toBe("Strict Judge");
        expect(decisionSettingsFromEvaluator(ev).escalateTo).toBe("judge-1");
        const snap = decisionSettingsFromSnapshot({
            question: { type: "boolean", instructions: "Q" },
            scoring: "chosen",
            reads: ["response"],
            min_confidence: 0.6,
            escalate_to_evaluator_id: "judge-1",
            escalate_to: { id: "judge-1", name: "Strict Judge" },
        });
        expect(snap).toMatchObject({ minConfidence: 0.6, escalateTo: "Strict Judge", reads: ["response"] });
    });
});
