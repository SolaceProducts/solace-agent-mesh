// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactElement } from "react";
import { Bar, BarChart, CartesianGrid, Cell, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { UsageBudgetStatus, UsageMoney } from "@/api/usage";

import { formatBudgetMoney, usageMoneyToNumber } from "@/utils";

import { budgetLineSegments, formatAxisDollars, formatUsageMonth, PARTIAL_PERIOD_OPACITY } from "./usageChartFormat";

const BAR_COLOR = "var(--primary-wMain)";
const CURRENT_BUDGET_LINE_COLOR = "var(--secondary-text-wMain)";
const EARLIER_BUDGET_LINE_COLOR = "var(--secondary-w40)";
const BUDGET_LINE_DASH = "4 4";
const BUDGET_LINE_WIDTH = 1.5;

function renderSplitBudgetLine(monthCount: number, currentIndex: number) {
    return ({ x1, x2, y1 }: { x1?: number; x2?: number; y1?: number }) => {
        if (typeof x1 !== "number" || typeof x2 !== "number" || typeof y1 !== "number" || !Number.isFinite(x1 + x2 + y1)) return <g />;
        return (
            <g>
                {budgetLineSegments(x1, x2, monthCount, currentIndex).map(segment => (
                    <line key={segment.x1} x1={segment.x1} x2={segment.x2} y1={y1} y2={y1} stroke={segment.current ? CURRENT_BUDGET_LINE_COLOR : EARLIER_BUDGET_LINE_COLOR} strokeDasharray={BUDGET_LINE_DASH} strokeWidth={BUDGET_LINE_WIDTH} />
                ))}
            </g>
        );
    };
}

const STATUS_BAR_COLOR: Record<UsageBudgetStatus, string> = {
    ok: BAR_COLOR,
    approaching: "var(--warning-wMain)",
    exceeded: "var(--error-wMain)",
    unlimited: BAR_COLOR,
};

export interface MonthSpend {
    month: string;
    cost: UsageMoney;
    partial?: boolean;
}

export interface MonthSpendRow<T extends MonthSpend> {
    month: string;
    cost: number;
    source: T;
}

interface MonthlySpendChartProps<T extends MonthSpend> {
    months: readonly T[];
    currentBudget: UsageMoney | null;
    currentMonth: string;
    currentStatus: UsageBudgetStatus | null;
    tooltip: ReactElement;
    height: number;
    budgetLegend?: boolean;
}

export function MonthlySpendChart<T extends MonthSpend>({ months, currentBudget, currentMonth, currentStatus, tooltip, height, budgetLegend = true }: Readonly<MonthlySpendChartProps<T>>) {
    const rows: Array<MonthSpendRow<T>> = months.map(month => ({ month: month.month, cost: usageMoneyToNumber(month.cost), source: month }));
    return (
        <div className="flex flex-col gap-1">
            {currentBudget && budgetLegend && (
                <div className="flex justify-end">
                    <span className="flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                        <span className="w-4 border-t-2 border-dashed border-(--secondary-text-wMain)" aria-hidden />
                        Current budget {formatBudgetMoney(currentBudget)}
                    </span>
                </div>
            )}
            <div aria-hidden>
                <ResponsiveContainer width="100%" height={height}>
                    <BarChart accessibilityLayer={false} data={rows} margin={{ top: 8, right: 8, bottom: 0, left: -8 }}>
                        <CartesianGrid strokeDasharray="3 3" stroke="var(--secondary-w10)" vertical={false} />
                        <XAxis dataKey="month" tickFormatter={value => formatUsageMonth(String(value))} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} />
                        <YAxis tickFormatter={value => formatAxisDollars(Number(value))} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} width={56} />
                        <Tooltip cursor={{ fill: "var(--secondary-w10)" }} content={tooltip} isAnimationActive={false} />
                        <Bar dataKey="cost" radius={[2, 2, 0, 0]} isAnimationActive={false}>
                            {rows.map(row => (
                                <Cell key={row.month} fill={row.month === currentMonth && currentStatus ? STATUS_BAR_COLOR[currentStatus] : BAR_COLOR} fillOpacity={row.source.partial ? PARTIAL_PERIOD_OPACITY : 1} />
                            ))}
                        </Bar>
                        {currentBudget && (
                            <ReferenceLine
                                y={usageMoneyToNumber(currentBudget)}
                                ifOverflow="extendDomain"
                                shape={renderSplitBudgetLine(
                                    rows.length,
                                    rows.findIndex(row => row.month === currentMonth)
                                )}
                            />
                        )}
                    </BarChart>
                </ResponsiveContainer>
            </div>
        </div>
    );
}
