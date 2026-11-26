// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { ChevronDown, ChevronRight } from "lucide-react";

import { type TaskFE } from "@/types/chat";

import { cn } from "@/utils";

import { formatRowTimestamp } from "./format";
import { taskEventCount, taskTokenUsage } from "./sessionGrouping";
import { taskOriginLine } from "./taskTrigger";
import { TokenUsageLine } from "./TokenUsageLine";

export interface HierarchicalTaskItem extends TaskFE {
    children: HierarchicalTaskItem[];
    nestingLevel: number;
}

interface TaskListPanelProps {
    hierarchicalTasks: HierarchicalTaskItem[];
    selectedTaskId: string | null;
    onSelectTask: (taskId: string) => void;
    expandedTaskIds: Set<string>;
    onToggleExpand: (taskId: string) => void;
}

const INDENT_PX_PER_LEVEL = 20;

const TaskListPanel: React.FC<TaskListPanelProps> = ({ hierarchicalTasks, selectedTaskId, onSelectTask, expandedTaskIds, onToggleExpand }) => {
    const renderTaskItem = (task: HierarchicalTaskItem) => {
        const isSelected = task.taskId === selectedTaskId;
        const fullTimestamp = formatRowTimestamp(new Date(task.lastUpdated));
        const hasChildren = task.children.length > 0;
        const isExpanded = hasChildren && expandedTaskIds.has(task.taskId);
        const tokenUsage = taskTokenUsage(task);

        const originLine = taskOriginLine(task);
        const rowTitle = `Task ID: ${task.taskId}\nLast Update: ${fullTimestamp}`;
        const selectTask = () => onSelectTask(task.taskId);
        const selectTaskOnKey = (e: React.KeyboardEvent<HTMLDivElement>) => {
            if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                selectTask();
            }
        };

        return (
            <div
                key={task.taskId}
                data-testid="task-list-item"
                className={cn("cursor-pointer text-left transition-colors duration-150 focus:ring-2 focus:outline-none", isSelected ? "bg-(--primary-w10) text-(--primary-text-wMain)" : "text-(--primary-text-wMain) hover:bg-(--secondary-w10)")}
                style={{ marginLeft: `${task.nestingLevel * INDENT_PX_PER_LEVEL}px` }}
            >
                <div role="button" tabIndex={0} onClick={selectTask} onKeyDown={selectTaskOnKey} className="flex w-full items-center justify-between px-4 py-3" title={rowTitle}>
                    <div className="flex min-w-0 flex-1 items-start">
                        <div className="flex flex-shrink-0 items-center">
                            {hasChildren ? (
                                <span
                                    onClick={e => {
                                        e.stopPropagation();
                                        onToggleExpand(task.taskId);
                                    }}
                                    className="mr-1.5 cursor-pointer rounded-full p-0.5 hover:bg-(--secondary-w10)"
                                    title={isExpanded ? "Collapse sub-tasks" : "Expand sub-tasks"}
                                >
                                    {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                                </span>
                            ) : (
                                <span className="mr-1.5 w-[18px]"></span>
                            )}
                        </div>
                        <div className="min-w-0 flex-1">
                            <p className="mb-1 truncate text-left text-sm font-medium">{task.initialRequestText || "Task started..."}</p>
                            {originLine && (
                                <p data-testid="task-list-item-origin" className={cn("truncate text-left text-xs", isSelected ? "text-opacity-80" : "text-(--secondary-text-wMain)")}>
                                    {originLine}
                                </p>
                            )}
                        </div>
                    </div>
                    <div className="ml-2 flex-shrink-0 text-right">
                        <p className={cn("text-xs leading-5", tokenUsage && "mb-1", isSelected ? "text-opacity-80" : "text-(--secondary-text-wMain)")}>Events: {taskEventCount(task)}</p>
                        {tokenUsage && <TokenUsageLine usage={tokenUsage} className={isSelected ? "text-opacity-80" : "text-(--secondary-text-wMain)"} testId="task-list-item-tokens" />}
                    </div>
                </div>
            </div>
        );
    };

    const renderTasksRecursively = (tasksToRender: HierarchicalTaskItem[]) => {
        return tasksToRender.map(task => (
            <React.Fragment key={task.taskId}>
                {renderTaskItem(task)}
                {expandedTaskIds.has(task.taskId) && task.children.length > 0 && renderTasksRecursively(task.children)}
            </React.Fragment>
        ));
    };

    return <div>{renderTasksRecursively(hierarchicalTasks)}</div>;
};

export default TaskListPanel;
