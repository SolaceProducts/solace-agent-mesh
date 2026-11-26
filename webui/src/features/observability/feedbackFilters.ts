// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CrossUserFeedback } from "@/api/feedback";

import type { DashboardDatePreset } from "./dashboardFilters";
import { PRESET_DAYS, SHARED_FILTER_PARAM } from "./dashboardFilters";

export type { DateRange } from "./dashboardFilters";
export { isSameRange, parseRange, presetRange, serializeRange } from "./dashboardFilters";

export type DatePreset = DashboardDatePreset;
export { DASHBOARD_DATE_PRESETS as DATE_PRESETS, DEFAULT_DASHBOARD_PRESET as DEFAULT_DATE_PRESET, PRESET_DAYS } from "./dashboardFilters";

export function startDateFor(preset: DatePreset, now: Date = new Date()): string {
    const from = new Date(now);
    from.setUTCDate(from.getUTCDate() - PRESET_DAYS[preset]);
    return from.toISOString();
}

export interface SubjectOption {
    id: string;
    kind: "agent" | "workflow";
    label: string;
}

export function subjectOptionsFrom(rows: readonly CrossUserFeedback[]): SubjectOption[] {
    const seen = new Map<string, SubjectOption>();
    for (const r of rows) {
        if (r.subjectType !== "agent" && r.subjectType !== "workflow") continue;
        const key = `${r.subjectType}:${r.subjectId}`;
        if (!seen.has(key)) seen.set(key, { id: r.subjectId, kind: r.subjectType, label: r.subjectName || r.subjectId });
    }
    return [...seen.values()];
}

export function subjectOptionFromHandoff(searchParams: URLSearchParams): SubjectOption | undefined {
    const subject = searchParams.get(SHARED_FILTER_PARAM.subject);
    if (!subject) return undefined;
    const separator = subject.indexOf(":");
    if (separator < 0) return undefined;
    const kind = subject.slice(0, separator);
    if (kind !== "agent" && kind !== "workflow") return undefined;
    const id = subject.slice(separator + 1);
    const name = searchParams.get(SHARED_FILTER_PARAM.name);
    return { id, kind, label: name || id };
}
