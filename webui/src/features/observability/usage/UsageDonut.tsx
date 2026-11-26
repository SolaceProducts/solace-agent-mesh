// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Cell, Pie, PieChart, Tooltip } from "recharts";

import { TOOLTIP_SURFACE } from "@/ui/tooltip-surface";

import { cn } from "@/utils";

export interface DonutSlice {
    key: string;
    value: number;
    color: string;
    label: string;
    detail: string;
}

const DEFAULT_SIZE = 112;

function DonutTooltip({ active, payload }: Readonly<{ active?: boolean; payload?: ReadonlyArray<{ payload?: unknown }> }>) {
    const slice = active ? (payload?.[0]?.payload as DonutSlice | undefined) : undefined;
    if (!slice?.label) return null;
    return (
        <div className={cn("whitespace-nowrap", TOOLTIP_SURFACE)}>
            <div className="font-semibold">{slice.label}</div>
            <div>{slice.detail}</div>
        </div>
    );
}

export function UsageDonut({ slices, size = DEFAULT_SIZE }: Readonly<{ slices: readonly DonutSlice[]; size?: number }>) {
    const hasValue = slices.some(slice => slice.value > 0);
    const data = hasValue ? slices : [{ key: "empty", value: 1, color: "var(--secondary-w20)", label: "", detail: "" }];
    return (
        <div aria-hidden className="shrink-0 [&_*]:outline-none" onMouseDown={event => event.preventDefault()}>
            <PieChart accessibilityLayer={false} width={size} height={size}>
                <Pie
                    data={data as DonutSlice[]}
                    dataKey="value"
                    nameKey="key"
                    innerRadius={size * 0.34}
                    outerRadius={size / 2}
                    startAngle={90}
                    endAngle={-270}
                    stroke="var(--background-w10)"
                    strokeWidth={2}
                    rootTabIndex={-1}
                    isAnimationActive={false}
                >
                    {data.map(slice => (
                        <Cell key={slice.key} fill={slice.color} />
                    ))}
                </Pie>
                {hasValue && <Tooltip content={<DonutTooltip />} allowEscapeViewBox={{ x: true, y: true }} wrapperStyle={{ zIndex: 10 }} isAnimationActive={false} />}
            </PieChart>
        </div>
    );
}
