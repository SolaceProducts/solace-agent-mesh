// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { cn } from "@/utils";

import { formatRowTimestamp } from "./format";
import type { SessionGroup } from "./sessionGrouping";
import TaskListPanel from "./TaskListPanel";
import { TokenUsageLine } from "./TokenUsageLine";

interface SessionListPanelProps {
    sessions: SessionGroup[];
    expandedSessionKeys: Set<string>;
    onToggleSession: (sessionKey: string) => void;
    selectedTaskId: string | null;
    onSelectTask: (taskId: string) => void;
    expandedTaskIds: Set<string>;
    onToggleExpand: (taskId: string) => void;
    framed?: boolean;
}

const turnsLabel = (turnCount: number) => (turnCount === 1 ? "1 turn" : `${turnCount} turns`);

const SessionListPanel: React.FC<SessionListPanelProps> = ({ sessions, expandedSessionKeys, onToggleSession, selectedTaskId, onSelectTask, expandedTaskIds, onToggleExpand, framed = false }) => {
    const renderSessionRow = (session: SessionGroup) => {
        const isExpanded = expandedSessionKeys.has(session.key);
        const containsSelectedTask = selectedTaskId !== null && session.tasks.some(task => task.taskId === selectedTaskId);
        const { latestTask } = session;
        const fullTimestamp = formatRowTimestamp(new Date(latestTask.lastUpdated));
        const latestPrompt = latestTask.initialRequestText || "Task started...";
        const sessionLine = session.sessionId ? `Session ID: ${session.sessionId}\n` : "";
        const promptLine = session.name ? `Latest turn: ${latestPrompt}\n` : "";
        const userLine = latestTask.userId ? `\nUser: ${latestTask.userId}` : "";
        const rowTitle = `${sessionLine}${promptLine}Last Update: ${fullTimestamp}${userLine}`;
        const toggle = () => onToggleSession(session.key);
        const toggleOnKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
            if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                toggle();
            }
        };

        return (
            <div
                key={session.key}
                data-testid="session-list-item"
                data-expanded={isExpanded}
                data-latest-prompt={latestPrompt}
                className={cn(
                    "cursor-pointer border-b text-left text-(--primary-text-wMain) transition-colors duration-150 focus:ring-2 focus:outline-none",
                    framed && "border-x first:border-t",
                    containsSelectedTask && !isExpanded ? "bg-(--primary-w10)" : "hover:bg-(--secondary-w10)"
                )}
            >
                <div role="button" tabIndex={0} aria-expanded={isExpanded} onClick={toggle} onKeyDown={toggleOnKey} className="flex w-full items-center justify-between px-4 py-3" title={rowTitle}>
                    <div className="flex min-w-0 flex-1 items-start">
                        <span className="mr-1.5 flex-shrink-0 rounded-full p-0.5" data-testid="session-expand-toggle" title={isExpanded ? "Collapse session" : "Expand session"}>
                            {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                        </span>
                        <div className="min-w-0 flex-1">
                            <p className="mb-1 truncate text-left text-sm font-medium" data-testid="session-list-item-title">
                                {session.name ?? latestPrompt}
                            </p>
                            <p className="truncate text-left text-xs text-(--secondary-text-wMain)">
                                {fullTimestamp}
                                {latestTask.userId && <span data-testid="session-list-item-user"> · {latestTask.userId}</span>}
                                <span data-testid="session-list-item-turns"> · {turnsLabel(session.turnCount)}</span>
                            </p>
                        </div>
                    </div>
                    <div className="ml-2 flex-shrink-0 text-right">
                        <p className={cn("text-xs leading-5 text-(--secondary-text-wMain)", session.tokenUsage && "mb-1")}>Events: {session.eventCount}</p>
                        {session.tokenUsage && <TokenUsageLine usage={session.tokenUsage} className="text-(--secondary-text-wMain)" testId="session-list-item-tokens" />}
                    </div>
                </div>
                {isExpanded && (
                    <div className="border-t pl-5" data-testid="session-task-list">
                        <TaskListPanel hierarchicalTasks={session.tasks} selectedTaskId={selectedTaskId} onSelectTask={onSelectTask} expandedTaskIds={expandedTaskIds} onToggleExpand={onToggleExpand} />
                    </div>
                )}
            </div>
        );
    };

    return <div>{sessions.map(renderSessionRow)}</div>;
};

export default SessionListPanel;
