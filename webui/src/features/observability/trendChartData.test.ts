// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { FeedbackSummaryRow } from "@/api/feedback";

import { buildTrendRows, buildTrendSeries, TREND_SERIES_COLORS } from "./trendChartData";

const point = (date: string, negativeFeedback: number, negativeRatio: number, totalActivities = 100) => ({
    date,
    totalActivities,
    positiveFeedback: 0,
    negativeFeedback,
    negativeRatio,
});

const row = (id: string, name: string, trend: ReturnType<typeof point>[]): FeedbackSummaryRow => ({
    subject: { id, name, type: "agent" },
    totalActivities: 100,
    positiveFeedback: 0,
    negativeFeedback: 0,
    negativeRatio: 0,
    trend,
});

describe("buildTrendRows", () => {
    it("pivots per-subject trends into one row per date", () => {
        const rows = [row("a1", "Alpha", [point("2026-09-01T00:00:00Z", 1, 0.01), point("2026-09-02T00:00:00Z", 2, 0.02)]), row("b2", "Beta", [point("2026-09-01T00:00:00Z", 3, 0.03), point("2026-09-02T00:00:00Z", 4, 0.04)])];

        expect(buildTrendRows(rows, "ratio")).toEqual([
            { date: "2026-09-01T00:00:00Z", a1: 0.01, b2: 0.03 },
            { date: "2026-09-02T00:00:00Z", a1: 0.02, b2: 0.04 },
        ]);
    });

    it("plots the count when the mode is count", () => {
        const rows = [row("a1", "Alpha", [point("2026-09-01T00:00:00Z", 7, 0.07)])];

        expect(buildTrendRows(rows, "count")).toEqual([{ date: "2026-09-01T00:00:00Z", a1: 7 }]);
    });

    it("aligns series that cover different dates, leaving the missing side absent", () => {
        const rows = [row("a1", "Alpha", [point("2026-09-01T00:00:00Z", 1, 0.01), point("2026-09-02T00:00:00Z", 1, 0.01)]), row("b2", "Beta", [point("2026-09-02T00:00:00Z", 2, 0.02)])];

        const out = buildTrendRows(rows, "ratio");

        expect(out.map(r => r.date)).toEqual(["2026-09-01T00:00:00Z", "2026-09-02T00:00:00Z"]);
        expect(out[0].b2).toBeUndefined();
        expect(out[1].b2).toBe(0.02);
    });

    it("orders dates chronologically even when the API rows are not", () => {
        const rows = [row("a1", "Alpha", [point("2026-09-03T00:00:00Z", 1, 0.03), point("2026-09-01T00:00:00Z", 1, 0.01)])];

        expect(buildTrendRows(rows, "ratio").map(r => r.date)).toEqual(["2026-09-01T00:00:00Z", "2026-09-03T00:00:00Z"]);
    });

    it("keeps hourly buckets of one day as separate rows", () => {
        const rows = [row("a1", "Alpha", [point("2026-09-01T14:00:00Z", 1, 0.1), point("2026-09-01T15:00:00Z", 2, 0.2)])];

        const out = buildTrendRows(rows, "ratio");

        expect(out).toHaveLength(2);
        expect(out[0].date).toBe("2026-09-01T14:00:00Z");
    });

    it("orders buckets chronologically when the response is not already sorted", () => {
        const rows = [row("a1", "Alpha", [point("2026-09-01T15:00:00Z", 2, 0.2), point("2026-09-01T14:00:00Z", 1, 0.1)])];

        expect(buildTrendRows(rows, "ratio").map(r => r.date)).toEqual(["2026-09-01T14:00:00Z", "2026-09-01T15:00:00Z"]);
    });

    it("returns no rows for no subjects", () => {
        expect(buildTrendRows([], "ratio")).toEqual([]);
    });

    it("skips a row with no subject, which the tenant shape produces", () => {
        const tenantRow = { totalActivities: 1, positiveFeedback: 0, negativeFeedback: 0, negativeRatio: 0, trend: [point("2026-09-01T00:00:00Z", 0, 0)] } as FeedbackSummaryRow;

        expect(buildTrendRows([tenantRow], "ratio")).toEqual([]);
    });
});

describe("buildTrendSeries", () => {
    it("names each series by its subject and keys it by id", () => {
        const series = buildTrendSeries([row("a1", "Alpha", []), row("b2", "Beta", [])]);

        expect(series.map(s => s.key)).toEqual(["a1", "b2"]);
        expect(series.map(s => s.name)).toEqual(["Alpha", "Beta"]);
    });

    it("falls back to the id when a subject has no resolved name", () => {
        const series = buildTrendSeries([row("a1", "", [])]);

        expect(series[0].name).toBe("a1");
    });

    it("gives a subject the same colour regardless of rank", () => {
        const ranked = buildTrendSeries([row("b2", "Beta", []), row("a1", "Alpha", [])]);
        const reranked = buildTrendSeries([row("a1", "Alpha", []), row("b2", "Beta", [])]);

        expect(reranked.find(s => s.key === "a1")!.color).toBe(ranked.find(s => s.key === "a1")!.color);
        expect(reranked.find(s => s.key === "b2")!.color).toBe(ranked.find(s => s.key === "b2")!.color);
    });

    it("can repaint a survivor when two subjects contend for one slot", () => {
        const together = buildTrendSeries([row("hr-bot", "HR Bot", []), row("it-helpdesk", "IT Helpdesk", [])]);
        const alone = buildTrendSeries([row("it-helpdesk", "IT Helpdesk", [])]);

        expect(together.find(s => s.key === "hr-bot")!.color).not.toBe(together.find(s => s.key === "it-helpdesk")!.color);
        expect(alone[0].color).not.toBe(together.find(s => s.key === "it-helpdesk")!.color);
    });

    it("never gives two visible series the same colour", () => {
        const ids = ["a1", "b2", "c3", "d4", "e5"];
        const series = buildTrendSeries(ids.map(id => row(id, id.toUpperCase(), [])));

        expect(new Set(series.map(s => s.color)).size).toBe(ids.length);
    });

    it("keeps each name with its own key when the ranking reorders", () => {
        const ascending = buildTrendSeries([row("a1", "Alpha", []), row("b2", "Beta", [])]);
        const descending = buildTrendSeries([row("b2", "Beta", []), row("a1", "Alpha", [])]);

        const nameFor = (series: ReturnType<typeof buildTrendSeries>, key: string) => series.find(s => s.key === key)!.name;
        expect(nameFor(ascending, "a1")).toBe("Alpha");
        expect(nameFor(descending, "a1")).toBe("Alpha");
        expect(nameFor(descending, "b2")).toBe("Beta");
    });

    it("draws every colour from the validated palette", () => {
        const series = buildTrendSeries([row("a1", "Alpha", []), row("b2", "Beta", [])]);

        series.forEach(s => expect(TREND_SERIES_COLORS).toContain(s.color));
    });

    it("skips a row with no subject, which the tenant shape produces", () => {
        const tenantRow = { totalActivities: 1, positiveFeedback: 0, negativeFeedback: 0, negativeRatio: 0, trend: [] } as FeedbackSummaryRow;

        expect(buildTrendSeries([tenantRow])).toEqual([]);
    });
});
