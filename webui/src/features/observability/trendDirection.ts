// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FeedbackTrendPoint } from "@/api/feedback";

export type TrendDirection = "worsening" | "steady" | "improving";

export const STEADY_RELATIVE_CHANGE = 0.1;

function mean(values: readonly number[]): number {
    if (values.length === 0) return 0;
    return values.reduce((sum, value) => sum + value, 0) / values.length;
}

export function trendDirectionOf(trend: readonly FeedbackTrendPoint[]): TrendDirection {
    if (trend.length < 2) return "steady";

    const half = Math.floor(trend.length / 2);
    const first = mean(trend.slice(0, half).map(point => point.negativeRatio));
    const second = mean(trend.slice(trend.length - half).map(point => point.negativeRatio));

    if (first === 0 && second === 0) return "steady";

    if (first === 0) return second > 0 ? "worsening" : "steady";

    const change = (second - first) / first;
    if (Math.abs(change) < STEADY_RELATIVE_CHANGE) return "steady";
    return change > 0 ? "worsening" : "improving";
}
