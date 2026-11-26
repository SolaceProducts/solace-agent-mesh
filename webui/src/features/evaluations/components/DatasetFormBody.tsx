// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps, forwardRef, useCallback, useRef, useState } from "react";
import { FileDown, FileJson, Sparkles, Upload } from "lucide-react";

import { type EvaluationsApi, evaluationsApi } from "@/api/evaluations";

import { PageLabel, PageSection } from "@/common/layout";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui/dropdown-menu";
import { Input } from "@/ui/input";
import { Table, TableBody } from "@/ui/table";
import { Textarea } from "@/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/ui/tooltip";
import type { CreateExampleRequest, EvalArtifactRole, EvalDataset, EvalExample, ExpectedFacts, ExpectedPath } from "@/types/eval";

import { ConfirmationDialog, EmptyState, MorePopoverButton } from "@/common";
import { useIsWorkflowEvalsEnabled } from "@/hooks";

import { MAX_EXAMPLES_PER_DATASET } from "../constants";
import { useEvalExamplesPaginated } from "../hooks";
import { downloadExampleArtifact, examplesDescription, isUntitledPlaceholder, parseExpectedFactsText, parseExpectedPathText, parseInputDataText } from "../utils";

import { AiGenerationDialog } from "./AiGenerationDialog";
import { DatasetImportDialog } from "./DatasetImportDialog";
import { ExampleEditorRow, NewExampleRow } from "./ExampleEditorRows";
import { ExamplesPaginationFooter } from "./ExamplesPaginationFooter";
import { ExamplesTableHeader } from "./ExamplesTableHeader";

const EXAMPLES_PAGE_SIZE = 10;

interface DatasetFormBodyProps {
    readonly datasetId: string;
    readonly dataset: EvalDataset | null;
    readonly onDatasetChange: (ds: EvalDataset) => void;
    readonly name: string;
    readonly onNameChange: (v: string) => void;
    readonly description: string;
    readonly onDescriptionChange: (v: string) => void;
    readonly source?: "import" | "ai" | "manual";
    readonly autosaveFields?: boolean;
}

class ExampleFileUploadError extends Error {
    readonly role: EvalArtifactRole;

    constructor(role: EvalArtifactRole, cause: unknown) {
        super(getErrorMessage(cause));
        this.role = role;
    }
}

function newExampleRequest(prompt: string, expected: string, inputText: string, pathText: string, factsText: string): { request?: CreateExampleRequest; error?: string } {
    const input = parseInputDataText(inputText);
    const path = parseExpectedPathText(pathText);
    const facts = parseExpectedFactsText(factsText);
    const error = input.error ?? path.error ?? facts.error;
    if (error) return { error };
    if (!prompt.trim() && !input.value) return {};
    return { request: { prompt: prompt.trim(), expectedResponse: expected.trim() || undefined, inputData: input.value, expectedPath: path.value, expectedFacts: facts.value } };
}

function workflowFieldProps(
    enabled: boolean,
    inputDataExampleCount: number,
    setters: { setNewInputData: (v: string) => void; setNewExpectedPath: (v: string) => void; setNewExpectedFacts: (v: string) => void }
): Pick<ComponentProps<typeof NewExampleRow>, "inputFirst" | "onInputDataChange" | "onExpectedPathChange" | "onExpectedFactsChange"> {
    if (!enabled) return { inputFirst: false };
    return { inputFirst: inputDataExampleCount > 0, onInputDataChange: setters.setNewInputData, onExpectedPathChange: setters.setNewExpectedPath, onExpectedFactsChange: setters.setNewExpectedFacts };
}

async function createExampleWithOptionalFiles(evalApi: EvaluationsApi, datasetId: string, example: CreateExampleRequest, inputFile: File | null, expectedFile: File | null): Promise<void> {
    const created = await evalApi.createExample(datasetId, example);
    if (!inputFile && !expectedFile) return;
    const uploadAs = async (role: EvalArtifactRole, file: File) => {
        try {
            await evalApi.uploadExampleArtifact(created.id, role, file);
        } catch (error) {
            throw new ExampleFileUploadError(role, error);
        }
    };
    try {
        if (inputFile) await uploadAs("input", inputFile);
        if (expectedFile) await uploadAs("expected", expectedFile);
    } catch (error) {
        await evalApi.deleteExample(created.id).catch(cleanupError => console.warn(`eval: rollback delete of half-created example ${created.id} failed; an orphan example may remain`, cleanupError));
        throw error;
    }
}

export const DatasetFormBody = forwardRef<HTMLInputElement, DatasetFormBodyProps>(function DatasetFormBody(
    { datasetId, dataset, onDatasetChange, name, onNameChange, description, onDescriptionChange, source = "manual", autosaveFields = true },
    nameInputRef
) {
    const [addingExample, setAddingExample] = useState(false);
    const [newPrompt, setNewPrompt] = useState("");
    const [newInputData, setNewInputData] = useState("");
    const [newExpectedPath, setNewExpectedPath] = useState("");
    const [newExpectedFacts, setNewExpectedFacts] = useState("");
    const workflowEvalsEnabled = useIsWorkflowEvalsEnabled();
    const [newExpected, setNewExpected] = useState("");
    const [newFile, setNewFile] = useState<File | null>(null);
    const [newFileError, setNewFileError] = useState<string | null>(null);
    const [newExpectedFile, setNewExpectedFile] = useState<File | null>(null);
    const [newExpectedFileError, setNewExpectedFileError] = useState<string | null>(null);
    const [addingSaving, setAddingSaving] = useState(false);
    const [addError, setAddError] = useState<string | null>(null);
    const [deleteTarget, setDeleteTarget] = useState<{ example: EvalExample; position: number } | null>(null);
    const [importOpen, setImportOpen] = useState(false);
    const [aiExampleOpen, setAiExampleOpen] = useState(false);
    const downloadRef = useRef<HTMLAnchorElement>(null);

    const {
        examples,
        loading: exLoading,
        pageNumber: exPage,
        setPageNumber: setExPage,
        totalPages: exTotalPages,
        totalCount: exTotalCount,
        contentFilter,
        setContentFilter,
        fetchExamples,
    } = useEvalExamplesPaginated(datasetId, { pageSize: EXAMPLES_PAGE_SIZE });

    const saveField = useCallback(
        async (field: "name" | "description", value: string) => {
            if (!dataset) return;
            const trimmed = value.trim();
            if (field === "name" && trimmed === dataset.name) return;
            if (field === "description" && trimmed === (dataset.description ?? "")) return;
            if (field === "name" && !trimmed) return;
            if (field === "description" && !trimmed) return;
            try {
                const updated = await evaluationsApi.updateDataset(dataset.id, { [field]: trimmed });
                onDatasetChange(updated);
            } catch (err) {
                if (field === "name") onNameChange(isUntitledPlaceholder(dataset.name) ? "" : dataset.name);
                if (field === "description") onDescriptionChange(dataset.description ?? "");
                throw err;
            }
        },
        [dataset, onDatasetChange, onNameChange, onDescriptionChange]
    );

    const refetchDataset = useCallback(async () => {
        try {
            const ds = await evaluationsApi.getDataset(datasetId);
            onDatasetChange(ds);
        } catch {}
    }, [datasetId, onDatasetChange]);

    const refreshAfterMutation = useCallback((silent = false) => Promise.all([fetchExamples(silent), refetchDataset()]), [fetchExamples, refetchDataset]);

    const handleExportExamples = async (format: "csv" | "json") => {
        const blob = await evaluationsApi.exportExamples(datasetId, format);
        const url = URL.createObjectURL(blob);
        const a = downloadRef.current ?? document.createElement("a");
        a.href = url;
        a.download = `dataset-${datasetId}-examples.${format}`;
        a.click();
        URL.revokeObjectURL(url);
    };

    const actualExampleCount = dataset?.exampleCount ?? 0;
    const atExampleLimit = actualExampleCount >= MAX_EXAMPLES_PER_DATASET;

    const handleAddExample = async () => {
        const parsed = newExampleRequest(newPrompt, newExpected, newInputData, newExpectedPath, newExpectedFacts);
        if (parsed.error) {
            setAddError(parsed.error);
            return;
        }
        if (!parsed.request || atExampleLimit) return;
        setAddingSaving(true);
        setAddError(null);
        setNewFileError(null);
        setNewExpectedFileError(null);
        try {
            await createExampleWithOptionalFiles(evaluationsApi, datasetId, parsed.request, newFile, newExpectedFile);
            setNewPrompt("");
            setNewInputData("");
            setNewExpectedPath("");
            setNewExpectedFacts("");
            setNewExpected("");
            setNewFile(null);
            setNewExpectedFile(null);
            setAddingExample(false);
            await refreshAfterMutation();
        } catch (err) {
            if (err instanceof ExampleFileUploadError) {
                (err.role === "expected" ? setNewExpectedFileError : setNewFileError)(err.message);
            } else {
                setAddError(getErrorMessage(err));
            }
        } finally {
            setAddingSaving(false);
        }
    };

    const handleUploadArtifact = async (exampleId: string, role: EvalArtifactRole, file: File) => {
        await evaluationsApi.uploadExampleArtifact(exampleId, role, file);
        await fetchExamples(true);
    };

    const handleRemoveArtifact = async (exampleId: string, role: EvalArtifactRole) => {
        await evaluationsApi.deleteExampleArtifact(exampleId, role);
        await fetchExamples(true);
    };

    const handleCancelAdd = () => {
        setNewFile(null);
        setNewFileError(null);
        setNewExpectedFile(null);
        setNewExpectedFileError(null);
        setNewPrompt("");
        setNewInputData("");
        setNewExpectedPath("");
        setNewExpectedFacts("");
        setNewExpected("");
        setAddError(null);
        setAddingExample(false);
    };

    const handleDeleteExample = async () => {
        if (!deleteTarget) return;
        await evaluationsApi.deleteExample(deleteTarget.example.id);
        setDeleteTarget(null);
        await refreshAfterMutation(true);
    };

    const handleInlineUpdate = async (exampleId: string, field: "prompt" | "expectedResponse", value: string) => {
        await evaluationsApi.updateExample(exampleId, { [field]: value });
        await fetchExamples(true);
    };

    const handleInputDataUpdate = async (exampleId: string, value: Record<string, unknown> | null) => {
        await evaluationsApi.updateExample(exampleId, { inputData: value });
        await fetchExamples(true);
    };

    const handleExpectedPathUpdate = async (exampleId: string, value: ExpectedPath | null) => {
        await evaluationsApi.updateExample(exampleId, { expectedPath: value });
        await fetchExamples(true);
    };

    const handleExpectedFactsUpdate = async (exampleId: string, value: ExpectedFacts | null) => {
        await evaluationsApi.updateExample(exampleId, { expectedFacts: value });
        await fetchExamples(true);
    };

    const newRowWorkflowFields = workflowFieldProps(workflowEvalsEnabled, dataset?.inputDataExampleCount ?? 0, { setNewInputData, setNewExpectedPath, setNewExpectedFacts });

    const addExampleButton = (
        <Button variant="outline" onClick={() => setAddingExample(true)} disabled={addingExample || atExampleLimit}>
            + Add Example
        </Button>
    );

    return (
        <>
            <PageSection>
                <div className="flex flex-col gap-5">
                    <div className="flex items-center gap-6">
                        <PageLabel required className="w-28 shrink-0">
                            Name
                        </PageLabel>
                        <Input ref={nameInputRef} value={name} onChange={e => onNameChange(e.target.value)} onBlur={autosaveFields ? () => void saveField("name", name) : undefined} placeholder="Dataset name" className="max-w-md" />
                    </div>
                    <div className="flex gap-6">
                        <PageLabel required className="mt-2 w-28 shrink-0">
                            Description
                        </PageLabel>
                        <Textarea
                            value={description}
                            onChange={e => onDescriptionChange(e.target.value)}
                            onBlur={autosaveFields ? () => void saveField("description", description) : undefined}
                            rows={3}
                            placeholder="Describe what this dataset tests"
                            className="max-w-lg"
                        />
                    </div>
                </div>
            </PageSection>

            <PageSection className="mt-6">
                <div className="flex items-start justify-between gap-4">
                    <div className="flex flex-col gap-1">
                        <div className="flex items-center gap-2">
                            <div className="text-sm font-semibold">Examples</div>
                            {exTotalCount > 0 && (
                                <span className="text-xs text-(--secondary-text-wMain)">
                                    {exTotalCount} / {MAX_EXAMPLES_PER_DATASET}
                                </span>
                            )}
                        </div>
                        <div className="text-sm text-(--secondary-text-wMain)">
                            {examplesDescription(workflowEvalsEnabled)} (max {MAX_EXAMPLES_PER_DATASET} per dataset)
                        </div>
                    </div>
                    <div className="flex items-center gap-2">
                        {source === "import" && (
                            <Button variant="outline" onClick={() => setImportOpen(true)} disabled={atExampleLimit}>
                                <Upload className="mr-1 size-4" /> Import From File
                            </Button>
                        )}
                        {source === "ai" &&
                            (atExampleLimit ? (
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <span tabIndex={0}>
                                            <Button variant="outline" disabled>
                                                <Sparkles className="mr-1 size-4" /> Generate with AI
                                            </Button>
                                        </span>
                                    </TooltipTrigger>
                                    <TooltipContent>Dataset is full ({MAX_EXAMPLES_PER_DATASET} examples max).</TooltipContent>
                                </Tooltip>
                            ) : (
                                <Button variant="outline" onClick={() => setAiExampleOpen(true)}>
                                    <Sparkles className="mr-1 size-4" /> Generate with AI
                                </Button>
                            ))}
                        {atExampleLimit ? (
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <span tabIndex={0}>{addExampleButton}</span>
                                </TooltipTrigger>
                                <TooltipContent>Datasets are capped at {MAX_EXAMPLES_PER_DATASET} examples.</TooltipContent>
                            </Tooltip>
                        ) : (
                            addExampleButton
                        )}
                        <DropdownMenu>
                            <DropdownMenuTrigger asChild>{MorePopoverButton}</DropdownMenuTrigger>
                            <DropdownMenuContent align="end">
                                {source !== "import" && (
                                    <DropdownMenuItem onClick={() => setImportOpen(true)} disabled={atExampleLimit}>
                                        <Upload className="mr-2 size-4" /> Import From File
                                    </DropdownMenuItem>
                                )}
                                {source !== "ai" && (
                                    <DropdownMenuItem onClick={() => setAiExampleOpen(true)} disabled={atExampleLimit}>
                                        <Sparkles className="mr-2 size-4" /> Generate with AI
                                    </DropdownMenuItem>
                                )}
                                <DropdownMenuItem onClick={() => void handleExportExamples("csv")}>
                                    <FileDown className="mr-2 size-4" /> Export as CSV
                                </DropdownMenuItem>
                                <DropdownMenuItem onClick={() => void handleExportExamples("json")}>
                                    <FileJson className="mr-2 size-4" /> Export as JSON
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </div>
                </div>

                {(exTotalCount > 0 || !!contentFilter) && <Input className="w-64" placeholder="Filter by content..." value={contentFilter} onChange={e => setContentFilter(e.target.value)} />}

                {exLoading && <EmptyState variant="loading" title="Loading examples..." />}
                {!exLoading && (examples.length > 0 || addingExample) && (
                    <div className="rounded-xs border">
                        <Table className="table-fixed">
                            <ExamplesTableHeader withActionsColumn withInputData={workflowEvalsEnabled} />
                            <TableBody borderLastRow>
                                {examples.map((ex, idx) => {
                                    const position = (exPage - 1) * EXAMPLES_PAGE_SIZE + idx + 1;
                                    return (
                                        <ExampleEditorRow
                                            key={ex.id}
                                            example={ex}
                                            position={position}
                                            onInlineUpdate={handleInlineUpdate}
                                            showInputData={workflowEvalsEnabled}
                                            onInputDataUpdate={handleInputDataUpdate}
                                            onExpectedPathUpdate={handleExpectedPathUpdate}
                                            onExpectedFactsUpdate={handleExpectedFactsUpdate}
                                            onUploadArtifact={handleUploadArtifact}
                                            onRemoveArtifact={handleRemoveArtifact}
                                            onDownloadArtifact={(example, role) => downloadExampleArtifact(evaluationsApi, example, role)}
                                            onDelete={() => setDeleteTarget({ example: ex, position })}
                                        />
                                    );
                                })}
                                {addingExample && (
                                    <NewExampleRow
                                        {...newRowWorkflowFields}
                                        prompt={newPrompt}
                                        onPromptChange={setNewPrompt}
                                        inputData={newInputData}
                                        expected={newExpected}
                                        onExpectedChange={setNewExpected}
                                        expectedPath={newExpectedPath}
                                        expectedFacts={newExpectedFacts}
                                        file={newFile}
                                        onFileChange={file => {
                                            setNewFile(file);
                                            setNewFileError(null);
                                        }}
                                        fileError={newFileError}
                                        expectedFile={newExpectedFile}
                                        onExpectedFileChange={file => {
                                            setNewExpectedFile(file);
                                            setNewExpectedFileError(null);
                                        }}
                                        expectedFileError={newExpectedFileError}
                                        saving={addingSaving}
                                        error={addError}
                                        onSave={() => void handleAddExample()}
                                        onCancel={handleCancelAdd}
                                    />
                                )}
                            </TableBody>
                        </Table>
                        <ExamplesPaginationFooter page={exPage} pageSize={EXAMPLES_PAGE_SIZE} totalPages={exTotalPages} totalCount={exTotalCount} onPageChange={setExPage} summariseSinglePage />
                    </div>
                )}
            </PageSection>

            <DatasetImportDialog open={importOpen} onClose={() => setImportOpen(false)} onImported={() => void refreshAfterMutation()} datasetId={datasetId} currentExampleCount={actualExampleCount} />

            <AiGenerationDialog mode="examples" open={aiExampleOpen} datasetId={datasetId} existingCount={actualExampleCount} onClose={() => setAiExampleOpen(false)} onGenerated={() => void refreshAfterMutation()} />

            <ConfirmationDialog
                title="Delete Example"
                content={
                    <span>
                        Delete example <strong>#{deleteTarget?.position}</strong>? This cannot be undone.
                    </span>
                }
                open={!!deleteTarget}
                onOpenChange={open => {
                    if (!open) setDeleteTarget(null);
                }}
                onCancel={() => setDeleteTarget(null)}
                onConfirm={() => void handleDeleteExample()}
            />

            <a ref={downloadRef} style={{ display: "none" }} />
        </>
    );
});
