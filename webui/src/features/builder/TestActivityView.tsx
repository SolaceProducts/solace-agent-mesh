// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { X } from "lucide-react";

import { useTaskContext } from "@/providers/contexts";
import { useTaskActivityEvents } from "@/providers/task/useTaskActivityEvents";

import { Button } from "@/ui/button";
import { type VisualizedTask } from "@/types/activities";

import { ArtifactSessionProvider } from "@/features/activities/flowChart/artifactSessionContext";
import FlowChartPanel from "@/features/activities/flowChart/FlowChartPanel";
import { StimDownloadButton } from "@/features/activities/StimDownloadButton";
import { useReconciledActivityTask } from "@/features/activities/useReconciledActivityTask";
import { testRunnerNameOverrides } from "@/features/builder/testActivityAgentLabel";

interface Props {
    taskId: string | null;
    runNumber?: number | null;
    isLive?: boolean;
    onClose: () => void;
}

export function TestActivityView({ taskId, runNumber, isLive = false, onClose }: Props) {
    const { monitoredTasks } = useTaskContext();

    const activityTasks = useTaskActivityEvents(taskId, isLive);
    const { visualizedTask, isLoadingTask } = useReconciledActivityTask(taskId, activityTasks, isLive);

    const agentNameOverrides = useMemo(() => testRunnerNameOverrides(visualizedTask), [visualizedTask]);

    const headerLabel = runNumber !== null && runNumber !== undefined ? `Test ${runNumber} Activity` : "Test Activity";

    let artifactSessionId: string | undefined;
    if (taskId) {
        const liveSessionId = isLive ? activityTasks[taskId]?.sessionId : undefined;
        artifactSessionId = liveSessionId ?? monitoredTasks[taskId]?.sessionId;
    }

    return (
        <div className="absolute inset-0 z-10 flex flex-col bg-(--background-w10) ring-2 ring-(--accent-n0-w30)">
            {!isLive && (
                <div className="pointer-events-none absolute top-3 right-0 left-0 z-20 flex items-center justify-center px-4">
                    <div className="pointer-events-auto flex items-center gap-3 rounded-md border border-(--accent-n0-w30) bg-(--accent-n0-w10) px-3 py-1.5 text-sm shadow-sm">
                        <span className="font-semibold text-(--primary-text-wMain)">{headerLabel}</span>
                        <StimDownloadButton taskId={taskId} className="h-6 w-6 shrink-0 p-0 text-(--secondary-text-wMain)" />
                        <Button variant="ghost" size="sm" className="h-6 w-6 shrink-0 p-0 text-(--secondary-text-wMain)" onClick={onClose} tooltip="Close (return to component view)">
                            <X className="h-3.5 w-3.5" />
                        </Button>
                    </div>
                </div>
            )}
            <div className="min-h-0 flex-1 overflow-hidden">
                <TestActivityBody isLoadingTask={isLoadingTask} visualizedTask={visualizedTask} taskId={taskId} artifactSessionId={artifactSessionId} agentNameOverrides={agentNameOverrides} />
            </div>
        </div>
    );
}

function TestActivityBody({
    isLoadingTask,
    visualizedTask,
    taskId,
    artifactSessionId,
    agentNameOverrides,
}: Readonly<{
    isLoadingTask: boolean;
    visualizedTask: VisualizedTask | null;
    taskId: string | null;
    artifactSessionId?: string;
    agentNameOverrides?: Record<string, string>;
}>) {
    if (!taskId) {
        return <ActivityEmpty message="No task selected." />;
    }
    if (isLoadingTask) {
        return <ActivityEmpty message="Loading activity data…" />;
    }
    if (!visualizedTask) {
        return <ActivityEmpty message="No activity data available for this task yet." />;
    }
    return (
        <div className="flex h-full flex-col">
            <ArtifactSessionProvider sessionId={artifactSessionId}>
                <FlowChartPanel processedSteps={visualizedTask.steps || []} isRightPanelVisible={false} agentNameOverrides={agentNameOverrides} />
            </ArtifactSessionProvider>
        </div>
    );
}

function ActivityEmpty({ message, children }: { message: string; children?: React.ReactNode }) {
    return (
        <div className="flex h-full items-center justify-center p-4 text-center text-xs">
            <div>
                <p>{message}</p>
                {children}
            </div>
        </div>
    );
}
