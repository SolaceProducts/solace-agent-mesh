// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useState } from "react";
import { BarChart2, ChevronDown, ChevronRight, Cpu, Wrench } from "lucide-react";

import { type VisualizerStep } from "@/types/activities";

import { formatMilliseconds } from "./format";

interface TimingEntry {
    count: number;
    totalDurationMs: number;
    durations: number[];
}
interface AgentTimings {
    llmCalls: Record<string, TimingEntry>;
    toolCalls: Record<string, TimingEntry>;
}
type AggregatedTimings = Record<string, AgentTimings>;

interface TaskTimingSummaryPanelProps {
    steps: VisualizerStep[];
}

const formatDuration = (ms: number): string => {
    if (ms < 0) return "N/A";
    return formatMilliseconds(ms);
};

const TaskTimingSummaryPanel: React.FC<TaskTimingSummaryPanelProps> = ({ steps }) => {
    const [isExpanded, setIsExpanded] = useState(true);

    const aggregatedData = useMemo(() => {
        const data: AggregatedTimings = {};

        steps.forEach(step => {
            if (!step.source || step.durationMs === undefined || step.durationMs < 0) {
                return;
            }

            if (!data[step.source]) {
                data[step.source] = { llmCalls: {}, toolCalls: {} };
            }
            const agentData = data[step.source];

            if (step.type === "AGENT_LLM_CALL" && step.data.llmCall) {
                const modelName = step.data.llmCall.modelName || "Unknown Model";
                if (!agentData.llmCalls[modelName]) {
                    agentData.llmCalls[modelName] = { count: 0, totalDurationMs: 0, durations: [] };
                }
                agentData.llmCalls[modelName].count++;
                agentData.llmCalls[modelName].totalDurationMs += step.durationMs;
                agentData.llmCalls[modelName].durations.push(step.durationMs);
            } else if (step.type === "AGENT_TOOL_INVOCATION_START" && step.data.toolInvocationStart) {
                const toolName = step.data.toolInvocationStart.toolName || "Unknown Tool";
                if (!agentData.toolCalls[toolName]) {
                    agentData.toolCalls[toolName] = { count: 0, totalDurationMs: 0, durations: [] };
                }
                agentData.toolCalls[toolName].count++;
                agentData.toolCalls[toolName].totalDurationMs += step.durationMs;
                agentData.toolCalls[toolName].durations.push(step.durationMs);
            }
        });
        return data;
    }, [steps]);

    const agentNames = Object.keys(aggregatedData).sort();

    if (agentNames.length === 0) {
        return <div className="p-2 text-xs text-(--secondary-text-wMain)">No performance timing data available for LLM or Tool calls.</div>;
    }

    return (
        <div className="rounded-md border bg-(--secondary-w10)">
            <button onClick={() => setIsExpanded(!isExpanded)} className="flex w-full items-center justify-between p-2 text-left text-sm font-semibold transition-colors hover:bg-(--secondary-w20)" aria-expanded={isExpanded}>
                <div className="flex items-center">
                    <BarChart2 className="mr-2 text-(--accent-n0-wMain)" size={16} />
                    Performance Summary
                </div>
                {isExpanded ? <ChevronDown size={18} /> : <ChevronRight size={18} />}
            </button>

            {isExpanded && (
                <div className="space-y-2 border-t p-2 text-xs">
                    {agentNames.map(agentName => {
                        const agentTimings = aggregatedData[agentName];
                        const llmCallEntries = Object.entries(agentTimings.llmCalls);
                        const toolCallEntries = Object.entries(agentTimings.toolCalls);

                        if (llmCallEntries.length === 0 && toolCallEntries.length === 0) {
                            return null;
                        }

                        return (
                            <div key={agentName} className="rounded border bg-(--background-w10) p-1.5 shadow-sm">
                                <h4 className="mb-1 font-semibold">{agentName}</h4>
                                {llmCallEntries.length > 0 && (
                                    <div className="mb-1 pl-2">
                                        <h5 className="flex items-center text-(--secondary-text-wMain)">
                                            <Cpu size={12} className="mr-1 text-(--accent-n3-wMain)" />
                                            LLM Calls:
                                        </h5>
                                        <ul className="list-inside list-disc space-y-0.5 pl-2">
                                            {llmCallEntries.map(([modelName, data]) => (
                                                <li key={modelName}>
                                                    {modelName}: {data.count} call(s), Avg: {formatDuration(data.totalDurationMs / data.count)}, Total: {formatDuration(data.totalDurationMs)}
                                                </li>
                                            ))}
                                        </ul>
                                    </div>
                                )}
                                {toolCallEntries.length > 0 && (
                                    <div className="pl-2">
                                        <h5 className="flex items-center text-(--secondary-text-wMain)">
                                            <Wrench size={12} className="mr-1 text-(--warning-wMain)" />
                                            Tool Calls:
                                        </h5>
                                        <ul className="list-inside list-disc space-y-0.5 pl-2">
                                            {toolCallEntries.map(([toolName, data]) => (
                                                <li key={toolName}>
                                                    {toolName}: {data.count} call(s), Avg: {formatDuration(data.totalDurationMs / data.count)}, Total: {formatDuration(data.totalDurationMs)}
                                                </li>
                                            ))}
                                        </ul>
                                    </div>
                                )}
                            </div>
                        );
                    })}
                    {agentNames.length > 0 && Object.values(aggregatedData).every(d => Object.keys(d.llmCalls).length === 0 && Object.keys(d.toolCalls).length === 0) && (
                        <p className="p-1 text-(--secondary-text-wMain)">No LLM or Tool call durations recorded for these agents.</p>
                    )}
                </div>
            )}
        </div>
    );
};

export default TaskTimingSummaryPanel;
