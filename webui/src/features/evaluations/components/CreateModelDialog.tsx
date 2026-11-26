// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { modelsApi } from "@/api/models";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";

import { ModelEdit } from "@/features/models/ModelEdit";
import { buildModelPayload, CHAT_PROVIDERS, type ModelFormData } from "@/features/models/modelProviderUtils";

import { MessageBanner } from "@/common";

interface CreateModelDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly onCreated: (createdModelId: string) => void;
}

export function CreateModelDialog({ open, onOpenChange, onCreated }: CreateModelDialogProps) {
    const [isSaving, setIsSaving] = useState(false);
    const [errorMessage, setErrorMessage] = useState<string | null>(null);

    useEffect(() => {
        if (open) setErrorMessage(null);
    }, [open]);

    const handleSave = async (data: ModelFormData, dirtyFields?: Partial<Record<string, boolean>>) => {
        setIsSaving(true);
        setErrorMessage(null);
        try {
            const payload = buildModelPayload(data, dirtyFields);
            const created = await modelsApi.create(payload);
            onOpenChange(false);
            onCreated(created.id);
        } catch (error) {
            setErrorMessage(getErrorMessage(error, "An error occurred while creating the model."));
        } finally {
            setIsSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="flex !max-h-[95vh] !max-w-3xl flex-col gap-0 p-0 outline-none">
                <DialogHeader className="border-b px-6 py-4">
                    <DialogTitle>Create Model</DialogTitle>
                </DialogHeader>

                <div className="flex-1 overflow-auto px-6 pt-5 pb-24">
                    {errorMessage && (
                        <div className="mb-4">
                            <MessageBanner variant="error" message={errorMessage} dismissible onDismiss={() => setErrorMessage(null)} />
                        </div>
                    )}

                    <ModelEdit isNew modelToEdit={null} onSave={handleSave} availableProviders={CHAT_PROVIDERS} />
                </div>

                <div className="flex items-center justify-end gap-3 border-t px-6 py-4">
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={isSaving}>
                        Cancel
                    </Button>
                    <Button type="submit" form="model-form" disabled={isSaving}>
                        {isSaving ? "Creating..." : "Create"}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
