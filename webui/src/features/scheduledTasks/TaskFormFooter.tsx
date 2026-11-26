// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Loader2 } from "lucide-react";

import { Button } from "@/ui";

export type PendingAction = "create" | "create-activate" | "save" | null;

interface TaskFormFooterProps {
    isEditing: boolean;
    isLoading: boolean;
    pendingAction: PendingAction;
    onCancel: () => void;
    onSave: (activate?: boolean) => void;
}

const Spinner: React.FC<{ label: string }> = ({ label }) => (
    <>
        <Loader2 className="mr-2 h-4 w-4 animate-spin" />
        {label}
    </>
);

export const TaskFormFooter: React.FC<TaskFormFooterProps> = ({ isEditing, isLoading, pendingAction, onCancel, onSave }) => {
    const label = (action: Exclude<PendingAction, null>, busy: string, idle: string) => (pendingAction === action ? <Spinner label={busy} /> : idle);

    return (
        <div className="flex flex-col gap-2 border-t p-4 sm:flex-row sm:justify-end">
            <Button variant="ghost" onClick={onCancel} disabled={isLoading}>
                {isEditing ? "Discard Changes" : "Cancel"}
            </Button>
            {isEditing ? (
                <Button onClick={() => onSave()} disabled={isLoading}>
                    {label("save", "Saving...", "Save")}
                </Button>
            ) : (
                <>
                    <Button variant="outline" onClick={() => onSave(false)} disabled={isLoading}>
                        {label("create", "Creating...", "Create")}
                    </Button>
                    <Button onClick={() => onSave(true)} disabled={isLoading}>
                        {label("create-activate", "Creating...", "Create and Activate")}
                    </Button>
                </>
            )}
        </div>
    );
};
