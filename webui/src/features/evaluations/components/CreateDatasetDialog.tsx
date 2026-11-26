// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { evaluationsApi, useDeleteDataset, useUpdateDataset } from "@/api/evaluations";

import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/ui/dialog";
import type { EvalDataset } from "@/types/eval";

import { ConfirmationDialog, EmptyState, MessageBanner } from "@/common";

import { createUntitledDataset } from "../utils";

import { DatasetFormBody } from "./DatasetFormBody";

interface CreateDatasetDialogProps {
    readonly open: boolean;
    readonly onOpenChange: (open: boolean) => void;
    readonly onCreated: (datasetId: string) => void;
    readonly existingDatasetId?: string;
}

export function CreateDatasetDialog({ open, onOpenChange, onCreated, existingDatasetId }: CreateDatasetDialogProps) {
    const [dataset, setDataset] = useState<EvalDataset | null>(null);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [name, setName] = useState("");
    const [description, setDescription] = useState("");
    const [confirmDiscard, setConfirmDiscard] = useState(false);
    const [saving, setSaving] = useState(false);

    const updateDatasetMutation = useUpdateDataset();
    const deleteDatasetMutation = useDeleteDataset();

    useEffect(() => {
        if (!open) {
            setDataset(null);
            setName("");
            setDescription("");
            setError(null);
            setConfirmDiscard(false);
            return;
        }
        if (dataset) return;
        let cancelled = false;
        setLoading(true);
        setError(null);
        const prepare = existingDatasetId ? evaluationsApi.getDataset(existingDatasetId) : createUntitledDataset(evaluationsApi);
        prepare
            .then(ds => {
                if (cancelled) {
                    if (!existingDatasetId) {
                        void deleteDatasetMutation.mutateAsync(ds.id).catch(() => {});
                    }
                    return;
                }
                setDataset(ds);
                setName(existingDatasetId ? ds.name : "");
                setDescription(existingDatasetId ? (ds.description ?? "") : "");
            })
            .catch(err => {
                if (!cancelled) setError(getErrorMessage(err));
            })
            .finally(() => {
                if (!cancelled) setLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [open, dataset, existingDatasetId]);

    const handleCreate = async () => {
        if (!dataset) return;
        const trimmedName = name.trim();
        const trimmedDesc = description.trim();
        if (!trimmedName || !trimmedDesc) return;

        setSaving(true);
        setError(null);
        try {
            const payload: { name?: string; description?: string } = {};
            if (trimmedName !== dataset.name) payload.name = trimmedName;
            if (trimmedDesc !== (dataset.description ?? "")) payload.description = trimmedDesc;
            if (Object.keys(payload).length > 0) {
                await updateDatasetMutation.mutateAsync({ id: dataset.id, data: payload });
            }
            const createdId = dataset.id;
            onOpenChange(false);
            onCreated(createdId);
        } catch (err) {
            setError(getErrorMessage(err));
        } finally {
            setSaving(false);
        }
    };

    const requestDiscard = () => {
        if (!dataset) {
            onOpenChange(false);
            return;
        }
        setConfirmDiscard(true);
    };

    const confirmDiscardDataset = async () => {
        if (dataset) {
            try {
                await deleteDatasetMutation.mutateAsync(dataset.id);
            } catch {}
        }
        setConfirmDiscard(false);
        deferPanelClose(() => onOpenChange(false));
    };

    const isValid = !!dataset && !!name.trim() && !!description.trim();

    return (
        <>
            <Dialog
                open={open}
                onOpenChange={next => {
                    if (!next) requestDiscard();
                    else onOpenChange(true);
                }}
            >
                <DialogContent className="flex !max-h-[95vh] !max-w-5xl flex-col gap-0 p-0 outline-none">
                    <DialogHeader className="border-b px-6 py-4">
                        <DialogTitle>Create Dataset</DialogTitle>
                    </DialogHeader>

                    <div className="flex-1 overflow-auto px-6 pt-5 pb-6">
                        {error && (
                            <div className="mb-4">
                                <MessageBanner variant="error" message={error} dismissible onDismiss={() => setError(null)} />
                            </div>
                        )}

                        {loading || !dataset ? (
                            <EmptyState variant="loading" title="Preparing dataset..." />
                        ) : (
                            <DatasetFormBody datasetId={dataset.id} dataset={dataset} onDatasetChange={setDataset} name={name} onNameChange={setName} description={description} onDescriptionChange={setDescription} autosaveFields={false} />
                        )}
                    </div>

                    <div className="flex items-center justify-end gap-3 border-t px-6 py-4">
                        <Button variant="outline" onClick={requestDiscard} disabled={saving}>
                            Cancel
                        </Button>
                        <Button onClick={() => void handleCreate()} disabled={saving || !isValid}>
                            {saving ? "Creating..." : "Create"}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>

            <ConfirmationDialog
                title="Discard Dataset"
                content="Discard this dataset? All name, description, and example edits will be lost."
                open={confirmDiscard}
                onOpenChange={o => {
                    if (!o) setConfirmDiscard(false);
                }}
                onCancel={() => setConfirmDiscard(false)}
                onConfirm={() => void confirmDiscardDataset()}
            />
        </>
    );
}
