// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type DashboardDatePreset, type DateRange, presetRange } from "./dashboardFilters";

interface Preset<V extends string> {
    value: V;
    label: string;
}

export function presetsWithinRetention<V extends string>(presets: ReadonlyArray<Preset<V>>, days: Partial<Record<V, number>>, maxDays: number | undefined): ReadonlyArray<Preset<V>> {
    if (maxDays === undefined) return presets;
    const within = presets.filter(preset => {
        const reach = days[preset.value];
        return reach === undefined || reach <= maxDays;
    });
    return within.length > 0 ? within : presets.slice(0, 1);
}

export function longestAvailablePreset<V extends string>(current: V, presetsShortestFirst: ReadonlyArray<Preset<V>>): V {
    if (presetsShortestFirst.some(preset => preset.value === current)) return current;
    return presetsShortestFirst.at(-1)?.value ?? current;
}

export function defaultRangeWithinRetention(defaultPreset: DashboardDatePreset, available: ReadonlyArray<Preset<DashboardDatePreset>>): DateRange {
    return presetRange(longestAvailablePreset(defaultPreset, available));
}

export const MAX_WINDOW_DAYS = 366;
const MS_PER_DAY = 24 * 60 * 60 * 1000;

function toCalendarDay(at: Date): string {
    const year = at.getFullYear();
    const month = String(at.getMonth() + 1).padStart(2, "0");
    const day = String(at.getDate()).padStart(2, "0");
    return `${year}-${month}-${day}`;
}

export function retentionFloorDate(retentionDays: number | undefined, now: Date = new Date()): string | undefined {
    if (retentionDays === undefined) return undefined;
    const floor = new Date(now);
    floor.setDate(floor.getDate() - retentionDays);
    return toCalendarDay(floor);
}

export function todayCalendarDay(now: Date = new Date()): string {
    return toCalendarDay(now);
}

interface RangeWithinRetentionArgs {
    range: DateRange;
    presets: ReadonlyArray<{ value: DashboardDatePreset; label: string }>;
    floorDate: string | undefined;
    ceilingDate: string;
    defaultPreset: DashboardDatePreset;
}

export function spanDays(from: string, to: string): number {
    return Math.round((Date.parse(`${to}T00:00:00.000Z`) - Date.parse(`${from}T00:00:00.000Z`)) / MS_PER_DAY) + 1;
}

function clampDay(day: string, floorDate: string | undefined, ceilingDate: string): string {
    if (floorDate !== undefined && day < floorDate) return floorDate;
    if (day > ceilingDate) return ceilingDate;
    return day;
}

export function rangeWithinRetention({ range, presets, floorDate, ceilingDate, defaultPreset }: RangeWithinRetentionArgs): DateRange {
    if (range.kind === "preset") return presetRange(longestAvailablePreset(range.preset, presets));
    if (spanDays(range.from, range.to) > MAX_WINDOW_DAYS) return presetRange(defaultPreset);
    if (range.from > ceilingDate) return presetRange(defaultPreset);
    if (floorDate !== undefined && range.to < floorDate) return presetRange(defaultPreset);
    const from = clampDay(range.from, floorDate, ceilingDate);
    const to = clampDay(range.to, floorDate, ceilingDate);
    if (from === range.from && to === range.to) return range;
    return { kind: "custom", from, to };
}
