// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { parseRange, presetRange } from "./dashboardFilters";
import { longestAvailablePreset, presetsWithinRetention, rangeWithinRetention, retentionFloorDate, todayCalendarDay } from "./retentionLimit";

const PRESETS = [
    { value: "24h", label: "Last 24 hours" },
    { value: "7d", label: "Last 7 days" },
    { value: "30d", label: "Last 30 days" },
    { value: "90d", label: "Last 90 days" },
] as const;

const DAYS = { "24h": 1, "7d": 7, "30d": 30, "90d": 90 };

describe("presetsWithinRetention", () => {
    it("drops the ranges that reach past the window", () => {
        expect(presetsWithinRetention(PRESETS, DAYS, 30).map(p => p.value)).toEqual(["24h", "7d", "30d"]);
    });

    it("keeps a range that exactly meets the window", () => {
        expect(presetsWithinRetention(PRESETS, DAYS, 30).map(p => p.value)).toContain("30d");
    });

    it("keeps every range when there is no window", () => {
        expect(presetsWithinRetention(PRESETS, DAYS, undefined)).toHaveLength(PRESETS.length);
    });

    it("keeps a preset the day map does not describe", () => {
        const withAll = [...PRESETS, { value: "all", label: "All time" }] as const;

        expect(presetsWithinRetention(withAll, DAYS, 7).map(p => p.value)).toEqual(["24h", "7d", "all"]);
    });

    it("still offers the shortest range when the window is shorter than every preset", () => {
        expect(presetsWithinRetention(PRESETS, DAYS, 0).map(p => p.value)).toEqual(["24h"]);
    });
});

describe("longestAvailablePreset", () => {
    it("keeps the current selection when it still fits", () => {
        expect(longestAvailablePreset("7d", presetsWithinRetention(PRESETS, DAYS, 30))).toBe("7d");
    });

    it("falls back to the longest range that fits", () => {
        expect(longestAvailablePreset("90d", presetsWithinRetention(PRESETS, DAYS, 30))).toBe("30d");
    });

    it("returns the current selection when nothing is available", () => {
        expect(longestAvailablePreset("90d", [])).toBe("90d");
    });
});

const NOW = new Date(2026, 9, 13, 12);

const TODAY = todayCalendarDay(NOW);

const narrow = (range: Parameters<typeof rangeWithinRetention>[0]["range"], floorDate: string | undefined, ceilingDate: string = TODAY) => rangeWithinRetention({ range, presets: PRESETS, floorDate, ceilingDate, defaultPreset: "90d" });

describe("retentionFloorDate", () => {
    it("is the day retention reaches back to", () => {
        expect(retentionFloorDate(90, NOW)).toBe("2026-07-15");
    });

    it("has no floor when retention is unconfigured", () => {
        expect(retentionFloorDate(undefined, NOW)).toBeUndefined();
    });
});

describe("todayCalendarDay", () => {
    it("is the day the clock is on, so a range cannot reach into the future", () => {
        expect(todayCalendarDay(NOW)).toBe("2026-10-13");
    });
});

describe("rangeWithinRetention", () => {
    it("keeps a range sitting wholly inside the window", () => {
        const range = parseRange("2026-09-01..2026-09-30");

        expect(narrow(range, "2026-07-15")).toEqual(range);
    });

    it("keeps a range starting exactly on the floor", () => {
        const range = parseRange("2026-07-15..2026-09-30");
        expect(narrow(range, "2026-07-15")).toEqual(range);
    });

    it("keeps a single-day range sitting exactly on the floor", () => {
        const range = parseRange("2026-07-15..2026-07-15");
        expect(narrow(range, "2026-07-15")).toEqual(range);
    });

    it("clamps a range that starts before the floor but still overlaps it", () => {
        expect(narrow(parseRange("2026-01-01..2026-09-30"), "2026-07-15")).toEqual({ kind: "custom", from: "2026-07-15", to: "2026-09-30" });
    });

    it("discards a range lying entirely before the floor", () => {
        expect(narrow(parseRange("2026-01-01..2026-02-01"), "2026-07-15")).toEqual(presetRange("90d"));
    });

    it("keeps a range at the 365-day boundary", () => {
        expect(narrow(parseRange("2025-10-14..2026-10-13"), undefined)).toEqual(parseRange("2025-10-14..2026-10-13"));
    });

    it("keeps a range at the 366-day boundary", () => {
        expect(narrow(parseRange("2025-10-13..2026-10-13"), undefined)).toEqual(parseRange("2025-10-13..2026-10-13"));
    });

    it("discards a range exceeding the 366-day ceiling", () => {
        expect(narrow(parseRange("2025-10-12..2026-10-13"), undefined)).toEqual(presetRange("90d"));
    });

    it("keeps an old range when retention is unconfigured and the span fits", () => {
        const range = parseRange("2025-06-01..2025-07-01");
        expect(narrow(range, undefined)).toEqual(range);
    });

    it("discards a range lying entirely after today", () => {
        expect(narrow(parseRange("2030-01-01..2030-02-01"), "2026-07-15")).toEqual(presetRange("90d"));
    });

    it("discards a future range even when retention is unconfigured", () => {
        expect(narrow(parseRange("2030-01-01..2030-02-01"), undefined)).toEqual(presetRange("90d"));
    });

    it("clamps a range that reaches past today but starts on or before it", () => {
        expect(narrow(parseRange("2026-10-01..2027-03-01"), "2026-07-15")).toEqual({ kind: "custom", from: "2026-10-01", to: TODAY });
    });

    it("clamps a range starting exactly today to that single day", () => {
        expect(narrow(parseRange("2026-10-13..2026-10-20"), "2026-07-15")).toEqual({ kind: "custom", from: TODAY, to: TODAY });
    });

    it("keeps a range ending exactly today", () => {
        const range = parseRange("2026-10-01..2026-10-13");
        expect(narrow(range, "2026-07-15")).toEqual(range);
    });

    it("clamps at both ends when a range straddles the floor and today", () => {
        expect(narrow(parseRange("2026-01-01..2026-12-31"), "2026-07-15")).toEqual({ kind: "custom", from: "2026-07-15", to: TODAY });
    });

    it("narrows a preset the same way longestAvailablePreset does", () => {
        expect(rangeWithinRetention({ range: presetRange("90d"), presets: presetsWithinRetention(PRESETS, DAYS, 30), floorDate: "2026-09-13", ceilingDate: TODAY, defaultPreset: "90d" })).toEqual(presetRange("30d"));
    });

    it("keeps a preset that still fits", () => {
        expect(rangeWithinRetention({ range: presetRange("7d"), presets: presetsWithinRetention(PRESETS, DAYS, 90), floorDate: "2026-07-15", ceilingDate: TODAY, defaultPreset: "90d" })).toEqual(presetRange("7d"));
    });
});
