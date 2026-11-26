// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ChangeEvent, type DragEvent, type RefCallback, useRef, useState } from "react";
import { Upload, X } from "lucide-react";
import { useFormContext, useWatch } from "react-hook-form";

import { Button } from "@/ui/button";

import { type ControlProps, type FieldProps, FieldShell } from "../FieldShell";

export interface CredentialFileFieldProps extends Omit<FieldProps, "placeholder"> {
    validate: (text: string) => string | null;
    accept: string;
    hasStoredValue: boolean;
}

const READ_FAILED = "That file could not be read. Try again.";
const STORED_TITLE = "A key is saved";
const UNNAMED_FILE = "Key file";

interface FileCardProps {
    title: string;
    removeLabel: string;
    disabled?: boolean;
    controlProps: ControlProps;
    controlRef: RefCallback<HTMLElement>;
    onRemove: () => void;
}

function FileCard({ title, removeLabel, disabled, controlProps, controlRef, onRemove }: Readonly<FileCardProps>) {
    return (
        <div className="flex items-center justify-between gap-3 rounded-xs border border-(--secondary-w20) px-4 py-3 md:w-1/2">
            <span className="truncate text-sm font-semibold">{title}</span>
            <Button ref={controlRef} type="button" variant="ghost" size="icon" disabled={disabled} onClick={onRemove} {...controlProps} aria-label={removeLabel}>
                <X className="size-4" />
            </Button>
        </div>
    );
}

export function CredentialFileField({ disabled, validate, accept, hasStoredValue, ...shell }: Readonly<CredentialFileFieldProps>) {
    const [replacingStored, setReplacingStored] = useState(false);
    const [ownClear, setOwnClear] = useState(false);
    const [readFailed, setReadFailed] = useState(false);
    const [dragging, setDragging] = useState(false);
    const [fileName, setFileName] = useState<string | null>(null);
    const fileInput = useRef<HTMLInputElement>(null);
    const focusNextControl = useRef(false);
    const { resetField } = useFormContext();

    const watched: unknown = useWatch({ name: shell.name });
    const value = typeof watched === "string" ? watched : "";
    const [seen, setSeen] = useState({ hasStoredValue, value });
    if (seen.hasStoredValue !== hasStoredValue || seen.value !== value) {
        const clearedByForm = value === "" && seen.value !== "" && !ownClear;
        setSeen({ hasStoredValue, value });
        setOwnClear(false);
        if (value === "") setFileName(null);
        if (seen.hasStoredValue !== hasStoredValue || clearedByForm) setReplacingStored(false);
    }

    const showsStoredCard = hasStoredValue && value === "" && !replacingStored;
    const showsFileCard = value !== "" && validate(value) === null;
    const canKeepStored = hasStoredValue && replacingStored && !showsFileCard;

    const keepStored = () => (
        <Button
            type="button"
            variant="link"
            size="sm"
            className="h-auto px-0 text-xs"
            disabled={disabled}
            onClick={() => {
                focusNextControl.current = true;
                setOwnClear(true);
                resetField(shell.name);
                setReplacingStored(false);
            }}
        >
            Keep the saved key
        </Button>
    );

    return (
        <FieldShell {...shell} aside={canKeepStored ? keepStored : undefined}>
            {({ field, controlProps }) => {
                const controlRef = (element: HTMLElement | null) => {
                    field.ref(element);
                    if (element && focusNextControl.current) {
                        focusNextControl.current = false;
                        element.focus();
                    }
                };

                const clear = () => {
                    focusNextControl.current = true;
                    setOwnClear(true);
                    setFileName(null);
                    field.onChange("");
                    setReplacingStored(true);
                };

                const readFile = async (file: File | undefined) => {
                    if (!file) return;
                    try {
                        const contents = await file.text();
                        setReadFailed(false);
                        setFileName(file.name);
                        if (validate(contents) === null) focusNextControl.current = true;
                        field.onChange(contents);
                        field.onBlur();
                    } catch {
                        setReadFailed(true);
                    }
                };

                if (showsStoredCard) {
                    return <FileCard title={STORED_TITLE} removeLabel={`Replace ${shell.label}`} disabled={disabled} controlProps={controlProps} controlRef={controlRef} onRemove={clear} />;
                }

                if (showsFileCard) {
                    return <FileCard title={fileName ?? UNNAMED_FILE} removeLabel={`Remove ${shell.label}`} disabled={disabled} controlProps={controlProps} controlRef={controlRef} onRemove={clear} />;
                }

                const onDrop = (event: DragEvent<HTMLDivElement>) => {
                    event.preventDefault();
                    setDragging(false);
                    if (!disabled) void readFile(event.dataTransfer.files[0]);
                };

                const onPick = (event: ChangeEvent<HTMLInputElement>) => {
                    void readFile(event.target.files?.[0]);
                    event.target.value = "";
                };

                return (
                    <div className="flex flex-col gap-1">
                        <div
                            data-testid="credential-drop-zone"
                            onDragOver={event => {
                                event.preventDefault();
                                setDragging(true);
                            }}
                            onDragLeave={() => setDragging(false)}
                            onDrop={onDrop}
                            className={`flex flex-col items-center gap-3 rounded-xs border border-dashed px-4 py-8 text-sm ${dragging ? "border-(--primary-wMain) bg-(--primary-w10)" : "border-(--secondary-w40)"}`}
                        >
                            <span className="flex items-center gap-2">
                                <Upload className="size-4" aria-hidden="true" />
                                Drag and drop a file here
                            </span>
                            <div className="flex w-1/4 min-w-40 items-center gap-2 text-xs text-(--secondary-text-wMain)" aria-hidden="true">
                                <span className="h-px flex-1 bg-(--secondary-w20)" />
                                <span>OR</span>
                                <span className="h-px flex-1 bg-(--secondary-w20)" />
                            </div>
                            <Button type="button" variant="link" size="sm" ref={controlRef} disabled={disabled} onClick={() => fileInput.current?.click()} {...controlProps} aria-label={`Upload File for ${shell.label}`}>
                                Upload File
                            </Button>
                            <input ref={fileInput} type="file" accept={accept} className="hidden" tabIndex={-1} aria-hidden="true" onChange={onPick} />
                        </div>
                        {readFailed && (
                            <p role="alert" className="text-xs text-(--error-wMain)">
                                {READ_FAILED}
                            </p>
                        )}
                    </div>
                );
            }}
        </FieldShell>
    );
}
