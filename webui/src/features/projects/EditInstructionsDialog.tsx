// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useState } from "react";

import { useConfigContext } from "@/providers/contexts";

import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";
import type { Project } from "@/types/projects";

import { MessageBanner } from "@/common";
import { Button, Textarea } from "@/ui";

import { DEFAULT_MAX_INSTRUCTIONS_LENGTH } from "./validation";

interface EditInstructionsDialogProps {
    isOpen: boolean;
    onClose: () => void;
    onSave: (systemPrompt: string) => Promise<void>;
    project: Project;
    isSaving: boolean;
    error?: string | null;
}

export const EditInstructionsDialog: React.FC<EditInstructionsDialogProps> = ({ isOpen, onClose, onSave, project, isSaving, error }) => {
    const { validationLimits } = useConfigContext();
    const advertisedMax = validationLimits?.projectInstructionsMax;
    const MAX_INSTRUCTIONS_LENGTH = advertisedMax && advertisedMax > 0 ? advertisedMax : DEFAULT_MAX_INSTRUCTIONS_LENGTH;

    const [editedPrompt, setEditedPrompt] = useState(project.systemPrompt || "");
    const [localError, setLocalError] = useState<string | null>(null);

    useEffect(() => {
        if (isOpen) {
            setEditedPrompt(project.systemPrompt || "");
            setLocalError(null);
        }
    }, [isOpen, project.systemPrompt]);

    useEffect(() => {
        if (error) {
            setLocalError(error);
        }
    }, [error]);

    const handleSave = async () => {
        setLocalError(null);

        if (editedPrompt.trim() !== (project.systemPrompt || "")) {
            try {
                await onSave(editedPrompt.trim());
                onClose();
            } catch {}
        } else {
            onClose();
        }
    };

    const handleCancel = () => {
        setEditedPrompt(project.systemPrompt || "");
        setLocalError(null);
        onClose();
    };

    const characterCount = editedPrompt.length;
    const trimmedLength = editedPrompt.trim().length;
    const isOverLimit = trimmedLength > MAX_INSTRUCTIONS_LENGTH;
    const counterId = "project-instructions-counter";

    return (
        <Dialog open={isOpen} onOpenChange={open => !open && handleCancel()}>
            <DialogContent className="flex max-h-[80vh] max-w-3xl flex-col">
                <DialogHeader>
                    <DialogTitle>Edit Project Instructions</DialogTitle>
                    <DialogDescription>Provide instructions that will guide the AI when working with this project.</DialogDescription>
                </DialogHeader>

                <div className="min-h-0 flex-1 pt-4">
                    <div className="relative">
                        <Textarea
                            value={editedPrompt}
                            onChange={e => setEditedPrompt(e.target.value)}
                            placeholder="Add instructions for this project..."
                            rows={15}
                            disabled={isSaving}
                            aria-invalid={isOverLimit}
                            aria-describedby={counterId}
                            className="resize-none text-sm"
                        />
                        <div id={counterId} role={isOverLimit ? "alert" : undefined} className={`text-xs ${isOverLimit ? "text-(--error-wMain)" : "text-right text-(--secondary-text-wMain)"}`}>
                            {isOverLimit && `Instructions must be ${MAX_INSTRUCTIONS_LENGTH} characters or fewer (currently ${characterCount})`}
                            {!isOverLimit && `${characterCount} / ${MAX_INSTRUCTIONS_LENGTH}`}
                        </div>
                    </div>
                    {localError && <MessageBanner variant="error" message={localError} className="py-3" />}
                </div>

                <DialogFooter>
                    <Button variant="outline" onClick={handleCancel} disabled={isSaving}>
                        Discard Changes
                    </Button>
                    <Button onClick={handleSave} disabled={isSaving || isOverLimit}>
                        Save
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};
