// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/ui/dialog";

const SKIP_CONFIRM_KEY = "sam_builder_skip_apply_changes_confirm";

export function shouldSkipApplyChangesConfirm(): boolean {
    try {
        return localStorage.getItem(SKIP_CONFIRM_KEY) === "true";
    } catch {
        return false;
    }
}

interface Props {
    open: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}

export function ApplyChangesDialog({ open, onConfirm, onCancel }: Props) {
    const [dontShowAgain, setDontShowAgain] = useState(false);

    return (
        <Dialog open={open} onOpenChange={isOpen => !isOpen && onCancel()}>
            <DialogContent className="!max-w-md bg-(--background-w10) p-6" onInteractOutside={() => onCancel()}>
                <DialogTitle className="leading-normal">Apply Changes</DialogTitle>
                <DialogDescription className="mt-3 text-sm leading-relaxed">
                    This will switch to Build Mode and apply the recommended changes to your components. Your test history will be preserved for <span className="font-medium">up to 3 days</span> after leaving Test Mode.
                </DialogDescription>
                <label className="mt-4 flex items-center gap-2 text-sm">
                    <Checkbox checked={dontShowAgain} onCheckedChange={setDontShowAgain} />
                    Do not show this again
                </label>
                <div className="mt-5 flex justify-end gap-2">
                    <Button size="sm" variant="ghost" onClick={onCancel}>
                        Cancel
                    </Button>
                    <Button
                        size="sm"
                        onClick={() => {
                            if (dontShowAgain) {
                                try {
                                    localStorage.setItem(SKIP_CONFIRM_KEY, "true");
                                } catch {}
                            }
                            onConfirm();
                        }}
                    >
                        Apply Changes
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
