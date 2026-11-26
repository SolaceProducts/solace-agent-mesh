// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { buildSeriesChartData, type SeriesChartInputPoint } from "./seriesChartData";

const day1 = "2026-03-05T12:00:00Z";
const day1Later = "2026-03-05T18:00:00Z";
const day2 = "2026-03-12T12:00:00Z";
const day3 = "2026-03-19T12:00:00Z";

describe("buildSeriesChartData", () => {
    test("returns empty data when no points are provided", () => {
        const result = buildSeriesChartData([], ["A"]);
        expect(result.data).toEqual([]);
        expect(result.seriesKeys).toEqual(["A"]);
    });

    test("returns empty data when no series keys are provided", () => {
        const result = buildSeriesChartData([{ groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 }], []);
        expect(result.data).toEqual([]);
    });

    test("emits one row per group with the score for that series", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g2", startedTime: day2, seriesKey: "A", score: 0.7 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data).toHaveLength(2);
        expect(data[0].A).toBe(0.5);
        expect(data[1].A).toBe(0.7);
    });

    test("groups overlapping series onto the same row", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g1", startedTime: day1, seriesKey: "B", score: 0.6 },
        ];
        const { data } = buildSeriesChartData(points, ["A", "B"]);

        expect(data).toHaveLength(1);
        expect(data[0].A).toBe(0.5);
        expect(data[0].B).toBe(0.6);
    });

    test("emits null for a series that did not participate in a group (bridges via connectNulls)", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g2", startedTime: day2, seriesKey: "B", score: 0.7 },
        ];
        const { data } = buildSeriesChartData(points, ["A", "B"]);

        expect(data).toHaveLength(2);
        expect(data[0]).toMatchObject({ A: 0.5, B: null });
        expect(data[1]).toMatchObject({ A: null, B: 0.7 });
    });

    test("preserves the score=null sentinel from the input (failed-run case)", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: null },
            { groupId: "g2", startedTime: day2, seriesKey: "A", score: 0.7 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data[0].A).toBeNull();
        expect(data[1].A).toBe(0.7);
    });

    test("orders rows by each group's earliest startedTime, regardless of input order", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g3", startedTime: day3, seriesKey: "A", score: 0.9 },
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g2", startedTime: day2, seriesKey: "A", score: 0.7 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data.map(r => r.A)).toEqual([0.5, 0.7, 0.9]);
    });

    test("two points with different groupIds at the same minute share a single row (visual stacking)", () => {
        const sameMinute = "2026-03-05T12:00:00Z";
        const points: SeriesChartInputPoint[] = [
            { groupId: "rg-experiment-A", startedTime: sameMinute, seriesKey: "A", score: 0.5 },
            { groupId: "rg-experiment-B", startedTime: sameMinute, seriesKey: "B", score: 0.6 },
        ];
        const { data } = buildSeriesChartData(points, ["A", "B"]);

        expect(data).toHaveLength(1);
        expect(data[0]).toMatchObject({ A: 0.5, B: 0.6 });
    });

    test("two points whose timestamps fall in the same minute (different seconds) share a row", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: "2026-03-05T12:00:14Z", seriesKey: "A", score: 0.5 },
            { groupId: "g2", startedTime: "2026-03-05T12:00:47Z", seriesKey: "B", score: 0.6 },
        ];
        const { data } = buildSeriesChartData(points, ["A", "B"]);

        expect(data).toHaveLength(1);
        expect(data[0]).toMatchObject({ A: 0.5, B: 0.6 });
    });

    test("two points one minute apart get separate rows (minute-precision bucketing)", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: "2026-03-05T12:00:00Z", seriesKey: "A", score: 0.5 },
            { groupId: "g1", startedTime: "2026-03-05T12:01:00Z", seriesKey: "B", score: 0.6 },
        ];
        const { data } = buildSeriesChartData(points, ["A", "B"]);

        expect(data).toHaveLength(2);
        expect(data[0]).toMatchObject({ A: 0.5, B: null });
        expect(data[1]).toMatchObject({ A: null, B: 0.6 });
    });

    test("includes a column of all nulls for a series with no points (legend-only case)", () => {
        const points: SeriesChartInputPoint[] = [{ groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 }];
        const { data, seriesKeys } = buildSeriesChartData(points, ["A", "B"]);

        expect(seriesKeys).toEqual(["A", "B"]);
        expect(data[0]).toMatchObject({ A: 0.5, B: null });
    });

    test("ignores points whose seriesKey is not in seriesKeys", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g1", startedTime: day1, seriesKey: "REJECTED", score: 0.99 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data[0].A).toBe(0.5);
        expect(Object.keys(data[0])).not.toContain("REJECTED");
    });

    test("skips points with invalid startedTime values", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g-bad", startedTime: "not-a-date", seriesKey: "A", score: 0.9 },
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data).toHaveLength(1);
        expect(data[0].A).toBe(0.5);
    });

    test("last write wins when the same (minute, series) pair shows up twice", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: "2026-03-05T12:00:14Z", seriesKey: "A", score: 0.4 },
            { groupId: "g1", startedTime: "2026-03-05T12:00:47Z", seriesKey: "A", score: 0.6 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data).toHaveLength(1);
        expect(data[0].A).toBe(0.6);
    });

    test("first row of a day shows the date; subsequent same-day rows show only the time", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g1b", startedTime: day1Later, seriesKey: "A", score: 0.6 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data).toHaveLength(2);
        expect(data[0].label).not.toContain(":");
        expect(data[1].label).toContain(":");
        expect(data[0].label).not.toBe(data[1].label);
    });

    test("each new day gets its own date label (no collision across days)", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g2", startedTime: day2, seriesKey: "A", score: 0.7 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data[0].label).not.toContain(":");
        expect(data[1].label).not.toContain(":");
        expect(data[0].label).not.toBe(data[1].label);
    });

    test("date-suppression resets when the day changes between rows", () => {
        const day2Later = "2026-03-12T18:00:00Z";
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g1b", startedTime: day1Later, seriesKey: "A", score: 0.6 },
            { groupId: "g2", startedTime: day2, seriesKey: "A", score: 0.7 },
            { groupId: "g2b", startedTime: day2Later, seriesKey: "A", score: 0.8 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data).toHaveLength(4);
        expect(data[0].label).not.toContain(":");
        expect(data[1].label).toContain(":");
        expect(data[2].label).not.toContain(":");
        expect(data[3].label).toContain(":");
    });

    test("emits a precise tooltipLabel (date+time) on every row regardless of axis label format", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.5 },
            { groupId: "g1b", startedTime: day1Later, seriesKey: "A", score: 0.6 },
            { groupId: "g2", startedTime: day2, seriesKey: "A", score: 0.7 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        for (const row of data) {
            expect(row.tooltipLabel).toContain(":");
            expect(row.tooltipLabel.length).toBeGreaterThanOrEqual(row.label.length);
        }
    });

    test("accepts epoch ms timestamps in addition to ISO strings", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: new Date(day1).getTime(), seriesKey: "A", score: 0.5 },
            { groupId: "g2", startedTime: new Date(day2).getTime(), seriesKey: "A", score: 0.7 },
        ];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data.map(r => r.A)).toEqual([0.5, 0.7]);
    });

    test("accepts Date objects in addition to ISO strings", () => {
        const points: SeriesChartInputPoint[] = [{ groupId: "g1", startedTime: new Date(day1), seriesKey: "A", score: 0.5 }];
        const { data } = buildSeriesChartData(points, ["A"]);

        expect(data[0].A).toBe(0.5);
    });

    test("returned seriesKeys reflect the input order (drives Line render order and color assignment)", () => {
        const points: SeriesChartInputPoint[] = [
            { groupId: "g1", startedTime: day1, seriesKey: "B", score: 0.5 },
            { groupId: "g1", startedTime: day1, seriesKey: "A", score: 0.6 },
        ];
        const { seriesKeys } = buildSeriesChartData(points, ["B", "A"]);

        expect(seriesKeys).toEqual(["B", "A"]);
    });
});
