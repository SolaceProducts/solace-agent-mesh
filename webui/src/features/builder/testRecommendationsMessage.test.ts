// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { formatRecommendationsAsBuildRequest } from "./testRecommendationsMessage";
import type { TestReport } from "./TestReportCard";

const baseReport: TestReport = {
    componentName: "MyAgent",
    componentKind: "agent",
    sampleInput: "hello world",
    expectedOutput: "Hello, World!",
    actualOutput: "hi world",
    recommendations: [],
};

describe("formatRecommendationsAsBuildRequest", () => {
    test("empty recommendations produces a 'no changes' message", () => {
        const out = formatRecommendationsAsBuildRequest(baseReport);
        expect(out).toContain("no changes recommended");
        expect(out).toContain("MyAgent");
        expect(out).not.toContain("### High priority");
    });

    test("recommendations are grouped by priority high → medium → low", () => {
        const report: TestReport = {
            ...baseReport,
            recommendations: [
                { area: "tools", finding: "tool A wrong", suggestion: "use tool B", priority: "low" },
                { area: "system_prompt", finding: "prompt unclear", suggestion: "clarify role", priority: "high" },
                { area: "model_choice", finding: "model too small", suggestion: "use bigger", priority: "medium" },
            ],
        };
        const out = formatRecommendationsAsBuildRequest(report);
        const high = out.indexOf("### High priority");
        const medium = out.indexOf("### Medium priority");
        const low = out.indexOf("### Low priority");
        expect(high).toBeGreaterThan(-1);
        expect(medium).toBeGreaterThan(high);
        expect(low).toBeGreaterThan(medium);
        expect(out).toContain("prompt unclear");
        expect(out).toContain("Suggested change: clarify role");
    });

    test("includes the test inputs/outputs as code blocks for context", () => {
        const report: TestReport = {
            ...baseReport,
            recommendations: [{ area: "system_prompt", finding: "f", suggestion: "s", priority: "high" }],
            sampleInput: { greeting: "hello" },
            actualOutput: { greeting: "hi" },
        };
        const out = formatRecommendationsAsBuildRequest(report);
        expect(out).toContain("**Sample input:**");
        expect(out).toContain('"greeting": "hello"');
        expect(out).toContain("**Actual output:**");
        expect(out).toContain('"greeting": "hi"');
    });

    test("priority sections are skipped when empty", () => {
        const report: TestReport = {
            ...baseReport,
            recommendations: [{ area: "tools", finding: "f", suggestion: "s", priority: "low" }],
        };
        const out = formatRecommendationsAsBuildRequest(report);
        expect(out).not.toContain("### High priority");
        expect(out).not.toContain("### Medium priority");
        expect(out).toContain("### Low priority");
    });

    test("instructs the build agent to call sam-build-apply__mark_changes_applied", () => {
        const report: TestReport = {
            ...baseReport,
            recommendations: [{ area: "system_prompt", finding: "f", suggestion: "s", priority: "high" }],
        };
        const out = formatRecommendationsAsBuildRequest(report);
        expect(out).toContain("sam-build-apply__mark_changes_applied");
    });
});
