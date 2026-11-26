// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { ConfirmationDialog } from "@/common";

interface Props {
    open: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}

export function SwitchToBuildWhileTestingDialog({ open, onConfirm, onCancel }: Props) {
    return (
        <ConfirmationDialog
            open={open}
            title="Switch to Build Mode"
            description="There is a test in progress. Switching to Build mode will stop the current test."
            actionLabels={{ confirm: "Switch to Build Mode" }}
            onOpenChange={isOpen => !isOpen && onCancel()}
            onConfirm={onConfirm}
            onCancel={onCancel}
        />
    );
}
