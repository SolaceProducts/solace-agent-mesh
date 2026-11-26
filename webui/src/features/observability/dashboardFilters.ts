// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TrendBucketGranularity } from "@/api/feedback";

export type DashboardDatePreset = "24h" | "7d" | "30d" | "90d";

export const DASHBOARD_DATE_PRESETS: ReadonlyArray<{ value: DashboardDatePreset; label: string }> = [
    { value: "24h", label: "Last 24 hours" },
    { value: "7d", label: "Last 7 days" },
    { value: "30d", label: "Last 30 days" },
    { value: "90d", label: "Last 90 days" },
];

export const DEFAULT_DASHBOARD_PRESET: DashboardDatePreset = "90d";

export interface DashboardWindow {
    startDate: string;
    endDate: string;
}

export const PRESET_DAYS: Record<DashboardDatePreset, number> = { "24h": 1, "7d": 7, "30d": 30, "90d": 90 };

export type DateRange<P extends string = DashboardDatePreset> = { kind: "preset"; preset: P } | { kind: "custom"; from: string; to: string };

const CUSTOM_RANGE_SEPARATOR = "..";
const CALENDAR_DAY = /^\d{4}-\d{2}-\d{2}$/;

export function presetRange(preset: DashboardDatePreset): DateRange {
    return { kind: "preset", preset };
}

function isCalendarDay(value: string): boolean {
    if (!CALENDAR_DAY.test(value)) return false;
    const at = new Date(`${value}T00:00:00.000Z`);
    return !Number.isNaN(at.getTime()) && at.toISOString().slice(0, 10) === value;
}

function isPreset(value: string): value is DashboardDatePreset {
    return DASHBOARD_DATE_PRESETS.some(preset => preset.value === value);
}

export function parseRange(raw: string | null): DateRange {
    if (raw === null) return presetRange(DEFAULT_DASHBOARD_PRESET);
    if (isPreset(raw)) return presetRange(raw);
    const parts = raw.split(CUSTOM_RANGE_SEPARATOR);
    if (parts.length !== 2) return presetRange(DEFAULT_DASHBOARD_PRESET);
    const [from, to] = parts;
    if (!isCalendarDay(from) || !isCalendarDay(to) || from > to) return presetRange(DEFAULT_DASHBOARD_PRESET);
    return { kind: "custom", from, to };
}

export function serializeRange(range: DateRange): string {
    return range.kind === "preset" ? range.preset : `${range.from}${CUSTOM_RANGE_SEPARATOR}${range.to}`;
}

export function isSameRange(a: DateRange, b: DateRange): boolean {
    return serializeRange(a) === serializeRange(b);
}

const MS_PER_DAY = 24 * 60 * 60 * 1000;

function startOfLocalDay(day: string): Date {
    const [year, month, date] = day.split("-").map(Number);
    return new Date(year, month - 1, date, 0, 0, 0, 0);
}

function endOfLocalDay(day: string): Date {
    const [year, month, date] = day.split("-").map(Number);
    return new Date(year, month - 1, date, 23, 59, 59, 999);
}

export function resolveWindow(range: DateRange, now: Date = new Date()): DashboardWindow {
    if (range.kind === "custom") {
        return { startDate: startOfLocalDay(range.from).toISOString(), endDate: endOfLocalDay(range.to).toISOString() };
    }
    const end = new Date(now);
    const start = new Date(end);
    start.setUTCDate(start.getUTCDate() - PRESET_DAYS[range.preset]);
    return { startDate: start.toISOString(), endDate: end.toISOString() };
}

const MAX_HOUR_SPAN_DAYS = 2;
const MAX_DAY_SPAN_DAYS = 31;

export function granularityFor(window: DashboardWindow): TrendBucketGranularity {
    const spanDays = Math.round((Date.parse(window.endDate) - Date.parse(window.startDate)) / MS_PER_DAY);
    if (spanDays <= MAX_HOUR_SPAN_DAYS) return "hour";
    if (spanDays <= MAX_DAY_SPAN_DAYS) return "day";
    return "week";
}

export function formatDay(day: string): string {
    const [year, month, date] = day.split("-").map(Number);
    return new Date(year, month - 1, date).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
}

export function formatRangeLabel<P extends string>(range: DateRange<P>, presets: ReadonlyArray<{ value: P; label: string }>): string {
    if (range.kind === "custom") return `${formatDay(range.from)} – ${formatDay(range.to)}`;
    return presets.find(preset => preset.value === range.preset)?.label ?? range.preset;
}

export const ALL_SUBJECTS = "all";

export interface UrlWriteOptions {
    replace?: boolean;
}

export const SHARED_FILTER_PARAM = { subject: "subject", name: "name", range: "range" } as const;

export function sharedFilterSearch(search: string): string {
    const source = new URLSearchParams(search);
    const next = new URLSearchParams();
    for (const key of Object.values(SHARED_FILTER_PARAM)) {
        const value = source.get(key);
        if (value !== null) next.set(key, value);
    }
    const query = next.toString();
    return query ? `?${query}` : "";
}

export function subjectKeyOf(subject: { id: string; type: "agent" | "workflow" }): string {
    return `${subject.type}:${subject.id}`;
}

export function subjectKindOf(subjectKey: string): "agent" | "workflow" | undefined {
    const separator = subjectKey.indexOf(":");
    if (separator < 0) return undefined;
    const kind = subjectKey.slice(0, separator);
    return kind === "agent" || kind === "workflow" ? kind : undefined;
}

function isSubjectKind(kind: string | undefined): kind is "agent" | "workflow" {
    return kind === "agent" || kind === "workflow";
}

export function isSubjectKey(value: string): boolean {
    if (value === ALL_SUBJECTS) return true;
    const [kind, id] = value.split(":");
    return isSubjectKind(kind) && Boolean(id);
}

export function subjectFilterFor(subjectKey: string): { agentIds?: string[]; workflowIds?: string[] } {
    if (subjectKey === ALL_SUBJECTS) return {};
    const separator = subjectKey.indexOf(":");
    if (separator < 0) return {};
    const kind = subjectKey.slice(0, separator);
    const id = subjectKey.slice(separator + 1);
    if (kind === "agent") return { agentIds: [id] };
    if (kind === "workflow") return { workflowIds: [id] };
    return {};
}
