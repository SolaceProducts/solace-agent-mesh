// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useState } from "react";

import { Input } from "@/ui/input";

import { ConfirmationDialog, MessageBanner } from "@/common";

interface TypedDeleteConfirmationDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly title: string;
    readonly primaryMessage: ReactNode;
    readonly warningMessage?: ReactNode;
    readonly confirmLabel?: ReactNode;
    readonly deleting: boolean;
    readonly error: string | null;
    readonly onConfirm: () => void | Promise<void>;
}

const DEFAULT_CONFIRM_LABEL = (
    <>
        Type <strong>DELETE</strong> to confirm
    </>
);

export function TypedDeleteConfirmationDialog({ open, onOpenChange, title, primaryMessage, warningMessage, confirmLabel = DEFAULT_CONFIRM_LABEL, deleting, error, onConfirm }: TypedDeleteConfirmationDialogProps) {
    const [confirmText, setConfirmText] = useState("");

    useEffect(() => {
        if (!open) setConfirmText("");
    }, [open]);

    return (
        <ConfirmationDialog
            title={title}
            open={open}
            onOpenChange={onOpenChange}
            actionLabels={{ confirm: "Delete" }}
            isEnabled={confirmText === "DELETE"}
            isLoading={deleting}
            onConfirm={onConfirm}
            content={
                <div className="flex flex-col gap-4">
                    <p className="text-sm text-(--primary-text-wMain)">{primaryMessage}</p>
                    {warningMessage && <MessageBanner variant="warning" message={warningMessage} />}
                    {error && <MessageBanner variant="error" message={error} />}
                    <div className="flex flex-col gap-2">
                        <label className="text-sm font-medium">{confirmLabel}</label>
                        <Input value={confirmText} onChange={e => setConfirmText(e.target.value)} disabled={deleting} autoComplete="off" />
                    </div>
                </div>
            }
        />
    );
}
