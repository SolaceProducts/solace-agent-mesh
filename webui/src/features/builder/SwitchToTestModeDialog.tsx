// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { Checkbox } from "@/ui/checkbox";

import { ConfirmationDialog } from "@/common";

const SKIP_CONFIRM_KEY = "sam_builder_skip_test_mode_confirm";

export function shouldSkipTestModeConfirm(): boolean {
    try {
        return localStorage.getItem(SKIP_CONFIRM_KEY) === "true";
    } catch {
        return false;
    }
}

interface SwitchToTestModeDialogProps {
    open: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}

export function SwitchToTestModeDialog({ open, onConfirm, onCancel }: SwitchToTestModeDialogProps) {
    const [dontShowAgain, setDontShowAgain] = useState(false);

    const handleConfirm = () => {
        if (dontShowAgain) {
            try {
                localStorage.setItem(SKIP_CONFIRM_KEY, "true");
            } catch {}
        }
        onConfirm();
    };

    return (
        <ConfirmationDialog
            open={open}
            title="Switch to Test Mode"
            description="This will switch you into Test mode. Your building conversation history will be preserved and you can switch back into Build mode at any time."
            content={
                <label className="flex items-center gap-2 pt-4 text-sm">
                    <Checkbox checked={dontShowAgain} onCheckedChange={setDontShowAgain} />
                    Do not show this again
                </label>
            }
            actionLabels={{ confirm: "Switch to Test Mode" }}
            onOpenChange={isOpen => !isOpen && onCancel()}
            onConfirm={handleConfirm}
        />
    );
}
