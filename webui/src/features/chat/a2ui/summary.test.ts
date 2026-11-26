// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { A2UISurface } from "@/types";

import { buildAnsweredResponseText, buildAnswerEntries, buildApprovalEntries } from "./summary";

function approvalSurface(): A2UISurface {
    return {
        surfaceId: "req-1",
        catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
        components: [
            { id: "info", component: "Text", text: { literalString: 'Agent "Mailer" wants to run tool: send_email' } },
            { id: "arg-subject-label", component: "Text", text: { literalString: "subject" } },
            { id: "arg-subject-value", component: "Text", text: { literalString: "Hello" } },
            { id: "arg-to-label", component: "Text", text: { literalString: "to" } },
            { id: "arg-to-value", component: "Text", text: { literalString: "user@example.com" } },
            { id: "approve-btn", component: "Button" },
        ],
        dataModel: {},
    };
}

function questionSurface(): A2UISurface {
    return {
        surfaceId: "req-2",
        catalogId: "https://a2ui.org/specification/v0_9/basic_catalog.json",
        components: [{ id: "q0-label", component: "Text", text: { literalString: "Which environment?" } }],
        dataModel: {},
    };
}

describe("buildApprovalEntries", () => {
    it("leads with the info line carrying the decision, then one entry per arg", () => {
        const entries = buildApprovalEntries(approvalSurface(), "Approved");
        expect(entries).toEqual([
            { question: 'Agent "Mailer" wants to run tool: send_email', answer: "Approved" },
            { question: "subject", answer: "Hello" },
            { question: "to", answer: "user@example.com" },
        ]);
    });

    it("carries a Denied decision in the first entry", () => {
        expect(buildApprovalEntries(approvalSurface(), "Denied")[0]).toEqual({ question: 'Agent "Mailer" wants to run tool: send_email', answer: "Denied" });
    });

    it("falls back to a generic label when the info line is missing", () => {
        const surface: A2UISurface = { surfaceId: "s", catalogId: "c", components: [], dataModel: {} };
        expect(buildApprovalEntries(surface, "Approved")).toEqual([{ question: "Request", answer: "Approved" }]);
    });

    it("skips args whose value is empty", () => {
        const surface = approvalSurface();
        surface.components.push({ id: "arg-cc-label", component: "Text", text: { literalString: "cc" } }, { id: "arg-cc-value", component: "Text", text: { literalString: "" } });
        const args = buildApprovalEntries(surface, "Approved").map(e => e.question);
        expect(args).not.toContain("cc");
    });

    it("carries the contentType hint from the arg value component onto the entry", () => {
        const surface = approvalSurface();
        surface.components.push({ id: "arg-body-label", component: "Text", text: { literalString: "body" } }, { id: "arg-body-value", component: "Text", text: { literalString: "<h1>Hi</h1>" }, contentType: "html" });
        const bodyEntry = buildApprovalEntries(surface, "Approved").find(e => e.question === "body");
        expect(bodyEntry).toEqual({ question: "body", answer: "<h1>Hi</h1>", contentType: "html" });
    });
});

describe("buildAnswerEntries", () => {
    it("maps each answer to its question label", () => {
        const entries = buildAnswerEntries(questionSurface(), { answers: { q0: "prod" } });
        expect(entries).toEqual([{ question: "Which environment?", answer: "prod" }]);
    });

    it("returns nothing when there are no answers", () => {
        expect(buildAnswerEntries(questionSurface(), {})).toEqual([]);
    });

    it("resolves __other__ to the free-text value", () => {
        const entries = buildAnswerEntries(questionSurface(), { answers: { q0: "__other__" }, other_text: { q0: "staging-eu" } });
        expect(entries).toEqual([{ question: "Which environment?", answer: "staging-eu" }]);
    });

    it("joins multi-select arrays and expands __other__ within them", () => {
        const entries = buildAnswerEntries(questionSurface(), { answers: { q0: ["a", "__other__"] }, other_text: { q0: "z" } });
        expect(entries).toEqual([{ question: "Which environment?", answer: "a, z" }]);
    });
});

describe("buildAnsweredResponseText", () => {
    it("encodes an approval as JSON entries with the Approved decision", () => {
        const json = buildAnsweredResponseText(approvalSurface(), "tool_approval", { approved: true });
        expect(JSON.parse(json)[0]).toEqual({ question: 'Agent "Mailer" wants to run tool: send_email', answer: "Approved" });
    });

    it("encodes a denial when approved is false", () => {
        const json = buildAnsweredResponseText(approvalSurface(), "tool_approval", { approved: false });
        expect(JSON.parse(json)[0].answer).toBe("Denied");
    });

    it("encodes question answers as JSON entries", () => {
        const json = buildAnsweredResponseText(questionSurface(), "ask_user_question", { answers: { q0: "prod" } });
        expect(JSON.parse(json)).toEqual([{ question: "Which environment?", answer: "prod" }]);
    });

    it("returns an empty string when a question has no answers to summarize", () => {
        expect(buildAnsweredResponseText(questionSurface(), "ask_user_question", {})).toBe("");
    });
});
