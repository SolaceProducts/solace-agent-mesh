// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TargetType } from "@/types/scheduled-tasks";

export interface TaskConfig {
    name: string;
    description: string;
    scheduleType: "cron" | "interval" | "one_time";
    scheduleExpression: string;
    targetType: TargetType;
    targetAgentName: string;
    taskMessage: string;
    timezone: string;
    enabled: boolean;
}

export type IntervalUnit = "s" | "m" | "h" | "d";

export const INTERVAL_UNITS: Array<{ value: IntervalUnit; label: string; seconds: number }> = [
    { value: "s", label: "Seconds", seconds: 1 },
    { value: "m", label: "Minutes", seconds: 60 },
    { value: "h", label: "Hours", seconds: 3600 },
    { value: "d", label: "Days", seconds: 86400 },
];

export const INTERVAL_UNIT_LABELS: Record<IntervalUnit, string> = {
    s: "Seconds",
    m: "Minutes",
    h: "Hours",
    d: "Days",
};

export const MIN_INTERVAL_SECONDS = 60;

export const MAX_INTERVAL_SECONDS = 365 * 86400;

export function parseInterval(expr: string): { value: number; unit: IntervalUnit } | null {
    const match = /^(\d+)([smhd])$/i.exec(expr.trim());
    if (!match) return null;
    return { value: parseInt(match[1], 10), unit: match[2].toLowerCase() as IntervalUnit };
}

export function intervalToSeconds(value: number, unit: IntervalUnit): number {
    return value * (INTERVAL_UNITS.find(u => u.value === unit)?.seconds ?? 1);
}
