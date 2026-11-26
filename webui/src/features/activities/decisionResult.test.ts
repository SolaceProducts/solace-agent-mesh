// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { decisionAnswerSummary, decisionEscalationLabel, decisionHeadlineAnswer, decisionResultOf, decisionRoute } from "./decisionResult";

describe("decisionResultOf", () => {
    test("reads every answer shape and the routing fields", () => {
        const result = decisionResultOf({
            answers: {
                category: { type: "choice", choice: "billing", confidence: 0.92, probabilities: { billing: 0.92 } },
                urgent: { type: "boolean", value: true, probability: 0.81 },
                severity: { type: "score", score: 2, legend: { "0": "low", "1": "medium", "2": "high" }, confidence: 0.7 },
                broken: "not an answer",
            },
            escalated: false,
            escalation_reason: "",
            model: "jev-1",
            duration_ms: 41,
            selected_branch: "billing_agent",
            routed_branch: "billing_agent",
        })!;

        expect(result.answers.map(a => a.key)).toEqual(["category", "urgent", "severity"]);
        expect(result.answers[2].level).toBe("high");
        expect(result.escalationReason).toBeUndefined();
        expect(result.model).toBe("jev-1");
        expect(result.durationMs).toBe(41);
        expect(decisionRoute(result)).toBe("billing_agent");
        expect(decisionHeadlineAnswer(result)?.key).toBe("category");
    });

    test("is null for metadata that is not a decision's", () => {
        expect(decisionResultOf(undefined)).toBeNull();
        expect(decisionResultOf({ selected_branch: "a", selected_case_index: 0 })).toBeNull();
        expect(decisionResultOf({ answers: {}, escalated: "no" })).toBeNull();
    });

    test("an escalation routes to its selected target until a route is chosen", () => {
        const result = decisionResultOf({ answers: {}, escalated: true, escalation_reason: "provider_error", provider_error: "timeout", selected_branch: "human_review" })!;

        expect(decisionRoute(result)).toBe("human_review");
        expect(result.providerError).toBe("timeout");
        expect(decisionEscalationLabel(result)).toBe("escalated · provider error");
        expect(decisionEscalationLabel({ ...result, escalationReason: "low_confidence" })).toBe("escalated · low confidence");
        expect(decisionEscalationLabel({ ...result, escalationReason: undefined })).toBe("escalated");
        expect(decisionRoute({ ...result, routedBranch: "billing_agent" })).toBe("billing_agent");
    });
});

describe("decisionAnswerSummary", () => {
    test("formats each answer type", () => {
        expect(decisionAnswerSummary({ key: "c", type: "choice", choice: "billing", confidence: 0.923 })).toBe("billing · 92% confidence");
        expect(decisionAnswerSummary({ key: "c", type: "choice", choice: "billing" })).toBe("billing");
        expect(decisionAnswerSummary({ key: "u", type: "boolean", value: false, probability: 0.12 })).toBe("no · 88% likely");
        expect(decisionAnswerSummary({ key: "u", type: "boolean", value: true })).toBe("yes");
        expect(decisionAnswerSummary({ key: "s", type: "score", score: 2, level: "high", confidence: 0.7 })).toBe("2 (high) · 70% confidence");
        expect(decisionAnswerSummary({ key: "s", type: "score", score: 1 })).toBe("1");
    });
});

describe("decisionHeadlineAnswer", () => {
    test("shows no headline when several choice questions make route_on ambiguous", () => {
        const result = decisionResultOf({
            answers: { a: { type: "choice", choice: "x" }, b: { type: "choice", choice: "y" } },
            escalated: false,
        })!;
        expect(decisionHeadlineAnswer(result)).toBeUndefined();
    });
});
