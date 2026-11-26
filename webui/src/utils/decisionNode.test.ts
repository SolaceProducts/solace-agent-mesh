// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { decisionBranches, decisionBranchLabel, type DecisionQuestion, decisionQuestions, decisionQuestionTypeLabel, escalationModeOf, reroutesEscalation, routedQuestionFirst } from "./decisionNode";

describe("decisionQuestions", () => {
    it("reads the YAML map in declaration order", () => {
        const questions = decisionQuestions({
            questions: {
                department: { type: "choice", instructions: "Which team?", criteria: { technical: null, billing: "invoices" } },
                urgent: { type: "boolean", instructions: "Is it urgent?" },
            },
        });
        expect(questions).toEqual([
            { key: "department", type: "choice", instructions: "Which team?", options: ["technical", "billing"] },
            { key: "urgent", type: "boolean", instructions: "Is it urgent?", options: [] },
        ]);
    });

    it("reads the visualization payload's list", () => {
        const questions = decisionQuestions({ questions: [{ key: "severity", type: "score", instructions: "How bad?", criteria: ["low", "high"] }] });
        expect(questions).toEqual([{ key: "severity", type: "score", instructions: "How bad?", options: ["low", "high"] }]);
    });

    it("reads score levels and choice options written as a YAML list of one-entry maps", () => {
        const questions = decisionQuestions({
            questions: {
                severity: { type: "score", instructions: "How bad?", criteria: [{ low: "cosmetic" }, { medium: "degraded" }, { high: "outage" }] },
                department: { type: "choice", criteria: [{ billing: null }, { technical: "errors" }] },
            },
        });
        expect(questions.map(q => q.options)).toEqual([
            ["low", "medium", "high"],
            ["billing", "technical"],
        ]);
    });

    it("skips keyless list entries and copes with no questions", () => {
        expect(decisionQuestions({ questions: [{ type: "boolean" }] })).toEqual([]);
        expect(decisionQuestions({})).toEqual([]);
    });
});

describe("decisionQuestionTypeLabel", () => {
    it("title-cases each answer type", () => {
        expect(["boolean", "choice", "score"].map(t => decisionQuestionTypeLabel(t as "boolean" | "choice" | "score"))).toEqual(["Boolean", "Choice", "Score"]);
    });
});

describe("routedQuestionFirst", () => {
    const question = (key: string): DecisionQuestion => ({ key, type: "boolean", instructions: "", options: [] });
    const keys = (questions: DecisionQuestion[]) => questions.map(q => q.key);

    it("moves the routed-on question to the front, keeping the rest in order", () => {
        expect(keys(routedQuestionFirst([question("urgent"), question("category"), question("tone")], "category"))).toEqual(["category", "urgent", "tone"]);
    });

    it("keeps declaration order when route_on names none of the questions", () => {
        expect(keys(routedQuestionFirst([question("urgent"), question("tone")], undefined))).toEqual(["urgent", "tone"]);
        expect(keys(routedQuestionFirst([question("urgent")], "missing"))).toEqual(["urgent"]);
    });
});

describe("decisionBranches", () => {
    it("labels the escalation with its confidence threshold", () => {
        const label = (node: Parameters<typeof decisionBranches>[0]) => decisionBranches({ escalate: "human_review", ...node }).map(decisionBranchLabel);
        expect(label({})).toEqual(["escalate"]);
        expect(label({ min_confidence: 0.8 })).toEqual(["escalate < 80% confidence"]);
    });

    it("reroutes an escalation only when escalation_mode is unset or reroute", () => {
        expect(reroutesEscalation({ escalate: "human_review" })).toBe(true);
        expect(reroutesEscalation({ escalate: "human_review", escalation_mode: "reroute" })).toBe(true);
        expect(reroutesEscalation({ escalate: "human_review", escalation_mode: "handoff" })).toBe(false);
        expect(reroutesEscalation({ escalate: "human_review", escalation_mode: "Handoff" })).toBe(false);
        expect(reroutesEscalation({})).toBe(false);
    });

    it("reads escalation_mode under either key spelling, with an empty or null value as unset", () => {
        expect(reroutesEscalation({ escalate: "human_review", escalation_mode: "" })).toBe(true);
        expect(reroutesEscalation({ escalate: "human_review", escalation_mode: null })).toBe(true);
        expect(reroutesEscalation({ escalate: "human_review", escalationMode: "handoff" })).toBe(false);
        expect(escalationModeOf({ escalation_mode: "" })).toBeUndefined();
        expect(escalationModeOf({ escalationMode: "handoff" })).toBe("handoff");
        expect(escalationModeOf({ escalation_mode: "Handoff" })).toBe("Handoff");
        expect(escalationModeOf({ escalation_mode: true as unknown as string })).toBe("true");
        expect(escalationModeOf({ escalation_mode: "", escalationMode: "handoff" })).toBe("handoff");
    });

    it("lists routes, then default, then escalate, one edge per target that keeps every label", () => {
        const branches = decisionBranches({ routes: { billing: "billing_desk", technical: "tech_desk", sales: "billing_desk" }, default: "general_desk", escalate: "reclassify" });
        expect(branches).toEqual([
            { kind: "route", labels: ["billing", "sales"], target: "billing_desk" },
            { kind: "route", labels: ["technical"], target: "tech_desk" },
            { kind: "default", labels: ["default"], target: "general_desk" },
            { kind: "escalate", labels: ["escalate"], target: "reclassify" },
        ]);
        expect(branches.map(decisionBranchLabel)).toEqual(["billing / sales", "technical", "default", "escalate"]);
    });

    it("keeps the escalate tone when the escalate target is also a route or the default", () => {
        const branches = decisionBranches({ routes: { billing: "human_review" }, default: "human_review", escalate: "human_review" });
        expect(branches).toEqual([{ kind: "escalate", labels: ["billing", "default", "escalate"], target: "human_review" }]);
    });

    it("has no branches for a classify-only node", () => {
        expect(decisionBranches({})).toEqual([]);
    });
});
