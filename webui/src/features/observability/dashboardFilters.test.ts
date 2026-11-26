// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import {
    ALL_SUBJECTS,
    DASHBOARD_DATE_PRESETS,
    type DateRange,
    DEFAULT_DASHBOARD_PRESET,
    formatRangeLabel,
    granularityFor,
    isSameRange,
    isSubjectKey,
    parseRange,
    presetRange,
    resolveWindow,
    serializeRange,
    SHARED_FILTER_PARAM,
    sharedFilterSearch,
    subjectFilterFor,
    subjectKeyOf,
    subjectKindOf,
} from "./dashboardFilters";

process.env.TZ = "America/Los_Angeles";

const NOW = new Date("2026-09-30T12:00:00.000Z");

describe("resolveWindow", () => {
    it("defaults to the last 90 days", () => {
        expect(DEFAULT_DASHBOARD_PRESET).toBe("90d");
        expect(resolveWindow(presetRange("90d"), NOW)).toEqual({ startDate: "2026-07-02T12:00:00.000Z", endDate: "2026-09-30T12:00:00.000Z" });
    });

    it("resolves the shorter presets against the same end", () => {
        expect(resolveWindow(presetRange("24h"), NOW).startDate).toBe("2026-09-29T12:00:00.000Z");
        expect(resolveWindow(presetRange("7d"), NOW).startDate).toBe("2026-09-23T12:00:00.000Z");
        expect(resolveWindow(presetRange("30d"), NOW).startDate).toBe("2026-08-31T12:00:00.000Z");
    });

    it("does not mutate the clock it is handed", () => {
        const clock = new Date("2026-09-30T12:00:00.000Z");

        resolveWindow(presetRange("90d"), clock);

        expect(clock.toISOString()).toBe("2026-09-30T12:00:00.000Z");
    });

    it("offers exactly the four presets, with no unbounded option", () => {
        expect(DASHBOARD_DATE_PRESETS.map(p => p.value)).toEqual(["24h", "7d", "30d", "90d"]);
    });

    it("spans a custom range from the start of its first local day to the end of its last", () => {
        const window = resolveWindow({ kind: "custom", from: "2026-08-19", to: "2026-09-17" });

        const start = new Date(window.startDate);
        const end = new Date(window.endDate);

        expect([start.getFullYear(), start.getMonth(), start.getDate(), start.getHours(), start.getMinutes(), start.getSeconds(), start.getMilliseconds()]).toEqual([2026, 7, 19, 0, 0, 0, 0]);
        expect([end.getFullYear(), end.getMonth(), end.getDate(), end.getHours(), end.getMinutes(), end.getSeconds(), end.getMilliseconds()]).toEqual([2026, 8, 17, 23, 59, 59, 999]);
    });

    it("resolves a single-day range to that whole local day", () => {
        const window = resolveWindow({ kind: "custom", from: "2026-08-19", to: "2026-08-19" });

        expect(Date.parse(window.endDate) - Date.parse(window.startDate)).toBe(24 * 60 * 60 * 1000 - 1);
    });

    it("ignores the clock for a custom range, which names its own bounds", () => {
        const range = { kind: "custom", from: "2026-08-19", to: "2026-09-17" } as const;

        expect(resolveWindow(range, NOW)).toEqual(resolveWindow(range, new Date("2027-01-01T00:00:00.000Z")));
    });
});

describe("granularityFor", () => {
    it.each([
        ["24h", "hour"],
        ["7d", "day"],
        ["30d", "day"],
        ["90d", "week"],
    ] as const)("maps the %s preset to %s buckets", (preset, expected) => {
        expect(granularityFor(resolveWindow(presetRange(preset), NOW))).toBe(expected);
    });

    it("covers every preset the picker offers", () => {
        DASHBOARD_DATE_PRESETS.forEach(preset => {
            expect(granularityFor(resolveWindow(presetRange(preset.value), NOW))).toBeDefined();
        });
    });

    it.each([
        ["2026-08-19", "2026-08-19", "hour"],
        ["2026-08-19", "2026-08-20", "hour"],
        ["2026-08-19", "2026-08-21", "day"],
        ["2026-08-19", "2026-09-18", "day"],
        ["2026-08-19", "2026-09-19", "week"],
        ["2026-01-01", "2026-12-31", "week"],
    ] as const)("buckets %s..%s by %s", (from, to, expected) => {
        expect(granularityFor(resolveWindow({ kind: "custom", from, to }))).toBe(expected);
    });

    it("never asks for hour buckets beyond the server's seven-day limit", () => {
        const hourly = [
            ["2026-08-19", "2026-08-19"],
            ["2026-08-19", "2026-08-20"],
        ] as const;

        hourly.forEach(([from, to]) => {
            const window = resolveWindow({ kind: "custom", from, to });
            expect(granularityFor(window)).toBe("hour");
            expect(Date.parse(window.endDate) - Date.parse(window.startDate)).toBeLessThan(7 * 24 * 60 * 60 * 1000);
        });
    });

    it("counts a span in calendar days rather than elapsed time", () => {
        expect(granularityFor(resolveWindow({ kind: "custom", from: "2026-02-20", to: "2026-03-23" }))).toBe("week");
    });
});

describe("subject keys", () => {
    it("round-trips a subject through its key", () => {
        expect(subjectKeyOf({ id: "a1", type: "agent" })).toBe("agent:a1");
        expect(subjectFilterFor("agent:a1")).toEqual({ agentIds: ["a1"] });
        expect(subjectFilterFor("workflow:w1")).toEqual({ workflowIds: ["w1"] });
    });

    it("filters nothing for the all-subjects key", () => {
        expect(subjectFilterFor(ALL_SUBJECTS)).toEqual({});
    });

    it("keeps a dashed uuid intact", () => {
        const id = "3f2b8c1e-0000-4000-8000-000000000001";
        expect(subjectFilterFor(subjectKeyOf({ id, type: "agent" }))).toEqual({ agentIds: [id] });
    });

    it("filters nothing for a key with no separator, rather than guessing a kind", () => {
        expect(subjectFilterFor("garbage")).toEqual({});
    });

    it("filters nothing for an unrecognised kind, rather than falling through to agentIds", () => {
        expect(subjectFilterFor("bogus:xyz")).toEqual({});
    });
});

describe("subjectKindOf", () => {
    it("reads the kind a subject key carries", () => {
        expect(subjectKindOf("agent:a-1")).toBe("agent");
        expect(subjectKindOf("workflow:w-1")).toBe("workflow");
    });

    it("has no kind for the all-subjects sentinel", () => {
        expect(subjectKindOf(ALL_SUBJECTS)).toBeUndefined();
    });

    it("keeps an id that contains a colon", () => {
        expect(subjectKindOf("agent:a:1")).toBe("agent");
        expect(subjectFilterFor("agent:a:1").agentIds).toEqual(["a:1"]);
    });

    it("has no kind for an unrecognised prefix", () => {
        expect(subjectKindOf("tenant:t-1")).toBeUndefined();
    });
});

describe("isSubjectKey", () => {
    it("accepts the all-subjects sentinel", () => {
        expect(isSubjectKey(ALL_SUBJECTS)).toBe(true);
    });

    it("accepts a recognized kind with a non-empty id", () => {
        expect(isSubjectKey("agent:a-1")).toBe(true);
        expect(isSubjectKey("workflow:w-1")).toBe(true);
    });

    it("rejects an unrecognized kind", () => {
        expect(isSubjectKey("bogus:xyz")).toBe(false);
    });

    it("rejects an empty id", () => {
        expect(isSubjectKey("agent:")).toBe(false);
    });

    it("rejects a value with no separator", () => {
        expect(isSubjectKey("garbage")).toBe(false);
    });
});

describe("SHARED_FILTER_PARAM", () => {
    it("names the three query params both Feedback pages agree on", () => {
        expect(SHARED_FILTER_PARAM).toEqual({ subject: "subject", name: "name", range: "range" });
    });
});

describe("sharedFilterSearch", () => {
    it("keeps only the shared filter params", () => {
        expect(sharedFilterSearch("?subject=agent:a-1&name=Agent+One&range=7d&page=3&selected=f-1&rating=down")).toBe("?subject=agent%3Aa-1&name=Agent+One&range=7d");
    });

    it("returns an empty string when none of the shared params are present", () => {
        expect(sharedFilterSearch("?page=3&selected=f-1")).toBe("");
    });

    it("returns an empty string for an empty search", () => {
        expect(sharedFilterSearch("")).toBe("");
    });
});

describe("parseRange", () => {
    it("reads each preset the picker offers", () => {
        DASHBOARD_DATE_PRESETS.forEach(preset => {
            expect(parseRange(preset.value)).toEqual({ kind: "preset", preset: preset.value });
        });
    });

    it("reads an explicit from..to pair", () => {
        expect(parseRange("2026-08-19..2026-09-17")).toEqual({ kind: "custom", from: "2026-08-19", to: "2026-09-17" });
    });

    it("reads a single-day range where both bounds are the same day", () => {
        expect(parseRange("2026-08-19..2026-08-19")).toEqual({ kind: "custom", from: "2026-08-19", to: "2026-08-19" });
    });

    it("falls back to the default for a missing param", () => {
        expect(parseRange(null)).toEqual({ kind: "preset", preset: DEFAULT_DASHBOARD_PRESET });
    });

    it.each([["garbage"], ["1y"], ["2026-08-19"], ["2026-08-19.."], ["..2026-09-17"], ["2026-08-19..2026-09-17..2026-10-01"], ["2026-13-01..2026-13-05"], ["2026-02-30..2026-03-05"], ["2026-08-19..2026-09-31"], ["20260819..20260917"]])(
        "falls back to the default for %s",
        raw => {
            expect(parseRange(raw)).toEqual({ kind: "preset", preset: DEFAULT_DASHBOARD_PRESET });
        }
    );

    it("falls back to the default when the bounds are reversed", () => {
        expect(parseRange("2026-09-17..2026-08-19")).toEqual({ kind: "preset", preset: DEFAULT_DASHBOARD_PRESET });
    });
});

describe("serializeRange", () => {
    it("round-trips a preset", () => {
        expect(serializeRange(presetRange("7d"))).toBe("7d");
        expect(parseRange(serializeRange(presetRange("7d")))).toEqual(presetRange("7d"));
    });

    it("round-trips an explicit range", () => {
        const range: DateRange = { kind: "custom", from: "2026-08-19", to: "2026-09-17" };

        expect(serializeRange(range)).toBe("2026-08-19..2026-09-17");
        expect(parseRange(serializeRange(range))).toEqual(range);
    });
});

describe("isSameRange", () => {
    it("is true for equal presets and equal explicit ranges", () => {
        expect(isSameRange(presetRange("7d"), presetRange("7d"))).toBe(true);
        expect(isSameRange(parseRange("2026-08-19..2026-09-17"), parseRange("2026-08-19..2026-09-17"))).toBe(true);
    });

    it("is false across kinds and across differing bounds", () => {
        expect(isSameRange(presetRange("7d"), presetRange("30d"))).toBe(false);
        expect(isSameRange(presetRange("7d"), parseRange("2026-08-19..2026-09-17"))).toBe(false);
        expect(isSameRange(parseRange("2026-08-19..2026-09-17"), parseRange("2026-08-19..2026-09-18"))).toBe(false);
    });
});

describe("formatRangeLabel", () => {
    it("names a preset by its own label", () => {
        expect(formatRangeLabel(presetRange("30d"), DASHBOARD_DATE_PRESETS)).toBe("Last 30 days");
    });

    it("names an explicit range by its dates", () => {
        expect(formatRangeLabel({ kind: "custom", from: "2026-08-19", to: "2026-09-17" }, DASHBOARD_DATE_PRESETS)).toBe("Aug 19, 2026 – Sep 17, 2026");
    });

    it("falls back to the preset's own value when the list does not carry it", () => {
        expect(formatRangeLabel(presetRange("90d"), DASHBOARD_DATE_PRESETS.slice(0, 2))).toBe("90d");
    });
});
