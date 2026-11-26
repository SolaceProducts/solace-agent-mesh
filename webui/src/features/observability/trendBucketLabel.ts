// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TrendBucketGranularity } from "@/api/feedback";

export function formatBucketLabel(iso: string, granularity: TrendBucketGranularity): string {
    const at = new Date(iso);
    if (Number.isNaN(at.getTime())) return iso;

    if (granularity === "hour") {
        return at.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
    }
    return formatDayBucketAtUtcMidnight(at);
}

function formatDayBucketAtUtcMidnight(at: Date): string {
    return at.toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" });
}
