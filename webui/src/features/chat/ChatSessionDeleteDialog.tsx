// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { ConfirmationDialog, type ConfirmationDialogProps } from "@/common";

export interface ChatSessionDeleteDialogProps extends Omit<ConfirmationDialogProps, "title" | "content" | "onOpenChange"> {
    sessionName: string;
    onCancel: () => void;
}

export const ChatSessionDeleteDialog = React.memo<ChatSessionDeleteDialogProps>(({ open, onCancel, onConfirm, sessionName }) => {
    return (
        <ConfirmationDialog
            open={open}
            onOpenChange={open => !open && onCancel()}
            title="Delete Chat"
            content={
                <>
                    This action cannot be undone. This chat session and any associated artifacts will be permanently deleted: <strong>{sessionName}</strong>
                </>
            }
            actionLabels={{
                confirm: "Delete",
            }}
            onConfirm={onConfirm}
        />
    );
});
