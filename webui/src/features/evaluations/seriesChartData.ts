// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface SeriesChartInputPoint {
    readonly groupId: string;
    readonly startedTime: string | number | Date;
    readonly seriesKey: string;
    readonly score: number | null;
}

export interface SeriesChartRow {
    readonly label: string;
    readonly tooltipLabel: string;
    readonly [seriesKey: string]: number | null | string;
}

interface BuildSeriesChartDataResult {
    readonly data: SeriesChartRow[];
    readonly seriesKeys: readonly string[];
}

const MINUTE_MS = 60_000;

export function buildSeriesChartData(points: readonly SeriesChartInputPoint[], seriesKeys: readonly string[]): BuildSeriesChartDataResult {
    if (points.length === 0 || seriesKeys.length === 0) {
        return { data: [], seriesKeys };
    }

    const { bucketStartsMs, parsedPoints } = parsePoints(points, new Set(seriesKeys));

    const orderedMinuteKeys = Array.from(bucketStartsMs.entries())
        .sort((a, b) => a[1] - b[1])
        .map(([key]) => key);

    const rowByMinute = new Map<number, Record<string, number | null | string>>();
    let prevDay: string | null = null;
    for (const minuteKey of orderedMinuteKeys) {
        const ms = bucketStartsMs.get(minuteKey)!;
        const day = formatDayLabel(ms);
        const row: Record<string, number | null | string> = Object.create(null);
        row.label = day === prevDay ? formatTimeOnlyLabel(ms) : day;
        row.tooltipLabel = formatDayTimeLabel(ms);
        for (const key of seriesKeys) row[key] = null;
        rowByMinute.set(minuteKey, row);
        prevDay = day;
    }

    for (const p of parsedPoints) {
        const row = rowByMinute.get(p.minuteKey);
        if (!row) continue;
        row[p.seriesKey] = p.score;
    }

    return {
        data: orderedMinuteKeys.map(key => rowByMinute.get(key)! as SeriesChartRow),
        seriesKeys,
    };
}

interface ParsedPoint {
    readonly minuteKey: number;
    readonly seriesKey: string;
    readonly score: number | null;
}

function parsePoints(points: readonly SeriesChartInputPoint[], seriesKeySet: ReadonlySet<string>): { bucketStartsMs: Map<number, number>; parsedPoints: ParsedPoint[] } {
    const bucketStartsMs = new Map<number, number>();
    const parsedPoints: ParsedPoint[] = [];
    for (const p of points) {
        const ms = new Date(p.startedTime).getTime();
        if (Number.isNaN(ms)) continue;
        const minuteKey = Math.floor(ms / MINUTE_MS);
        const existing = bucketStartsMs.get(minuteKey);
        if (existing === undefined || ms < existing) {
            bucketStartsMs.set(minuteKey, ms);
        }
        if (seriesKeySet.has(p.seriesKey)) {
            parsedPoints.push({ minuteKey, seriesKey: p.seriesKey, score: p.score });
        }
    }
    return { bucketStartsMs, parsedPoints };
}

function formatDayLabel(ms: number): string {
    return new Date(ms).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

function formatDayTimeLabel(ms: number): string {
    return new Date(ms).toLocaleString(undefined, {
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
    });
}

function formatTimeOnlyLabel(ms: number): string {
    return new Date(ms).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}
