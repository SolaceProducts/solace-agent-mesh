// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { TypedDeleteConfirmationDialog } from "@/features/evaluations/components";

interface DeleteWorkflowConfirmationDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly workflowName: string;
    readonly onConfirm: () => void | Promise<void>;
}

export function DeleteWorkflowConfirmationDialog({ open, onOpenChange, workflowName, onConfirm }: DeleteWorkflowConfirmationDialogProps) {
    const [isDeleting, setIsDeleting] = useState(false);

    const primaryMessage = (
        <span>
            Are you sure you want to delete the workflow <span className="font-semibold">{workflowName}</span>? If it's currently deployed it will be undeployed first. This action cannot be undone.
        </span>
    );

    return (
        <TypedDeleteConfirmationDialog
            title="Confirm Workflow Deletion"
            primaryMessage={primaryMessage}
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
