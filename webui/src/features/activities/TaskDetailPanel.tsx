// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { BarChart2, PanelRightIcon, Sparkles, ToggleLeft, ToggleRight, X } from "lucide-react";

import FlowChartPanel from "@/features/activities/flowChart/FlowChartPanel";

import { type ArtifactInfo, type TaskFE, type VisualizedTask } from "@/types";
import { Button, ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/ui";

import EventDetailsPanel from "./EventDetailsPanel";
import LinearStepViewPanel from "./LinearStepViewPanel";
import { StimDownloadButton } from "./StimDownloadButton";
import { TaskOrigin } from "./TaskOrigin";

export type TaskDetailState = "ready" | "loading" | "unavailable" | "error" | "empty";

const TASK_DETAIL_STATE_MESSAGES: Record<Exclude<TaskDetailState, "ready" | "empty">, string> = {
    loading: "Loading task details…",
    unavailable: "Task details are not available.",
    error: "Couldn't load task details.",
};

const DISABLED_LOOK = "aria-disabled:cursor-not-allowed aria-disabled:opacity-50";

export interface AskAiAction {
    canStart: boolean;
    disabledReason?: string;
    onStart: () => void;
}

interface TaskDetailPanelProps {
    task: TaskFE;
    persistedTask: TaskFE | null;
    visualizedTask: VisualizedTask | null;
    detailState: TaskDetailState;
    askAi?: AskAiAction;
    onClose: () => void;
    onOpenPerformance: () => void;
    artifactLookup: (filename: string) => ArtifactInfo | undefined;
    onViewArtifact: (artifact: ArtifactInfo, version?: number) => void;
}

const ASK_AI_TITLE = "Ask the Activity Monitor about this task";
const ASK_AI_DISABLED_TITLE = "Ask AI needs your own task or permission to view this agent's activities";

const ESCAPE_OWNERS = '[role="dialog"], input, textarea, select, [contenteditable="true"]';
const useEscapeOutsideDialogs = (onEscape: () => void) => {
    useEffect(() => {
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.key !== "Escape") return;
            if (event.target instanceof Element && event.target.closest(ESCAPE_OWNERS)) return;
            onEscape();
        };
        document.addEventListener("keydown", handleKeyDown);
        return () => document.removeEventListener("keydown", handleKeyDown);
    }, [onEscape]);
};

export function TaskDetailPanel({ task, persistedTask, visualizedTask, detailState, askAi, onClose, onOpenPerformance, artifactLookup, onViewArtifact }: Readonly<TaskDetailPanelProps>) {
    const [showRawEventsView, setShowRawEventsView] = useState(false);
    const [isStepPanelVisible, setIsStepPanelVisible] = useState(true);
    useEscapeOutsideDialogs(onClose);

    const askAiCanStart = askAi !== undefined && askAi.canStart && detailState !== "unavailable";
    const askAiDisabledReason = detailState === "unavailable" ? TASK_DETAIL_STATE_MESSAGES.unavailable : askAi?.disabledReason;

    const steps = visualizedTask?.steps ?? [];

    const renderHeader = () => (
        <div className="flex flex-shrink-0 items-center justify-between border-b p-4">
            <div className="min-w-0 flex-1">
                <h3 className="truncate text-lg font-semibold" data-testid="task-heading" title={task.initialRequestText}>
                    {task.initialRequestText}
                </h3>
                <p className="truncate text-xs text-(--secondary-text-wMain)" title={task.taskId}>
                    Task:{" "}
                    <span data-testid="task-id" className="font-mono">
                        {task.taskId}
                    </span>
                </p>
                <TaskOrigin subject={task.subject} trigger={task.trigger} />
            </div>
            <div className="ml-2 flex shrink-0 items-center gap-1">
                {askAi && (
                    <Button
                        onClick={() => {
                            if (askAiCanStart) askAi.onStart();
                        }}
                        variant="ghost"
                        size="sm"
                        className={DISABLED_LOOK}
                        aria-disabled={!askAiCanStart || undefined}
                        aria-label="Ask AI"
                        tooltip={askAiCanStart ? ASK_AI_TITLE : (askAiDisabledReason ?? ASK_AI_DISABLED_TITLE)}
                        data-testid="ask-ai"
                    >
                        <Sparkles />
                        <span className="hidden sm:inline">Ask AI</span>
                    </Button>
                )}
                <StimDownloadButton taskId={task.taskId} unavailableReason={detailState === "ready" || detailState === "empty" ? undefined : TASK_DETAIL_STATE_MESSAGES[detailState]} className={DISABLED_LOOK} />
                <Button onClick={onOpenPerformance} variant="ghost" size="sm" title="View Performance Insights" data-testid="view-performance-insights" disabled={!visualizedTask}>
                    <BarChart2 />
                    <span className="hidden sm:inline">Performance</span>
                </Button>
                <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={() => setIsStepPanelVisible(prev => !prev)} title="Toggle Linear Step View Panel" data-testid="toggle-right-panel">
                    <PanelRightIcon className="size-5" />
                    <span className="sr-only">Toggle Linear Step View Panel</span>
                </Button>
                <Button variant="ghost" size="sm" className="h-8 w-8 p-0" onClick={onClose} title="Close task details" data-testid="close-task-details">
                    <X className="size-5" />
                    <span className="sr-only">Close task details</span>
                </Button>
            </div>
        </div>
    );

    const renderStepPanel = () => (
        <>
            <ResizableHandle />
            <ResizablePanel id="task-details-steps" defaultSize="40%" minSize="20%" maxSize="70%" className="relative">
                <div className="absolute top-3 right-3 z-10">
                    <Button onClick={() => setShowRawEventsView(prev => !prev)} variant="ghost" title="Toggle Event View" data-testid="toggle-event-view">
                        {showRawEventsView ? <ToggleLeft /> : <ToggleRight />}
                    </Button>
                </div>
                <div data-testid="event-panel-content" className="h-full overflow-y-auto p-4">
                    {showRawEventsView ? (
                        <div data-testid="raw-event-view-panel">
                            <EventDetailsPanel selectedTask={persistedTask} />
                        </div>
                    ) : (
                        <div data-testid="step-view-panel">
                            <LinearStepViewPanel steps={steps} artifactLookup={artifactLookup} onViewArtifact={onViewArtifact} />
                        </div>
                    )}
                </div>
            </ResizablePanel>
        </>
    );

    return (
        <div role="region" aria-label="Task details" data-testid="task-detail-panel" className="flex h-full w-full flex-col border-l bg-(--background-w10)">
            {renderHeader()}
            {detailState === "ready" || detailState === "empty" ? (
                <ResizablePanelGroup orientation="horizontal" className="min-h-0 flex-1">
                    <ResizablePanel id="task-details-flow-chart" defaultSize="60%" minSize="30%">
                        <FlowChartPanel processedSteps={steps} isRightPanelVisible={isStepPanelVisible} />
                    </ResizablePanel>
                    {isStepPanelVisible && renderStepPanel()}
                </ResizablePanelGroup>
            ) : (
                <div data-testid="task-detail-state" className="flex flex-1 items-center justify-center p-4 text-(--secondary-text-wMain)">
                    {TASK_DETAIL_STATE_MESSAGES[detailState]}
                </div>
            )}
        </div>
    );
}
