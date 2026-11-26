// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FeedbackSummaryRow, FeedbackTrendPoint } from "@/api/feedback";

export type TrendMode = "ratio" | "count";

export const TREND_SERIES_COLORS = ["#6E89D6", "#4C9496", "#9A80B8", "#E2988A", "#C17BAD"] as const;

export interface TrendChartRow {
    date: string;
    [seriesKey: string]: string | number | undefined;
}

export interface TrendSeries {
    key: string;
    name: string;
    color: string;
}

function hashSubjectID(subjectID: string): number {
    let hash = 0;
    for (let i = 0; i < subjectID.length; i++) {
        hash = Math.trunc(hash * 31 + (subjectID.codePointAt(i) ?? 0));
    }
    return Math.abs(hash);
}

export function seriesColorFor(subjectId: string, taken: ReadonlySet<string>): string {
    const start = hashSubjectID(subjectId) % TREND_SERIES_COLORS.length;
    for (let offset = 0; offset < TREND_SERIES_COLORS.length; offset++) {
        const candidate = TREND_SERIES_COLORS[(start + offset) % TREND_SERIES_COLORS.length];
        if (!taken.has(candidate)) return candidate;
    }
    return TREND_SERIES_COLORS[start];
}

export function buildTrendSeries(rows: readonly FeedbackSummaryRow[]): TrendSeries[] {
    const subjects = rows.flatMap(row => (row.subject ? [row.subject] : []));

    const taken = new Set<string>();
    const colorById = new Map<string, string>();
    [...subjects]
        .sort((a, b) => a.id.localeCompare(b.id))
        .forEach(subject => {
            const color = seriesColorFor(subject.id, taken);
            taken.add(color);
            colorById.set(subject.id, color);
        });

    return subjects.map(subject => ({
        key: subject.id,
        name: subject.name || subject.id,
        color: colorById.get(subject.id)!,
    }));
}

export function trendPointValue(point: FeedbackTrendPoint, mode: TrendMode): number {
    return mode === "count" ? point.negativeFeedback : point.negativeRatio;
}

export function buildTrendRows(rows: readonly FeedbackSummaryRow[], mode: TrendMode): TrendChartRow[] {
    const byDate = new Map<string, TrendChartRow>();

    rows.forEach(row => {
        if (!row.subject) return;
        row.trend.forEach(point => {
            const existing = byDate.get(point.date) ?? { date: point.date };
            existing[row.subject!.id] = trendPointValue(point, mode);
            byDate.set(point.date, existing);
        });
    });

    return [...byDate.values()].sort((a, b) => a.date.localeCompare(b.date));
}
