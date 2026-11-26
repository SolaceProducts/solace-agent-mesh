// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";

import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import { ProjectDetailsFields } from "@/features/projects/ProjectDetailsFields";
import { isProjectTextOverLimit, useProjectFieldLimits, validateProjectName } from "@/features/projects/validation";

import { MessageBanner } from "@/common";
import { Button } from "@/ui";
import { getErrorMessage } from "@/utils";

const DUPLICATE_NAME_ERROR = "A project with this name already exists";

interface CreateProjectDialogProps {
    isOpen: boolean;
    onClose: () => void;
    onSubmit: (data: { name: string; description: string }) => Promise<void>;
    isSubmitting?: boolean;
    existingNames?: string[];
}

export const CreateProjectDialog: React.FC<CreateProjectDialogProps> = ({ isOpen, onClose, onSubmit, isSubmitting = false, existingNames = [] }) => {
    const { maxNameLength, maxDescriptionLength } = useProjectFieldLimits();
    const [name, setName] = useState("");
    const [description, setDescription] = useState("");
    const [error, setError] = useState<string | null>(null);
    const [nameError, setNameError] = useState<string | null>(null);

    const isDescriptionOverLimit = isProjectTextOverLimit(description, maxDescriptionLength);

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();

        const trimmedName = name.trim();
        const validationError = validateProjectName(trimmedName);
        if (validationError) {
            setNameError(validationError);
            return;
        }
        if (existingNames.some(existing => existing.toLowerCase() === trimmedName.toLowerCase())) {
            setNameError(DUPLICATE_NAME_ERROR);
            return;
        }
        setNameError(null);

        try {
            await onSubmit({ name: trimmedName, description: description.trim() });
            setName("");
            setDescription("");
            setError(null);
        } catch (err) {
            const status = (err as { status?: number })?.status;
            if (status === 409) {
                setNameError(DUPLICATE_NAME_ERROR);
                return;
            }
            setError(getErrorMessage(err, "Failed to create project"));
        }
    };

    const handleClose = () => {
        if (!isSubmitting) {
            setName("");
            setDescription("");
            setNameError(null);
            setError(null);
            onClose();
        }
    };

    return (
        <Dialog open={isOpen} onOpenChange={open => !open && handleClose()}>
            <DialogContent className="sm:max-w-[500px]">
                <form onSubmit={handleSubmit}>
                    <DialogHeader>
                        <DialogTitle>Create New Project</DialogTitle>
                        <DialogDescription>Create a new project to organize your chats and files. You can add more details after creation.</DialogDescription>
                    </DialogHeader>

                    <ProjectDetailsFields
                        idPrefix="project"
                        name={name}
                        onNameChange={value => {
                            setName(value);
                            setNameError(null);
                        }}
                        nameError={nameError}
                        description={description}
                        onDescriptionChange={setDescription}
                        maxNameLength={maxNameLength}
                        maxDescriptionLength={maxDescriptionLength}
                        isDescriptionOverLimit={isDescriptionOverLimit}
                        disabled={isSubmitting}
                        header={error ? <MessageBanner variant="error" message={error} /> : null}
                    />

                    <DialogFooter>
                        <Button type="button" variant="ghost" onClick={handleClose} disabled={isSubmitting}>
                            Cancel
                        </Button>
                        <Button type="submit" disabled={isSubmitting || isDescriptionOverLimit}>
                            Create Project
                        </Button>
                    </DialogFooter>
                </form>
            </DialogContent>
        </Dialog>
    );
};
