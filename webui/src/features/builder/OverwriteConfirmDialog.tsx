// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { AlertTriangle } from "lucide-react";

import { Button } from "@/ui/button";
import { Checkbox } from "@/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import { persistSkipOverwriteConfirm } from "./overwriteConfirmPreference";
import type { PlannedOverwrite } from "./serverApply";

interface OverwriteConfirmDialogProps {
    overwrites: PlannedOverwrite[] | null;
    onConfirm: () => void;
    onCancel: () => void;
}

export function OverwriteConfirmDialog({ overwrites, onConfirm, onCancel }: Readonly<OverwriteConfirmDialogProps>) {
    const [dontShowAgain, setDontShowAgain] = useState(false);
    const items = overwrites ?? [];

    const cancel = () => {
        setDontShowAgain(false);
        onCancel();
    };
    const confirm = () => {
        if (dontShowAgain) persistSkipOverwriteConfirm();
        setDontShowAgain(false);
        onConfirm();
    };

    return (
        <Dialog open={items.length > 0} onOpenChange={open => !open && cancel()}>
            <DialogContent className="bg-(--background-w10) p-6 sm:max-w-lg" onInteractOutside={cancel}>
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2">
                        <AlertTriangle className="h-5 w-5 text-(--warning-wMain)" />
                        Replace existing {items.length === 1 ? "component" : "components"}?
                    </DialogTitle>
                    <DialogDescription>
                        {items.length === 1 ? "This component already exists" : "These components already exist"} on the platform. Saving replaces {items.length === 1 ? "it" : "them"} with the Builder&apos;s version, including any changes made
                        outside the Builder.
                    </DialogDescription>
                </DialogHeader>
                <ul className="mt-2 max-h-60 space-y-2 overflow-y-auto text-sm">
                    {items.map(item => (
                        <li key={`${item.kind}:${item.name}`} className="rounded-md border border-(--secondary-w20) p-2">
                            <div className="font-medium">
                                {item.name} <span className="text-xs font-normal text-(--secondary-text-wMain)">({item.kind})</span>
                            </div>
                            <div className="text-xs text-(--secondary-text-wMain)">{item.changedFields.length > 0 ? `Changes: ${item.changedFields.join(", ")}` : "No field changes reported"}</div>
                        </li>
                    ))}
                </ul>
                <label className="mt-3 flex items-center gap-2 text-sm">
                    <Checkbox checked={dontShowAgain} onCheckedChange={checked => setDontShowAgain(checked === true)} />
                    Do not show me this again
                </label>
                <DialogFooter className="mt-4">
                    <Button size="sm" variant="ghost" onClick={cancel}>
                        Cancel
                    </Button>
                    <Button size="sm" onClick={confirm}>
                        Replace and save
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
