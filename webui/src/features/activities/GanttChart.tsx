// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import type { TooltipContentProps } from "recharts";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import { type AgentPerformanceMetrics, type LlmCallPerformance, type ToolCallPerformance } from "@/types/activities";

interface GanttChartProps {
    agent: AgentPerformanceMetrics;
}

type GanttBar = {
    name: string;
    fullName: string;
    type: "LLM" | "Tool" | "Peer";
    start: number;
    duration: number;
    end: number;
};

const CustomTooltip = ({ active, payload }: TooltipContentProps) => {
    if (active && payload?.length) {
        const data: GanttBar = payload[0].payload;
        if (data) {
            return (
                <div className="rounded-md border bg-(--background-w10) p-2 text-sm shadow-lg">
                    <p className="font-bold">{data.fullName}</p>
                    <p className="text-(--secondary-text-wMain)">Duration: {(data.duration / 1000).toFixed(2)}s</p>
                </div>
            );
        }
    }
    return null;
};

export const GanttChart: React.FC<GanttChartProps> = ({ agent }) => {
    const processDataForGantt = () => {
        const allEvents: (LlmCallPerformance | ToolCallPerformance)[] = [...agent.llmCalls, ...agent.toolCalls];
        if (allEvents.length === 0) return [];

        const taskStartTime = Math.min(...allEvents.map(e => new Date(e.timestamp).getTime()));

        const ganttData: GanttBar[] = allEvents.map(event => {
            const start = new Date(event.timestamp).getTime() - taskStartTime;
            const duration = event.durationMs;

            const isLlm = "modelName" in event;
            const isPeer = !isLlm && event.isPeer;

            let fullName: string;
            let type: GanttBar["type"];

            if (isLlm) {
                fullName = `LLM: ${event.modelName}`;
                type = "LLM";
            } else {
                const toolCall = event;
                if (isPeer) {
                    fullName = `Peer: ${toolCall.peerAgentName ?? toolCall.toolName}`;
                    type = "Peer";
                } else {
                    fullName = `Tool: ${toolCall.toolName}`;
                    type = "Tool";
                }
            }

            const TRUNCATE_LENGTH = 35;
            const name = fullName.length > TRUNCATE_LENGTH ? `${fullName.substring(0, TRUNCATE_LENGTH)}...` : fullName;

            return { name, fullName, type, start, duration, end: start + duration };
        });

        return ganttData.sort((a, b) => a.start - b.start);
    };

    const ganttData = processDataForGantt();

    if (ganttData.length === 0) {
        return <div className="py-4 text-center text-(--secondary-text-wMain)">No calls to display in timeline.</div>;
    }

    const taskDuration = Math.max(...ganttData.map(d => d.end));
    const barHeight = 35;
    const chartHeight = ganttData.length * barHeight + 40;

    const getColor = (type: GanttBar["type"]) => {
        switch (type) {
            case "LLM":
                return "var(--brand-w60)";
            case "Peer":
                return "var(--primary-wMain)";
            case "Tool":
                return "var(--accent-n3-wMain)";
            default:
                return "var(--primary-wMain)";
        }
    };

    return (
        <div className="mt-4 w-full">
            <div className="mb-3 flex items-center gap-4 text-sm">
                <div className="flex items-center gap-1">
                    <div className="h-3 w-3 rounded" style={{ backgroundColor: getColor("LLM") }} />
                    <span>LLM Calls</span>
                </div>
                <div className="flex items-center gap-1">
                    <div className="h-3 w-3 rounded" style={{ backgroundColor: getColor("Tool") }} />
                    <span>Tool Calls</span>
                </div>
                <div className="flex items-center gap-1">
                    <div className="h-3 w-3 rounded" style={{ backgroundColor: getColor("Peer") }} />
                    <span>Peer Delegations</span>
                </div>
            </div>

            <div style={{ height: `${chartHeight}px` }}>
                <ResponsiveContainer width="100%" height="100%">
                    <BarChart data={ganttData} layout="vertical" margin={{ top: 5, right: 30, left: 20, bottom: 5 }} barCategoryGap="20%">
                        <CartesianGrid strokeDasharray="3 3" />
                        <XAxis type="number" domain={[0, taskDuration]} tickFormatter={ms => `${(ms / 1000).toFixed(2)}s`} tick={{ fontSize: 12, fill: "var(--primary-text-wMain)" }} />
                        <YAxis type="category" dataKey="name" width={220} interval={0} tick={{ fontSize: 12, fill: "var(--primary-text-wMain)" }} />
                        <Tooltip cursor={{ fill: "var(--secondary-w10)", fillOpacity: 0.1 }} content={CustomTooltip} />
                        <Bar dataKey="start" stackId="a" fill="transparent" stroke="transparent" />
                        <Bar dataKey="duration" stackId="a">
                            {ganttData.map(entry => (
                                <Cell key={`${entry.type}-${entry.start}-${entry.fullName}`} fill={getColor(entry.type)} />
                            ))}
                        </Bar>
                    </BarChart>
                </ResponsiveContainer>
            </div>
        </div>
    );
};
