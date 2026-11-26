// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { evaluationsApi } from "@/api/evaluations";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";

import { MessageBanner } from "@/common";

import { useDefaultModelSelection } from "../hooks/useDefaultModelSelection";

import { buildCreateEvaluatorRequest, emptyEvaluatorFormValue, type EvaluatorFieldErrors, type EvaluatorFormValue, isEvaluatorFormComplete, preSaveErrorState, saveErrorState } from "./evaluatorForm";
import { EvaluatorFormBody } from "./EvaluatorFormBody";

interface CreateEvaluatorDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly onCreated: (createdEvaluatorId: string) => void;
}

export function CreateEvaluatorDialog({ open, onOpenChange, onCreated }: CreateEvaluatorDialogProps) {
    const [value, setValue] = useState<EvaluatorFormValue>(emptyEvaluatorFormValue());
    const [isSaving, setIsSaving] = useState(false);
    const [errorMessage, setErrorMessage] = useState<string | null>(null);
    const [fieldErrors, setFieldErrors] = useState<EvaluatorFieldErrors>({});

    useEffect(() => {
        if (open) {
            setValue(emptyEvaluatorFormValue());
            setErrorMessage(null);
            setFieldErrors({});
        }
    }, [open]);

    useDefaultModelSelection(open, value.model, id => setValue(v => ({ ...v, model: id })));

    const isValid = isEvaluatorFormComplete(value);

    const showError = (state: { fieldErrors: EvaluatorFieldErrors; message: string }) => {
        setFieldErrors(state.fieldErrors);
        setErrorMessage(state.message);
    };

    const handleCreate = async () => {
        const refused = preSaveErrorState(value);
        if (refused) {
            showError(refused);
            return;
        }

        setIsSaving(true);
        setErrorMessage(null);
        setFieldErrors({});
        try {
            const created = await evaluationsApi.createEvaluator(buildCreateEvaluatorRequest(value));
            onOpenChange(false);
            onCreated(created.id);
        } catch (error) {
            showError(saveErrorState(error, value.type, "An error occurred while creating the evaluator."));
        } finally {
            setIsSaving(false);
        }
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="flex !max-h-[95vh] !max-w-3xl flex-col gap-0 p-0 outline-none">
                <DialogHeader className="border-b px-6 py-4">
                    <DialogTitle>Create Evaluator</DialogTitle>
                </DialogHeader>

                <div className="flex-1 overflow-auto px-6 pt-5 pb-6">
                    {errorMessage && (
                        <div className="mb-4">
                            <MessageBanner variant="error" message={errorMessage} dismissible onDismiss={() => setErrorMessage(null)} />
                        </div>
                    )}

                    <EvaluatorFormBody value={value} onChange={setValue} disabled={isSaving} variant="dialog" fieldErrors={fieldErrors} typeEditable />
                </div>

                <div className="flex items-center justify-end gap-3 border-t px-6 py-4">
                    <Button variant="outline" onClick={() => onOpenChange(false)} disabled={isSaving}>
                        Cancel
                    </Button>
                    <Button onClick={() => void handleCreate()} disabled={isSaving || !isValid}>
                        {isSaving ? "Creating..." : "Create"}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
