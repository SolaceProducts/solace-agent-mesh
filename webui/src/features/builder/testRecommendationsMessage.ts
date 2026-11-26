// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Recommendation, TestReport } from "./TestReportCard";

export function formatRecommendationsAsBuildRequest(report: TestReport): string {
    if (!report.recommendations.length) {
        return [`I just tested the ${report.componentKind} **${report.componentName}** and the actual output matched the expected behaviour — no changes recommended.`, "", "Let me know if you'd like to adjust anything anyway."].join("\n");
    }

    const lines: string[] = [];
    lines.push(`Apply these changes to the ${report.componentKind} **${report.componentName}** based on the test I just ran.`);
    lines.push("");

    const grouped = groupByPriority(report.recommendations);
    for (const priority of ["high", "medium", "low"] as const) {
        const items = grouped[priority];
        if (!items.length) continue;
        lines.push(`### ${priorityLabel(priority)} (${items.length})`);
        for (const r of items) {
            lines.push(`- **${areaLabel(r.area)} — ${r.finding}**`, `  - Suggested change: ${r.suggestion}`);
        }
        lines.push("");
    }

    lines.push("---");
    lines.push(
        "Once every component you edited has been redeployed and is back in " +
            "a working state, call **`sam-build-apply__mark_changes_applied`** with " +
            "a one-paragraph `summary` and a per-component `changesApplied` list. " +
            "That tool is what tells the frontend the apply has finished and " +
            "drives the post-edit summary card + Switch-to-Test CTA — without " +
            "it the FE has to fall back to weaker heuristics. Call the tool " +
            "exactly once, at the end."
    );
    lines.push("");
    lines.push("---");
    lines.push("Test inputs / outputs for context:");
    lines.push("");
    lines.push("**Sample input:**");
    lines.push("```");
    lines.push(stringify(report.sampleInput));
    lines.push("```");
    lines.push("**Expected output:**");
    lines.push("```");
    lines.push(stringify(report.expectedOutput));
    lines.push("```");
    lines.push("**Actual output:**");
    lines.push("```");
    lines.push(stringify(report.actualOutput));
    lines.push("```");

    return lines.join("\n");
}

function groupByPriority(recs: Recommendation[]): Record<Recommendation["priority"], Recommendation[]> {
    const out: Record<Recommendation["priority"], Recommendation[]> = { high: [], medium: [], low: [] };
    for (const r of recs) out[r.priority].push(r);
    return out;
}

function priorityLabel(p: Recommendation["priority"]): string {
    switch (p) {
        case "high":
            return "High priority";
        case "medium":
            return "Medium priority";
        case "low":
            return "Low priority";
    }
}

function areaLabel(a: Recommendation["area"]): string {
    switch (a) {
        case "system_prompt":
            return "System prompt";
        case "tools":
            return "Tools";
        case "workflow_logic":
            return "Workflow logic";
        case "model_choice":
            return "Model choice";
        case "other":
            return "Other";
    }
}

function stringify(v: unknown): string {
    if (typeof v === "string") return v;
    try {
        return JSON.stringify(v, null, 2);
    } catch {
        return String(v);
    }
}
