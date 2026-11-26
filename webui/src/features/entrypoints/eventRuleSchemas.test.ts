// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { createEventRuleSchema, eventRuleErrorLabels, eventRuleSubmitErrorMessage } from "./eventRuleSchemas";

function baseRule(overrides: Record<string, unknown> = {}) {
    return {
        name: "rule1",
        subscriptions: [{ topic: "acme/orders/v1/created" }],
        messageFormat: "json",
        targetAgent: "",
        promptTemplate: "",
        targetWorkflowName: undefined,
        successOutput: { enabled: false, topic: "" },
        errorOutput: { enabled: false, topic: "" },
        ...overrides,
    };
}

function issuePaths(rule: Record<string, unknown>, targetType: "agent" | "workflow") {
    const result = createEventRuleSchema([], false, targetType).safeParse(rule);
    if (result.success) return [];
    return result.error.issues.map(issue => issue.path.join("."));
}

describe("createEventRuleSchema promptTemplate requirement", () => {
    test("workflow target does not require promptTemplate (runtime never consumes it)", () => {
        const rule = baseRule({ targetWorkflowName: "AbnormalDetectionWorkflow" });
        expect(issuePaths(rule, "workflow")).toEqual([]);
    });

    test("workflow target with promptTemplate still passes", () => {
        const rule = baseRule({ targetWorkflowName: "AbnormalDetectionWorkflow", promptTemplate: "Process this event {payload}" });
        expect(issuePaths(rule, "workflow")).toEqual([]);
    });

    test("agent target without promptTemplate is rejected", () => {
        const rule = baseRule({ targetAgent: "OrderAgent" });
        expect(issuePaths(rule, "agent")).toContain("promptTemplate");
    });

    test("agent target with promptTemplate passes", () => {
        const rule = baseRule({ targetAgent: "OrderAgent", promptTemplate: "Process this event {payload}" });
        expect(issuePaths(rule, "agent")).toEqual([]);
    });
});

describe("createEventRuleSchema target selection", () => {
    test("workflow target without a workflow name is rejected", () => {
        const rule = baseRule({ promptTemplate: "Process this event {payload}" });
        expect(issuePaths(rule, "workflow")).toContain("targetWorkflowName");
    });

    test("agent target without an agent is rejected", () => {
        const rule = baseRule({ promptTemplate: "Process this event {payload}" });
        expect(issuePaths(rule, "agent")).toContain("targetAgent");
    });

    test("workflow target does not require an agent", () => {
        const rule = baseRule({ targetAgent: "", targetWorkflowName: "AbnormalDetectionWorkflow", promptTemplate: "Process this event {payload}" });
        expect(issuePaths(rule, "workflow")).not.toContain("targetAgent");
    });

    test("agent target does not require a workflow name", () => {
        const rule = baseRule({ targetAgent: "OrderAgent", targetWorkflowName: "", promptTemplate: "Process this event {payload}" });
        expect(issuePaths(rule, "agent")).not.toContain("targetWorkflowName");
    });

    test("whitespace-only promptTemplate does not satisfy the agent requirement", () => {
        const rule = baseRule({ targetAgent: "OrderAgent", promptTemplate: "   " });
        expect(issuePaths(rule, "agent")).toContain("promptTemplate");
    });
});

describe("eventRuleErrorLabels", () => {
    test("maps known error fields to human-facing labels", () => {
        expect(eventRuleErrorLabels(["promptTemplate", "targetWorkflowName"])).toEqual(["Additional Instructions", "Workflow Name"]);
    });

    test("falls back to the raw field name for unknown fields", () => {
        expect(eventRuleErrorLabels(["somethingNew"])).toEqual(["somethingNew"]);
    });

    test("deduplicates labels that map from multiple fields", () => {
        expect(eventRuleErrorLabels(["successOutput", "successOutput"])).toEqual(["Success Response"]);
    });

    test("returns an empty array for no errors", () => {
        expect(eventRuleErrorLabels([])).toEqual([]);
    });
});

describe("eventRuleSubmitErrorMessage", () => {
    test("names the offending fields (silent save failure)", () => {
        expect(eventRuleSubmitErrorMessage(["promptTemplate"])).toBe("Cannot save. Please correct: Additional Instructions.");
    });

    test("joins multiple fields", () => {
        expect(eventRuleSubmitErrorMessage(["name", "subscriptions"])).toBe("Cannot save. Please correct: Name, Topics.");
    });

    test("falls back to a generic message when no fields are named", () => {
        expect(eventRuleSubmitErrorMessage([])).toBe("Cannot save. Please correct the highlighted fields.");
    });
});
