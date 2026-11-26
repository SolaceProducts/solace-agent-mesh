// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { TypedDeleteConfirmationDialog } from "@/features/evaluations/components";

const SESSION_HISTORY_WARNING = "The agent's session history won't be deleted, but it will be disassociated from this agent and no longer accessible through it.";

interface DeleteAgentConfirmationDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly agentName: string;
    readonly onConfirm: () => void | Promise<void>;
    readonly needsCleanup?: boolean;
    readonly title?: string;
}

export function DeleteAgentConfirmationDialog({ open, onOpenChange, agentName, onConfirm, needsCleanup = false, title = "Confirm Agent Deletion" }: DeleteAgentConfirmationDialogProps) {
    const [isDeleting, setIsDeleting] = useState(false);

    const primaryMessage = (
        <span>
            Are you sure you want to delete the agent <span className="font-semibold">{agentName}</span>?{needsCleanup ? " A cleanup will be attempted to remove any remaining deployment resources." : ""} This action cannot be undone.
        </span>
    );

    return (
        <TypedDeleteConfirmationDialog
            title={title}
            primaryMessage={primaryMessage}
            warningMessage={SESSION_HISTORY_WARNING}
            open={open}
            onOpenChange={onOpenChange}
            deleting={isDeleting}
            error={null}
            onConfirm={async () => {
                setIsDeleting(true);
                try {
                    await onConfirm();
                    onOpenChange(false);
                } finally {
                    setIsDeleting(false);
                }
            }}
        />
    );
}
