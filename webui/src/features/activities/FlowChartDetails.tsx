// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type JSX, useMemo } from "react";

import { useChatContext } from "@/providers/contexts";

import { LoadingMessageRow } from "@/common";
import type { MessageFE, TextPart, VisualizedTask } from "@/types";
import { Badge } from "@/ui";

import { StimDownloadButton } from "./StimDownloadButton";

const getStatusBadge = (status: string, type: "info" | "error" | "success") => {
    return (
        <Badge type={type} className={`rounded-full border-none`}>
            <span className="text-xs font-semibold" title={status}>
                {status}
            </span>
        </Badge>
    );
};

const staticStatus = (text: string): JSX.Element => (
    <div className="truncate text-(--secondary-text-wMain)" title={text}>
        {text}
    </div>
);

const getTaskStatus = (task: VisualizedTask, loadingMessage: MessageFE | undefined, isRunning: boolean): string | JSX.Element => {
    if (task.currentStatusText) {
        return isRunning ? (
            <div title={task.currentStatusText}>
                <LoadingMessageRow statusText={task.currentStatusText} />
            </div>
        ) : (
            staticStatus(task.currentStatusText)
        );
    }

    const loadingMessageText = loadingMessage?.parts
        ?.filter(p => p.kind === "text")
        .map(p => (p as TextPart).text)
        .join("");

    switch (task.status) {
        case "submitted":
        case "working":
            if (!isRunning) return loadingMessageText ? staticStatus(loadingMessageText) : getStatusBadge("Ended", "info");
            return (
                <div title={loadingMessageText || task.status}>
                    <LoadingMessageRow statusText={loadingMessageText || task.status} />
                </div>
            );
        case "input-required":
            return getStatusBadge("Input Required", "info");
        case "completed":
            return getStatusBadge("Completed", "success");
        case "canceled":
            return getStatusBadge("Canceled", "info");
        case "failed":
            return getStatusBadge("Failed", "error");
        default:
            return getStatusBadge("Unknown", "info");
    }
};

export const FlowChartDetails: React.FC<{ task: VisualizedTask }> = ({ task }) => {
    const { messages } = useChatContext();

    const taskStatus = useMemo(() => {
        const loadingMessage = messages.find(message => message.isStatusBubble);
        const isRunning = !!loadingMessage || messages.some(m => m.taskId === task?.taskId && !m.isComplete);

        return task ? getTaskStatus(task, loadingMessage, isRunning) : null;
    }, [messages, task]);

    return task ? (
        <div className="grid grid-cols-[auto_1fr_auto] grid-rows-[32px_32px] items-center gap-x-8 border-b p-4">
            <div className="text-(--secondary-text-wMain)">User</div>
            <div className="truncate" title={task.initialRequestText}>
                {task.initialRequestText}
            </div>
            <div />

            <div className="text-(--secondary-text-wMain)">Status</div>
            <div className="truncate">{taskStatus}</div>

            <div>
                <StimDownloadButton taskId={task.taskId} />
            </div>
        </div>
    ) : null;
};
