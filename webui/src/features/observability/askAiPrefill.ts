// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type DashboardDatePreset, type DateRange, formatRangeLabel } from "./dashboardFilters";

export interface AskAiPrefillFilters {
    range: DateRange;
    presets: ReadonlyArray<{ value: DashboardDatePreset; label: string }>;
    subjectLabel?: string;
}

function timeframeClause(range: DateRange, presets: AskAiPrefillFilters["presets"]): string {
    const label = formatRangeLabel(range, presets);
    if (range.kind === "custom") return ` between ${label.replace(" – ", " and ")}`;
    return ` in the last ${label.replace(/^Last /, "").toLowerCase()}`;
}

export function buildAskAiPrefill({ range, presets, subjectLabel }: AskAiPrefillFilters): string {
    const subjectClause = subjectLabel ? ` to ${subjectLabel}` : "";
    return `Tell me about feedback given${subjectClause}${timeframeClause(range, presets)}`;
}
