// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { Trash2 } from "lucide-react";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { TableCell, TableRow } from "@/ui/table";
import { Textarea } from "@/ui/textarea";
import type { EvalArtifactRole, EvalExample, ExpectedFacts, ExpectedPath } from "@/types/eval";

import { MessageBanner } from "@/common";

import { EXPECTED_ARTIFACT_ACCEPT } from "../constants";
import { EXPECTED_FACTS_PLACEHOLDER, EXPECTED_PATH_PLACEHOLDER, formatExpectedFacts, formatExpectedPath, formatInputData, INPUT_DATA_PLACEHOLDER, parseExpectedFactsText, parseExpectedPathText, parseInputDataText } from "../utils";

import { ExampleArtifactField } from "./ExampleArtifactField";
import { InlineEditableCell } from "./InlineEditableCell";

interface ExampleEditorRowProps {
    readonly example: EvalExample;
    readonly position: number;
    readonly onInlineUpdate: (exampleId: string, field: "prompt" | "expectedResponse", value: string) => Promise<void>;
    readonly showInputData?: boolean;
    readonly onInputDataUpdate?: (exampleId: string, value: Record<string, unknown> | null) => Promise<void>;
    readonly onExpectedPathUpdate?: (exampleId: string, value: ExpectedPath | null) => Promise<void>;
    readonly onExpectedFactsUpdate?: (exampleId: string, value: ExpectedFacts | null) => Promise<void>;
    readonly onUploadArtifact: (exampleId: string, role: EvalArtifactRole, file: File) => Promise<void>;
    readonly onRemoveArtifact: (exampleId: string, role: EvalArtifactRole) => Promise<void>;
    readonly onDownloadArtifact: (example: EvalExample, role: EvalArtifactRole) => Promise<void>;
    readonly onDelete: () => void;
}

interface JsonCellProps<T> {
    readonly value: T | null | undefined;
    readonly onSave?: (value: T | null) => Promise<void>;
}

interface JsonObjectCellProps<T> extends JsonCellProps<T> {
    readonly label: string;
    readonly placeholder: string;
    readonly format: (value: T | null | undefined) => string;
    readonly parse: (text: string) => { value?: T; error?: string };
}

function JsonObjectCell<T>({ value, onSave, label, placeholder, format, parse }: JsonObjectCellProps<T>) {
    const [error, setError] = useState<string | null>(null);
    const save = async (text: string) => {
        const parsed = parse(text);
        if (parsed.error) {
            setError(parsed.error);
            throw new Error(parsed.error);
        }
        try {
            await onSave?.(parsed.value ?? null);
        } catch (err) {
            setError(getErrorMessage(err));
            throw err;
        }
        setError(null);
    };
    return (
        <div className="mt-1 flex flex-col">
            <span className="px-2 text-xs text-(--secondary-text-wMain)">{label}</span>
            <InlineEditableCell value={format(value)} onSave={save} placeholder={placeholder} readOnly={!onSave} keepDraftOnError />
            {error && <span className="px-2 text-xs text-(--error-wMain)">{error}</span>}
        </div>
    );
}

export function InputDataCell(props: JsonCellProps<Record<string, unknown>>) {
    return <JsonObjectCell {...props} label="Input data (JSON): what the workflow receives" placeholder={INPUT_DATA_PLACEHOLDER} format={formatInputData} parse={parseInputDataText} />;
}

export function ExpectedFactsCell(props: JsonCellProps<ExpectedFacts>) {
    return <JsonObjectCell {...props} label="Expected values (JSON): values the result must and must not contain" placeholder={EXPECTED_FACTS_PLACEHOLDER} format={formatExpectedFacts} parse={parseExpectedFactsText} />;
}

export function ExpectedPathCell(props: JsonCellProps<ExpectedPath>) {
    return <JsonObjectCell {...props} label="Expected steps (JSON): steps that must run and steps that must be skipped" placeholder={EXPECTED_PATH_PLACEHOLDER} format={formatExpectedPath} parse={parseExpectedPathText} />;
}

const EVALUATOR_INSTRUCTIONS_LABEL = "Evaluator instructions (optional)";

function FieldLabel({ children, className }: { readonly children: React.ReactNode; readonly className?: string }) {
    return <span className={cn("block text-xs text-(--secondary-text-wMain)", className)}>{children}</span>;
}

function LabelledField({ label, children }: { readonly label: string; readonly children: React.ReactNode }) {
    return (
        <div className="flex flex-col gap-1">
            <FieldLabel>{label}</FieldLabel>
            {children}
        </div>
    );
}

export function ExampleEditorRow({ example, position, onInlineUpdate, showInputData, onInputDataUpdate, onExpectedPathUpdate, onExpectedFactsUpdate, onUploadArtifact, onRemoveArtifact, onDownloadArtifact, onDelete }: ExampleEditorRowProps) {
    return (
        <TableRow className="align-top">
            <TableCell className="pl-4 text-xs text-(--secondary-text-wMain)">{position}</TableCell>
            <TableCell className="[overflow-wrap:anywhere]">
                {example.inputData ? (
                    <>
                        {onInputDataUpdate && <InputDataCell value={example.inputData} onSave={value => onInputDataUpdate(example.id, value)} />}
                        <FieldLabel className="mt-1 px-2">{EVALUATOR_INSTRUCTIONS_LABEL}</FieldLabel>
                        <InlineEditableCell value={example.prompt} onSave={value => onInlineUpdate(example.id, "prompt", value)} placeholder="Add instructions..." />
                    </>
                ) : (
                    <>
                        <InlineEditableCell value={example.prompt} onSave={value => onInlineUpdate(example.id, "prompt", value)} placeholder="Enter prompt..." />
                        {showInputData && onInputDataUpdate && <InputDataCell value={example.inputData} onSave={value => onInputDataUpdate(example.id, value)} />}
                    </>
                )}
            </TableCell>
            <TableCell className="[overflow-wrap:anywhere]">
                <InlineEditableCell value={example.expectedResponse ?? ""} onSave={value => onInlineUpdate(example.id, "expectedResponse", value)} />
                {(showInputData || !!example.expectedPath) && onExpectedPathUpdate && <ExpectedPathCell value={example.expectedPath} onSave={value => onExpectedPathUpdate(example.id, value)} />}
                {(showInputData || !!example.expectedFacts) && onExpectedFactsUpdate && <ExpectedFactsCell value={example.expectedFacts} onSave={value => onExpectedFactsUpdate(example.id, value)} />}
            </TableCell>
            <TableCell>
                <ExampleArtifactField artifact={example.inputArtifact} onUpload={file => onUploadArtifact(example.id, "input", file)} onRemove={() => onRemoveArtifact(example.id, "input")} onDownload={() => onDownloadArtifact(example, "input")} />
            </TableCell>
            <TableCell>
                <ExampleArtifactField
                    artifact={example.expectedArtifact}
                    onUpload={file => onUploadArtifact(example.id, "expected", file)}
                    onRemove={() => onRemoveArtifact(example.id, "expected")}
                    onDownload={() => onDownloadArtifact(example, "expected")}
                    accept={EXPECTED_ARTIFACT_ACCEPT}
                    subject="expected file"
                />
            </TableCell>
            <TableCell>
                <Button variant="ghost" size="icon" onClick={onDelete} aria-label={`Delete example #${position}`}>
                    <Trash2 className="size-4" />
                </Button>
            </TableCell>
        </TableRow>
    );
}

interface NewExampleRowProps {
    readonly inputFirst?: boolean;
    readonly prompt: string;
    readonly onPromptChange: (v: string) => void;
    readonly inputData?: string;
    readonly onInputDataChange?: (v: string) => void;
    readonly expected: string;
    readonly onExpectedChange: (v: string) => void;
    readonly expectedPath?: string;
    readonly onExpectedPathChange?: (v: string) => void;
    readonly expectedFacts?: string;
    readonly onExpectedFactsChange?: (v: string) => void;
    readonly file: File | null;
    readonly onFileChange: (file: File | null) => void;
    readonly fileError?: string | null;
    readonly expectedFile: File | null;
    readonly onExpectedFileChange: (file: File | null) => void;
    readonly expectedFileError?: string | null;
    readonly saving: boolean;
    readonly error: string | null;
    readonly onSave: () => void;
    readonly onCancel: () => void;
}

export function NewExampleRow({
    inputFirst = false,
    prompt,
    onPromptChange,
    inputData,
    onInputDataChange,
    expected,
    onExpectedChange,
    expectedPath,
    onExpectedPathChange,
    expectedFacts,
    onExpectedFactsChange,
    file,
    onFileChange,
    fileError,
    expectedFile,
    onExpectedFileChange,
    expectedFileError,
    saving,
    error,
    onSave,
    onCancel,
}: NewExampleRowProps) {
    const withInput = onInputDataChange !== undefined;
    const hasContent = !!prompt.trim() || (withInput && !!inputData?.trim());
    const inputDataField = withInput && (
        <LabelledField label="Input data (JSON): what the workflow receives">
            <Textarea
                value={inputData ?? ""}
                onChange={e => onInputDataChange(e.target.value)}
                placeholder={`For example: ${INPUT_DATA_PLACEHOLDER}`}
                rows={3}
                disabled={saving}
                className="font-mono text-xs"
                aria-label="Input data (JSON)"
                autoFocus={inputFirst}
            />
        </LabelledField>
    );
    return (
        <TableRow className="bg-(--secondary-w10)/30 align-top">
            <TableCell className="pl-4" />
            <TableCell>
                <div className="flex flex-col gap-2">
                    {inputFirst && withInput ? (
                        <>
                            {inputDataField}
                            <LabelledField label={EVALUATOR_INSTRUCTIONS_LABEL}>
                                <Textarea
                                    value={prompt}
                                    onChange={e => onPromptChange(e.target.value)}
                                    placeholder="What the LLM judge should check in this example. The workflow does not receive this text."
                                    rows={2}
                                    disabled={saving}
                                    aria-label={EVALUATOR_INSTRUCTIONS_LABEL}
                                />
                            </LabelledField>
                        </>
                    ) : (
                        <>
                            <LabelledField label={withInput ? "Prompt (optional with input data)" : "Prompt"}>
                                <Textarea value={prompt} onChange={e => onPromptChange(e.target.value)} placeholder="Enter prompt..." rows={3} disabled={saving} autoFocus aria-label="Prompt" />
                            </LabelledField>
                            {inputDataField}
                        </>
                    )}
                </div>
            </TableCell>
            <TableCell>
                <div className="flex flex-col gap-2">
                    <LabelledField label="Expected response (optional)">
                        <Textarea value={expected} onChange={e => onExpectedChange(e.target.value)} rows={3} disabled={saving} aria-label="Expected response" />
                    </LabelledField>
                    {onExpectedPathChange && (
                        <LabelledField label="Expected steps (JSON): steps that must run and steps that must be skipped">
                            <Textarea
                                value={expectedPath ?? ""}
                                onChange={e => onExpectedPathChange(e.target.value)}
                                placeholder={`For example: ${EXPECTED_PATH_PLACEHOLDER}`}
                                rows={2}
                                disabled={saving}
                                className="font-mono text-xs"
                                aria-label="Expected steps (JSON)"
                            />
                        </LabelledField>
                    )}
                    {onExpectedFactsChange && (
                        <LabelledField label="Expected values (JSON): values the result must and must not contain">
                            <Textarea
                                value={expectedFacts ?? ""}
                                onChange={e => onExpectedFactsChange(e.target.value)}
                                placeholder={`For example: ${EXPECTED_FACTS_PLACEHOLDER}`}
                                rows={2}
                                disabled={saving}
                                className="font-mono text-xs"
                                aria-label="Expected values (JSON)"
                            />
                        </LabelledField>
                    )}
                </div>
            </TableCell>
            <TableCell>
                <ExampleArtifactField disabled={saving} artifact={file ? { filename: file.name, size: file.size, version: 0 } : null} onUpload={onFileChange} onRemove={() => onFileChange(null)} deferredError={fileError} />
            </TableCell>
            <TableCell>
                <ExampleArtifactField
                    disabled={saving}
                    artifact={expectedFile ? { filename: expectedFile.name, size: expectedFile.size, version: 0 } : null}
                    onUpload={onExpectedFileChange}
                    onRemove={() => onExpectedFileChange(null)}
                    accept={EXPECTED_ARTIFACT_ACCEPT}
                    subject="expected file"
                    deferredError={expectedFileError}
                />
            </TableCell>
            <TableCell>
                <div className="flex flex-col gap-1">
                    <Button size="sm" onClick={onSave} disabled={saving || !hasContent}>
                        {saving ? "..." : "Save"}
                    </Button>
                    <Button size="sm" variant="ghost" onClick={onCancel} disabled={saving}>
                        Cancel
                    </Button>
                    {error && <MessageBanner variant="error" message={error} />}
                </div>
            </TableCell>
        </TableRow>
    );
}
