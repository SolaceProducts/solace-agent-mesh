// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { usePromptShares } from "@/api/prompt-sharing";

import { ConfirmationDialog } from "@/common";
import { useIsPromptSharingEnabled, useIsPromptVersionHistoryEnabled } from "@/hooks";

interface PromptDeleteDialogProps {
    isOpen: boolean;
    onClose: () => void;
    onConfirm: () => void;
    promptId: string;
    promptName: string;
}

export const PromptDeleteDialog = React.memo<PromptDeleteDialogProps>(({ isOpen, onClose, onConfirm, promptId, promptName }) => {
    const sharingEnabled = useIsPromptSharingEnabled();
    const versionHistoryEnabled = useIsPromptVersionHistoryEnabled();
    const { data: sharesData } = usePromptShares(promptId, { enabled: sharingEnabled && isOpen });
    const recipientCount = sharesData?.data?.length ?? 0;

    return (
        <ConfirmationDialog
            title="Delete Prompt"
            content={
                <div className="space-y-2">
                    <p>
                        This action cannot be undone. This will permanently delete {versionHistoryEnabled ? "the prompt and all its versions" : "the prompt"}: <strong>{promptName}</strong>.
                    </p>
                    {recipientCount > 0 && <p>{`This prompt is shared with ${recipientCount} ${recipientCount === 1 ? "person" : "people"}. They will lose access.`}</p>}
                </div>
            }
            actionLabels={{ confirm: "Delete" }}
            open={isOpen}
            onOpenChange={open => !open && onClose()}
            onConfirm={onConfirm}
            onCancel={onClose}
        />
    );
});
