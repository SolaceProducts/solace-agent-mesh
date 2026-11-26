// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useLayoutEffect, useState } from "react";
import { Loader2, MessageSquare } from "lucide-react";

import { formatDuration, formatEpochTimestampShort } from "@/utils/format";

import type { TaskExecution } from "@/types/scheduled-tasks";

import { getStatusBadge, IN_PROGRESS_STATUSES } from "@/features/scheduledTasks/StatusBadge";

import { Button } from "@/ui";

import { ExecutionOutput } from "./ExecutionOutput";
import { executionDisplayName, hasChatOutput, Metric } from "./helpers";

interface LatestExecutionPanelProps {
    execution: TaskExecution | null;
    onGoToChat: (executionId: string) => void;
    onShowFullOutput: (executionId: string) => void;
}

function useIsOverflowing(node: HTMLElement | null): boolean {
    const [overflowing, setOverflowing] = useState(false);

    useLayoutEffect(() => {
        if (!node) {
            setOverflowing(false);
            return;
        }
        const check = () => setOverflowing(node.scrollHeight > node.clientHeight + 1);
        check();
        const observer = new ResizeObserver(check);
        observer.observe(node);
        return () => observer.disconnect();
    }, [node]);

    return overflowing;
}

export const LatestExecutionPanel: React.FC<LatestExecutionPanelProps> = ({ execution, onGoToChat, onShowFullOutput }) => {
    if (!execution) {
        return <div className="rounded-md border bg-(--background-w10) p-6 text-sm text-(--secondary-text-wMain) italic">No executions yet.</div>;
    }

    const isInFlight = IN_PROGRESS_STATUSES.has(execution.status);
    const completedAt = execution.completedAt ? formatEpochTimestampShort(execution.completedAt) : "—";
    const duration = execution.durationMs ? formatDuration(execution.durationMs) : "—";

    return (
        <section className="space-y-4">
            <div className="-mt-6 -ml-4 flex flex-col items-stretch gap-4 border-b py-3 pl-4 md:-ml-8 md:flex-row md:items-start md:justify-between md:gap-8 md:pl-8">
                <div className="min-w-0 md:flex-shrink-0">
                    <div className="text-xs text-(--secondary-text-wMain)">Latest Execution</div>
                    <div className="truncate text-base font-semibold" title={executionDisplayName(execution)}>
                        {executionDisplayName(execution)}
                    </div>
                </div>
                <div className="flex flex-1 flex-wrap items-center gap-4 md:gap-8">
                    <Metric label="Status">{getStatusBadge(execution.status)}</Metric>
                    <Metric label="Completed On">{completedAt}</Metric>
                    <Metric label="Duration">{isInFlight ? <Loader2 className="h-4 w-4 animate-spin text-(--brand-wMain)" /> : duration}</Metric>
                </div>
                {hasChatOutput(execution) && (
                    <Button className="w-full md:w-auto" onClick={() => onGoToChat(execution.id)} disabled={isInFlight && !execution.startedAt}>
                        <MessageSquare className="mr-2 h-4 w-4" />
                        View Chat Output
                    </Button>
                )}
            </div>

            <div>
                <div className="mb-2 text-sm font-semibold">Output Summary</div>
                <OutputSummary execution={execution} onShowFullOutput={onShowFullOutput} />
            </div>
        </section>
    );
};

const OutputSummary: React.FC<{ execution: TaskExecution; onShowFullOutput: (id: string) => void }> = ({ execution, onShowFullOutput }) => {
    const snippetText = execution.resultSummary?.agentResponse || execution.resultSummary?.agentResponseFull || "";
    const [contentNode, setContentNode] = useState<HTMLDivElement | null>(null);
    const overflowing = useIsOverflowing(contentNode);

    const stringSignature = `${snippetText.length}:${execution.id}`;
    useEffect(() => {
        if (!contentNode) return;
        const id = requestAnimationFrame(() => {
            contentNode.dispatchEvent(new Event("transitionend"));
        });
        return () => cancelAnimationFrame(id);
    }, [contentNode, stringSignature]);

    if (!snippetText && execution.errorMessage) {
        return (
            <div className="rounded-md border bg-(--background-w10) p-4">
                <span className="line-clamp-6 whitespace-pre-wrap text-(--error-wMain)">{execution.errorMessage}</span>
            </div>
        );
    }
    if (!snippetText) {
        return (
            <div className="rounded-md border bg-(--background-w10) p-4">
                <span className="text-(--secondary-text-wMain) italic">No output yet.</span>
            </div>
        );
    }

    return (
        <div className="rounded-md border bg-(--background-w10) p-4">
            <div ref={setContentNode} className="line-clamp-6 text-sm break-words [&_p]:my-0">
                <ExecutionOutput sessionId={execution.sessionId} text={snippetText} hideArtifacts ragData={execution.resultSummary?.ragData} />
            </div>
            {overflowing && (
                <Button variant="link" size="sm" className="mt-2 h-auto p-0 text-xs font-medium" onClick={() => onShowFullOutput(execution.id)}>
                    Show more
                </Button>
            )}
        </div>
    );
};
