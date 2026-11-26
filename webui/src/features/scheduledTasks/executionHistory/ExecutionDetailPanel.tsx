// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Loader2 } from "lucide-react";

import { useExecutionArtifacts } from "@/api/scheduled-tasks";

import { formatDuration, formatEpochTimestampShort } from "@/utils/format";

import type { TaskExecution } from "@/types/scheduled-tasks";

import { ExecutionArtifactsView } from "@/features/scheduledTasks/ExecutionArtifactsView";
import { getStatusBadge, IN_PROGRESS_STATUSES } from "@/features/scheduledTasks/StatusBadge";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/ui";

import { ExecutionOutput } from "./ExecutionOutput";
import { Metric } from "./helpers";

interface ExecutionDetailPanelProps {
    execution: TaskExecution;
    activeTab: "output" | "artifacts";
    onTabChange: (tab: "output" | "artifacts") => void;
}

export const ExecutionDetailPanel: React.FC<ExecutionDetailPanelProps> = ({ execution, activeTab, onTabChange }) => {
    const { data: artifacts } = useExecutionArtifacts(execution.sessionId);
    const hasArtifacts = (artifacts?.length ?? 0) > 0;

    const isInFlight = IN_PROGRESS_STATUSES.has(execution.status);
    const startedAt = execution.startedAt ? formatEpochTimestampShort(execution.startedAt) : "—";
    const completedAt = execution.completedAt ? formatEpochTimestampShort(execution.completedAt) : "—";
    const duration = execution.durationMs ? formatDuration(execution.durationMs) : "—";

    const metrics = (
        <>
            <Metric label="Status">{getStatusBadge(execution.status)}</Metric>
            <Metric label="Started On">{startedAt}</Metric>
            <Metric label="Completed On">{completedAt}</Metric>
            <Metric label="Duration">{isInFlight ? <Loader2 className="h-4 w-4 animate-spin text-(--brand-wMain)" /> : duration}</Metric>
        </>
    );

    const fullText = execution.resultSummary?.agentResponseFull || execution.resultSummary?.agentResponse || execution.resultSummary?.messages?.find(m => m.role === "agent")?.text || "";

    const outputBody =
        !fullText && execution.errorMessage ? (
            <div className="text-sm whitespace-pre-wrap text-(--error-wMain)">{execution.errorMessage}</div>
        ) : (
            <ExecutionOutput sessionId={execution.sessionId} text={fullText} ragData={execution.resultSummary?.ragData} />
        );

    if (!hasArtifacts) {
        return (
            <section className="space-y-4">
                <div className="-mt-6 -ml-4 flex items-center gap-8 border-b py-3 pl-4 md:-ml-8 md:pl-8">
                    <div className="flex flex-1 flex-wrap items-center gap-4 md:gap-8">{metrics}</div>
                </div>
                {outputBody}
            </section>
        );
    }

    return (
        <section className="space-y-4">
            <Tabs value={activeTab} onValueChange={value => onTabChange(value as "output" | "artifacts")}>
                <div className="-mt-6 -ml-4 flex flex-col items-start gap-3 border-b py-3 pl-4 md:-ml-8 md:flex-row md:items-center md:gap-8 md:pl-8">
                    <TabsList className="flex bg-transparent p-0">
                        <TabsTrigger value="output" className="relative rounded-none rounded-l-md px-3 data-[state=active]:z-10">
                            Output
                        </TabsTrigger>
                        <TabsTrigger value="artifacts" className="relative rounded-none rounded-r-md border-l-0 px-3 data-[state=active]:z-10">
                            Artifacts
                        </TabsTrigger>
                    </TabsList>
                    <div className="flex flex-1 flex-wrap items-center gap-4 md:gap-8">{metrics}</div>
                </div>
                <TabsContent value="output" className="mt-4">
                    {outputBody}
                </TabsContent>
                <TabsContent value="artifacts" className="mt-4">
                    <ExecutionArtifactsView sessionId={execution.sessionId} />
                </TabsContent>
            </Tabs>
        </section>
    );
};
