// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { ExternalLink } from "lucide-react";

import { ApiV2Error } from "@/api/common";
import { evaluationsApi } from "@/api/evaluations";

import { PageLabel, PageLabelWithValue } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";
import { uuid } from "@/utils/uuid";

import { Button } from "@/ui/button";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/ui/dialog";

import { FileUpload, MessageBanner } from "@/common";

import { EVAL_DOCS, MAX_EXAMPLES_PER_DATASET } from "../constants";

interface DatasetImportDialogProps {
    readonly open: boolean;
    readonly onClose: () => void;
    readonly onImported: (datasetId: string, datasetName: string) => void;
    readonly datasetId?: string;
    readonly currentExampleCount?: number;
}

interface PreviewState {
    rows: number;
    format: "csv" | "json";
}

function isNameConflict(err: unknown): boolean {
    if (err instanceof ApiV2Error) return err.status === 409;
    return err instanceof Error && err.message.includes("already exists");
}

function isImportRefusal(err: unknown): boolean {
    const apiError = err instanceof Error && !(err instanceof ApiV2Error) ? err.cause : err;
    return apiError instanceof ApiV2Error && apiError.status >= 400 && apiError.status < 500;
}

interface UnremovedDataset {
    id: string;
    name: string;
    fileBaseName: string;
}

type EvaluationsApi = typeof evaluationsApi;

interface ImportTarget {
    id: string;
    name: string;
}

async function resolveImportTarget(evalApi: EvaluationsApi, existingId: string | undefined, baseName: string, unremoved: UnremovedDataset | null): Promise<ImportTarget> {
    if (existingId) return { id: existingId, name: baseName };
    if (unremoved?.fileBaseName === baseName) return { id: unremoved.id, name: unremoved.name };
    try {
        return { id: (await evalApi.createDataset({ name: baseName, description: "" })).id, name: baseName };
    } catch (err) {
        if (!isNameConflict(err)) throw err;
        const name = `${baseName} (${uuid().slice(0, 8)})`;
        return { id: (await evalApi.createDataset({ name, description: "" })).id, name };
    }
}

async function removeRefusedDataset(evalApi: EvaluationsApi, target: ImportTarget, baseName: string): Promise<UnremovedDataset | null> {
    try {
        await evalApi.deleteDataset(target.id);
        return null;
    } catch (cleanupError) {
        console.warn(`eval: cleanup of dataset ${target.id} after a refused import failed`, cleanupError);
        return { id: target.id, name: target.name, fileBaseName: baseName };
    }
}

function countCsvRows(text: string): number {
    let inQuote = false;
    let rowsWithContent = 0;
    let currentRowHasContent = false;
    for (let i = 0; i < text.length; i++) {
        const c = text[i];
        if (c === '"') {
            if (inQuote && text[i + 1] === '"') {
                i++;
                continue;
            }
            inQuote = !inQuote;
            currentRowHasContent = true;
        } else if (c === "\n" && !inQuote) {
            if (currentRowHasContent) rowsWithContent++;
            currentRowHasContent = false;
        } else if (c !== "\r" && !currentRowHasContent && c.trim() !== "") {
            currentRowHasContent = true;
        }
    }
    if (currentRowHasContent) rowsWithContent++;
    return Math.max(0, rowsWithContent - 1);
}

export function DatasetImportDialog({ open, onClose, onImported, datasetId, currentExampleCount = 0 }: DatasetImportDialogProps) {
    const [files, setFiles] = useState<FileList | null>(null);
    const [fileText, setFileText] = useState<string | null>(null);
    const [importing, setImporting] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [preview, setPreview] = useState<PreviewState | null>(null);
    const [unremoved, setUnremoved] = useState<UnremovedDataset | null>(null);

    const isExistingDataset = !!datasetId;

    const reset = () => {
        setFiles(null);
        setFileText(null);
        setError(null);
        setPreview(null);
    };

    const handleFileChange = async (fileList: FileList | null) => {
        setFiles(fileList);
        setError(null);
        setPreview(null);
        setFileText(null);
        const file = fileList?.[0];
        if (!file) return;
        try {
            const text = await file.text();
            setFileText(text);
            const isJson = file.name.toLowerCase().endsWith(".json");
            let rows = 0;
            if (isJson) {
                const parsed = JSON.parse(text);
                if (!Array.isArray(parsed)) {
                    setError("JSON file must contain an array of example objects.");
                    return;
                }
                rows = parsed.length;
            } else {
                rows = countCsvRows(text);
            }
            setPreview({ rows, format: isJson ? "json" : "csv" });
        } catch (err) {
            setError(`Could not read file: ${String(err)}`);
        }
    };

    const handleFileValidate = (fileList: FileList) => {
        const name = fileList[0].name.toLowerCase();
        if (!name.endsWith(".csv") && !name.endsWith(".json")) {
            return { valid: false, error: "Only CSV and JSON files are accepted." };
        }
        return { valid: true };
    };

    const handleImport = async () => {
        const file = files?.[0];
        if (!file) return;
        setImporting(true);
        setError(null);
        try {
            const body = fileText ?? (await file.text());
            const contentType = file.name.toLowerCase().endsWith(".csv") ? "text/csv" : "application/json";
            const baseName = file.name.replace(/\.(csv|json)$/i, "");

            const target = await resolveImportTarget(evaluationsApi, datasetId, baseName, unremoved);
            const datasetName = isExistingDataset ? "" : target.name;

            try {
                await evaluationsApi.importExamples(target.id, body, contentType);
            } catch (err) {
                if (!isExistingDataset && isImportRefusal(err)) {
                    setUnremoved(await removeRefusedDataset(evaluationsApi, target, baseName));
                }
                throw err;
            }
            setUnremoved(null);
            reset();
            onImported(target.id, datasetName);
            onClose();
        } catch (err) {
            setError(getErrorMessage(err));
        } finally {
            setImporting(false);
        }
    };

    const remainingCapacity = Math.max(0, MAX_EXAMPLES_PER_DATASET - currentExampleCount);
    const willImport = preview ? Math.min(preview.rows, remainingCapacity) : 0;
    const willDrop = preview ? Math.max(0, preview.rows - remainingCapacity) : 0;
    const atCapacity = remainingCapacity === 0;

    const isImportDisabled = importing || !files?.length || atCapacity;

    return (
        <Dialog
            open={open}
            onOpenChange={v => {
                if (!v) {
                    reset();
                    onClose();
                }
            }}
        >
            <DialogContent className="sm:max-w-[750px]">
                <DialogHeader>
                    <DialogTitle>Import from File</DialogTitle>
                </DialogHeader>
                <div className="flex flex-col gap-4 py-2">
                    <p className="text-sm text-(--primary-text-wMain)">
                        Upload your dataset or an example from our catalog to get started.{" "}
                        <a href={EVAL_DOCS.IMPORTING_EXAMPLES} target="_blank" rel="noopener noreferrer" className="inline-flex items-center gap-1 font-bold text-(--primary-wMain) hover:underline">
                            Browse Examples
                            <ExternalLink className="size-3.5" />
                        </a>
                    </p>

                    <PageLabelWithValue>
                        <PageLabel required>Dataset</PageLabel>
                        <div className="[&_div.flex-1]:text-(--primary-text-wMain)">
                            <FileUpload name="dataset-file" accept=".csv,.json" value={files} onChange={fileList => void handleFileChange(fileList)} onValidate={handleFileValidate} disabled={importing} />
                        </div>
                    </PageLabelWithValue>

                    {error && <MessageBanner variant="error" message={error} />}

                    {preview &&
                        !error &&
                        (atCapacity ? (
                            <MessageBanner variant="error" message={`This dataset is already at the ${MAX_EXAMPLES_PER_DATASET}-example limit. Delete some examples before importing more.`} />
                        ) : willDrop > 0 ? (
                            <MessageBanner
                                variant="warning"
                                message={
                                    <span>
                                        Your file has <strong>{preview.rows}</strong> {preview.rows === 1 ? "row" : "rows"}
                                        {currentExampleCount > 0 && (
                                            <>
                                                {" "}
                                                and this dataset already has <strong>{currentExampleCount}</strong>
                                            </>
                                        )}
                                        . Only the first <strong>{willImport}</strong> {willImport === 1 ? "row" : "rows"} will be imported; the remaining <strong>{willDrop}</strong> will be dropped because datasets are capped at{" "}
                                        {MAX_EXAMPLES_PER_DATASET} examples.
                                    </span>
                                }
                            />
                        ) : (
                            <div className="text-xs text-(--secondary-text-wMain)">
                                {preview.rows} {preview.rows === 1 ? "row" : "rows"} detected — ready to import.
                            </div>
                        ))}
                </div>
                <DialogFooter>
                    <DialogClose asChild>
                        <Button variant="ghost" disabled={importing}>
                            Cancel
                        </Button>
                    </DialogClose>
                    <Button onClick={() => void handleImport()} disabled={isImportDisabled}>
                        {importing ? "Importing..." : "Import"}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
