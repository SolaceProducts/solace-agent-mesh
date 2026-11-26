// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { MessageSquare, MoreHorizontal, SlidersHorizontal, Trash2 } from "lucide-react";

import type { TaskExecution } from "@/types/scheduled-tasks";

import { hasChatOutput } from "@/features/scheduledTasks/executionHistory/helpers";
import { IN_PROGRESS_STATUSES } from "@/features/scheduledTasks/StatusBadge";

import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui";

interface ViewChatOutputButtonProps {
    execution: TaskExecution;
    isMobile: boolean;
    onGoToChat: (executionId: string) => Promise<void>;
}

const ViewChatOutputButton: React.FC<ViewChatOutputButtonProps> = ({ execution, isMobile, onGoToChat }) => {
    const disabled = IN_PROGRESS_STATUSES.has(execution.status) && !execution.startedAt;
    if (isMobile) {
        return (
            <Button size="icon" className="h-8 w-8" onClick={() => onGoToChat(execution.id)} disabled={disabled} tooltip="View Chat Output">
                <MessageSquare className="h-4 w-4" />
            </Button>
        );
    }
    return (
        <Button onClick={() => onGoToChat(execution.id)} disabled={disabled}>
            <MessageSquare className="mr-2 h-4 w-4" />
            View Chat Output
        </Button>
    );
};

const ExecutionActionsMenu: React.FC<{ onDelete: () => void; deleteDisabled: boolean }> = ({ onDelete, deleteDisabled }) => (
    <DropdownMenu>
        <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="h-8 w-8" tooltip="Actions">
                <MoreHorizontal className="h-4 w-4" />
            </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
            <DropdownMenuItem disabled={deleteDisabled} onSelect={onDelete}>
                <Trash2 size={14} className="mr-2" />
                Delete
            </DropdownMenuItem>
        </DropdownMenuContent>
    </DropdownMenu>
);

interface ExecutionHeaderButtonsArgs {
    isMobile: boolean;
    selectedExecution: TaskExecution | null;
    onShowConfig: () => void;
    onGoToChat: (executionId: string) => Promise<void>;
    onDeleteExecution: (execution: TaskExecution) => void;
}

export function buildExecutionHeaderButtons({ isMobile, selectedExecution, onShowConfig, onGoToChat, onDeleteExecution }: ExecutionHeaderButtonsArgs): React.ReactNode[] | undefined {
    const buttons: React.ReactNode[] = [];
    if (isMobile) {
        buttons.push(
            <Button key="config" variant="ghost" size="icon" className="h-8 w-8" onClick={onShowConfig} tooltip="Configuration">
                <SlidersHorizontal className="h-4 w-4" />
            </Button>
        );
    }
    if (selectedExecution) {
        if (hasChatOutput(selectedExecution)) {
            buttons.push(<ViewChatOutputButton key="view-chat" execution={selectedExecution} isMobile={isMobile} onGoToChat={onGoToChat} />);
        }
        buttons.push(<ExecutionActionsMenu key="actions" deleteDisabled={IN_PROGRESS_STATUSES.has(selectedExecution.status)} onDelete={() => onDeleteExecution(selectedExecution)} />);
    }
    return buttons.length > 0 ? buttons : undefined;
}
