// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import type { TaskExecution } from "@/types/scheduled-tasks";

import { formatExecutionLabel } from "@/features/scheduledTasks/executionHistory/helpers";

import { ConfirmationDialog } from "@/common";

interface DeleteExecutionDialogProps {
    execution: TaskExecution | null;
    taskName: string;
    isLoading: boolean;
    onCancel: () => void;
    onConfirm: (execution: TaskExecution) => Promise<void>;
}

export const DeleteExecutionDialog: React.FC<DeleteExecutionDialogProps> = ({ execution, taskName, isLoading, onCancel, onConfirm }) => (
    <ConfirmationDialog
        open={!!execution}
        onOpenChange={open => {
            if (!open) onCancel();
        }}
        title={execution ? `Delete ${formatExecutionLabel(execution)}` : ""}
        content={
            execution ? (
                <p className="text-sm">
                    Deleting <strong>{formatExecutionLabel(execution)}</strong> will remove it from the <strong>{taskName}</strong> execution history and will no longer be available to view.
                </p>
            ) : null
        }
        actionLabels={{ confirm: "Delete" }}
        isLoading={isLoading}
        onConfirm={async () => {
            if (!execution) return;
            await onConfirm(execution);
        }}
    />
);
