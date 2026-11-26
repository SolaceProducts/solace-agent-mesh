// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect } from "react";
import { useForm } from "react-hook-form";

import { useConfigContext } from "@/providers/contexts";

import { Form, FormControl, FormError, FormField, FormInputLabel, FormItem } from "@/ui/form";

import { DEFAULT_MAX_FILE_DESCRIPTION_LENGTH } from "@/features/projects/validation";

import { ConfirmationDialog, MessageBanner } from "@/common";
import type { ArtifactInfo } from "@/types";
import { Textarea } from "@/ui";

import { FileLabel } from "../chat/file/FileLabel";

interface EditFileDescriptionDialogProps {
    isOpen: boolean;
    artifact: ArtifactInfo | null;
    onClose: () => void;
    onSave: (description: string) => Promise<void>;
    isSaving?: boolean;
}

export const EditFileDescriptionDialog: React.FC<EditFileDescriptionDialogProps> = ({ isOpen, artifact, onClose, onSave, isSaving = false }) => {
    const { validationLimits } = useConfigContext();
    const FILE_DESCRIPTION_MAX = validationLimits?.projectFileDescriptionMax ?? DEFAULT_MAX_FILE_DESCRIPTION_LENGTH;

    const form = useForm<{ description: string }>({
        mode: "onChange",
        defaultValues: {
            description: "",
        },
    });

    useEffect(() => {
        if (isOpen && artifact) {
            form.reset({ description: artifact.description || "" });
        }
    }, [isOpen, artifact, form]);

    const currentDescription = form.watch("description");

    const initialLength = artifact?.description?.length || 0;
    const wasOverLimit = initialLength > FILE_DESCRIPTION_MAX;

    const hasErrors = Object.keys(form.formState.errors).length > 0;

    const handleSave = async () => {
        const isValid = await form.trigger();
        if (!isValid) return;

        const { description } = form.getValues();
        await onSave(description.trim());
    };

    const handleCancel = () => {
        form.reset({ description: artifact?.description || "" });
        onClose();
    };

    if (!artifact) return null;

    const dialogContent = (
        <div className="my-5 flex flex-col gap-4">
            {wasOverLimit && <MessageBanner variant="warning" message={`This file's description (${initialLength} characters) exceeds the current limit of ${FILE_DESCRIPTION_MAX} characters. Please reduce it to save changes.`} />}

            <FileLabel fileName={artifact.filename} fileSize={artifact.size} />

            <Form {...form}>
                <FormField
                    control={form.control}
                    name="description"
                    rules={{
                        maxLength: {
                            value: FILE_DESCRIPTION_MAX,
                            message: `Description exceeds the maximum of ${FILE_DESCRIPTION_MAX} characters`,
                        },
                    }}
                    render={({ field, fieldState }) => (
                        <FormItem>
                            <FormControl>
                                <Textarea {...field} rows={2} disabled={isSaving} maxLength={FILE_DESCRIPTION_MAX + 1} className="mt-1 resize-none text-sm" />
                            </FormControl>
                            <FormError />
                            {!fieldState.error && (
                                <FormInputLabel rightAlign>
                                    {currentDescription.length} / {FILE_DESCRIPTION_MAX}
                                </FormInputLabel>
                            )}
                        </FormItem>
                    )}
                />
            </Form>
        </div>
    );

    return (
        <ConfirmationDialog
            open={isOpen}
            onOpenChange={open => !open && onClose()}
            title="Edit Project File Description"
            description="Update the description to help Solace Agent Mesh understand its purpose."
            content={dialogContent}
            actionLabels={{
                cancel: "Discard Changes",
                confirm: "Save",
            }}
            isLoading={isSaving}
            isEnabled={!hasErrors}
            onConfirm={handleSave}
            onCancel={handleCancel}
        />
    );
};
