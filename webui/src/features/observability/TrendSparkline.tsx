// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Line, LineChart, ResponsiveContainer, YAxis } from "recharts";

import type { FeedbackTrendPoint } from "@/api/feedback";

import { type TrendDirection, trendDirectionOf } from "./trendDirection";

const SPARKLINE_HEIGHT = 28;

const DIRECTION: Record<TrendDirection, { stroke: string; label: string }> = {
    worsening: { stroke: "var(--error-wMain)", label: "Negative rate rising" },
    improving: { stroke: "var(--success-wMain)", label: "Negative rate falling" },
    steady: { stroke: "var(--secondary-text-wMain)", label: "Negative rate steady" },
};

interface TrendSparklineProps {
    trend: readonly FeedbackTrendPoint[];
    subjectName: string;
}

export function TrendSparkline({ trend, subjectName }: Readonly<TrendSparklineProps>) {
    const direction = trendDirectionOf(trend);
    const { stroke, label } = DIRECTION[direction];
    const data = trend.map(point => ({ value: point.negativeRatio }));
    const values = data.map(point => point.value);
    const dataMax = values.length > 0 ? Math.max(...values) : 0;
    const isFlat = values.length > 0 && Math.min(...values) === dataMax;
    const yDomain: [number, number] = isFlat ? [dataMax - Number.EPSILON, dataMax + Number.EPSILON] : [0, Math.max(dataMax, Number.EPSILON)];

    return (
        <div className="h-7 w-full min-w-0" role="img" aria-label={`${label} for ${subjectName}`} title={label}>
            {data.length > 1 && (
                <ResponsiveContainer width="100%" height={SPARKLINE_HEIGHT}>
                    <LineChart data={data} margin={{ top: 4, right: 2, bottom: 4, left: 2 }}>
                        <YAxis hide domain={yDomain} />
                        <Line type="linear" dataKey="value" stroke={stroke} strokeWidth={1.5} dot={false} isAnimationActive={false} connectNulls={false} />
                    </LineChart>
                </ResponsiveContainer>
            )}
        </div>
    );
}
