// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { CrossUserFeedback } from "@/api/feedback";

import { DATE_PRESETS, DEFAULT_DATE_PRESET, PRESET_DAYS, startDateFor, subjectOptionsFrom } from "./feedbackFilters";
import { longestAvailablePreset, presetsWithinRetention } from "./retentionLimit";

const row = (over: Partial<CrossUserFeedback>): CrossUserFeedback => ({
    id: "f1",
    sessionId: "s",
    taskId: "t",
    userId: "u",
    rating: "up",
    comment: null,
    shareTaskContent: false,
    subjectId: "a-1",
    subjectType: "agent",
    subjectName: "Agent One",
    entrypointType: "webui",
    createdAt: "2026-08-25T09:42:00.000Z",
    ...over,
});

describe("startDateFor", () => {
    it("returns an ISO timestamp the given number of days back", () => {
        expect(startDateFor("30d", new Date("2026-09-24T00:00:00Z"))).toBe("2026-08-25T00:00:00.000Z");
    });

    it("resolves the 24-hour preset shared with the Dashboard's own catalog", () => {
        expect(startDateFor("24h", new Date("2026-09-24T12:00:00Z"))).toBe("2026-09-23T12:00:00.000Z");
    });
});

describe("the shared date-preset catalog", () => {
    it("offers the same four presets the Dashboard does, defaulting to 90 days", () => {
        expect(DATE_PRESETS.map(p => p.value)).toEqual(["24h", "7d", "30d", "90d"]);
        expect(DEFAULT_DATE_PRESET).toBe("90d");
    });

    it("still clamps correctly under a tight retention window now that there are four presets", () => {
        const capped = presetsWithinRetention(DATE_PRESETS, PRESET_DAYS, 10);
        expect(capped.map(p => p.value)).toEqual(["24h", "7d"]);
        expect(longestAvailablePreset("90d", capped)).toBe("7d");
    });
});

describe("subjectOptionsFrom", () => {
    it("derives one option per distinct subject, labelled by name", () => {
        const options = subjectOptionsFrom([row({}), row({ id: "f2" }), row({ id: "f3", subjectId: "w-1", subjectType: "workflow", subjectName: "Nightly Sync" })]);

        expect(options).toEqual([
            { id: "a-1", kind: "agent", label: "Agent One" },
            { id: "w-1", kind: "workflow", label: "Nightly Sync" },
        ]);
    });

    it("labels an unresolved subject by its id so the option is still selectable", () => {
        expect(subjectOptionsFrom([row({ subjectName: "" })])).toEqual([{ id: "a-1", kind: "agent", label: "a-1" }]);
    });

    it("skips unknown-subject rows", () => {
        expect(subjectOptionsFrom([row({ subjectType: "unknown", subjectId: "unknown", subjectName: "" })])).toEqual([]);
    });
});
