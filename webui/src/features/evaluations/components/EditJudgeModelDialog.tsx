// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { useUpdateEvaluator } from "@/api/evaluations";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { ConfirmationDialog } from "@/common/dialogs/ConfirmationDialog";
import { PageLabel } from "@/common/layout";

import type { EvalEvaluator } from "@/types/eval";

import { DEFAULT_JUDGE_MODEL_LABEL } from "./evaluatorForm";
import { ModelSelect } from "./ModelSelect";

interface EditJudgeModelDialogProps {
    readonly evaluator: EvalEvaluator;
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly onSaved?: (updated: EvalEvaluator) => void;
}

export function EditJudgeModelDialog({ evaluator, open, onOpenChange, onSaved }: EditJudgeModelDialogProps) {
    const updateEvaluator = useUpdateEvaluator();
    const [model, setModel] = useState(evaluator.model ?? "");
    const [saveError, setSaveError] = useState<string | null>(null);

    const handleOpenChange = (next: boolean) => {
        if (!next) updateEvaluator.reset();
        onOpenChange(next);
    };

    const onSave = async () => {
        setSaveError(null);
        try {
            const updated = await updateEvaluator.mutateAsync({ id: evaluator.id, data: { model: model.trim() || null } });
            onSaved?.(updated);
        } catch (e) {
            setSaveError(e instanceof Error ? e.message : "Failed to update the evaluator's model.");
            throw e;
        }
    };

    return (
        <ConfirmationDialog
            open={open}
            onOpenChange={handleOpenChange}
            title="Edit Model"
            description={
                <>
                    Choose the model that <strong>{evaluator.name}</strong> uses to grade responses. With no model selected, it uses the general model.
                </>
            }
            actionLabels={{ confirm: "Save" }}
            isLoading={updateEvaluator.isPending}
            onConfirm={onSave}
            content={
                <div className="flex flex-col gap-2 py-2">
                    {saveError && <MessageBanner variant="error" message={saveError} />}
                    <PageLabel>Model</PageLabel>
                    <ModelSelect value={model} onChange={setModel} disabled={updateEvaluator.isPending} hideLabel placeholder={DEFAULT_JUDGE_MODEL_LABEL} />
                </div>
            }
        />
    );
}
