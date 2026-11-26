// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { projectAgentsForBuilder } from "./projectAgentsForBuilder";

describe("projectAgentsForBuilder", () => {
    test("projects tools to skills", () => {
        const result = projectAgentsForBuilder([
            {
                name: "ReportAgent",
                displayName: "Report Agent",
                description: "Generates reports",
                tools: [{ name: "Generate daily report", description: "Produces a structured daily summary" }, { name: "Email report summary" }],
            },
        ]);
        expect(result).toEqual([
            {
                name: "ReportAgent",
                displayName: "Report Agent",
                description: "Generates reports",
                skills: [
                    { name: "Generate daily report", description: "Produces a structured daily summary" },
                    { name: "Email report summary", description: undefined },
                ],
            },
        ]);
    });

    test("drops nameless tool entries", () => {
        const result = projectAgentsForBuilder([
            {
                name: "AgentX",
                tools: [{ name: "Good skill" }, { name: "" }, { name: undefined } as { name?: string }],
            },
        ]);
        expect(result[0].skills).toEqual([{ name: "Good skill", description: undefined }]);
    });

    test("emits skills=undefined (not []) when every tool is nameless", () => {
        const result = projectAgentsForBuilder([
            {
                name: "AgentX",
                tools: [{ name: "" }, { name: undefined } as { name?: string }],
            },
        ]);
        expect(result[0].skills).toBeUndefined();
    });

    test("omits skills field for workflow targets", () => {
        const result = projectAgentsForBuilder([
            {
                name: "MyWorkflow",
                isWorkflow: true,
                tools: [{ name: "should be ignored" }],
            },
        ]);
        expect(result[0].skills).toBeUndefined();
    });

    test("omits skills field when tools array is empty or missing", () => {
        const result = projectAgentsForBuilder([{ name: "A", tools: [] }, { name: "B" }]);
        expect(result[0].skills).toBeUndefined();
        expect(result[1].skills).toBeUndefined();
    });

    test("preserves displayName and description but never carries tools / isWorkflow into the output", () => {
        const result = projectAgentsForBuilder([
            {
                name: "X",
                displayName: "X Agent",
                description: "desc",
                tools: [{ name: "t1" }],
                isWorkflow: false,
            },
        ]);
        expect(result[0]).not.toHaveProperty("tools");
        expect(result[0]).not.toHaveProperty("isWorkflow");
        expect(result[0].displayName).toBe("X Agent");
        expect(result[0].description).toBe("desc");
    });
});
