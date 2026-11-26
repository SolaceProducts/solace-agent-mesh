// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { buildAskAiPrefill } from "./askAiPrefill";
import { DASHBOARD_DATE_PRESETS, formatDay, presetRange } from "./dashboardFilters";

const build = (over: Partial<Parameters<typeof buildAskAiPrefill>[0]> = {}) => buildAskAiPrefill({ range: presetRange("30d"), presets: DASHBOARD_DATE_PRESETS, ...over });

const customRange = { kind: "custom", from: "2026-08-19", to: "2026-09-17" } as const;
const customWindow = `between ${formatDay(customRange.from)} and ${formatDay(customRange.to)}`;

describe("buildAskAiPrefill", () => {
    it("phrases a preset as the relative window it names", () => {
        expect(build()).toBe("Tell me about feedback given in the last 30 days");
    });

    it("keeps each preset's own unit rather than forcing a day count", () => {
        expect(build({ range: presetRange("24h") })).toBe("Tell me about feedback given in the last 24 hours");
    });

    it("names the subject when one is filtered", () => {
        expect(build({ subjectLabel: "Procurement Intake Router" })).toBe("Tell me about feedback given to Procurement Intake Router in the last 30 days");
    });

    it("phrases an explicit range as its dates, never as the word Custom", () => {
        const prefill = build({ range: customRange });

        expect(prefill).toBe(`Tell me about feedback given ${customWindow}`);
        expect(prefill).not.toMatch(/custom/i);
        expect(prefill).not.toContain("–");
    });

    it("names both the subject and an explicit range", () => {
        expect(build({ range: customRange, subjectLabel: "Budget Approval Workflow" })).toBe(`Tell me about feedback given to Budget Approval Workflow ${customWindow}`);
    });
});
