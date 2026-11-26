// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef, useState } from "react";
import { Paperclip } from "lucide-react";

import { getErrorMessage } from "@/utils/api";
import { validateFileSizes } from "@/utils/fileValidation";
import { formatBytes } from "@/utils/format";

import { Button } from "@/ui/button";
import type { EvalExampleArtifact } from "@/types/eval";

import { FileBadge } from "@/features/chat/file/FileBadge";

import { MAX_EXAMPLE_ARTIFACT_BYTES } from "../constants";

function artifactLabel(artifact: EvalExampleArtifact): string {
    const size = artifact.size == null ? "" : ` (${formatBytes(artifact.size)})`;
    return `${artifact.filename}${size}`;
}

interface ExampleArtifactFieldProps {
    readonly artifact?: EvalExampleArtifact | null;
    readonly onUpload?: (file: File) => Promise<void> | void;
    readonly onRemove?: () => Promise<void> | void;
    readonly onDownload?: () => Promise<void>;
    readonly disabled?: boolean;
    readonly maxBytes?: number;
    readonly accept?: string;
    readonly subject?: string;
    readonly deferredError?: string | null;
}

export function ExampleArtifactField({ artifact, onUpload, onRemove, onDownload, disabled = false, maxBytes = MAX_EXAMPLE_ARTIFACT_BYTES, accept, subject = "input file", deferredError }: ExampleArtifactFieldProps) {
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const shownError = error ?? deferredError;
    const inputRef = useRef<HTMLInputElement>(null);

    const run = async (action: () => Promise<void> | void) => {
        setBusy(true);
        setError(null);
        try {
            await action();
        } catch (e) {
            setError(getErrorMessage(e));
        } finally {
            setBusy(false);
        }
    };

    const upload = (file: File) => {
        if (!onUpload) return;
        const check = validateFileSizes([file], { maxSizeBytes: maxBytes });
        if (!check.valid) {
            setError(check.error ?? "File is too large.");
            return;
        }
        void run(() => onUpload(file));
    };

    if (!artifact && !onUpload) {
        return <span className="text-sm text-(--secondary-text-wMain)">—</span>;
    }

    const canPick = onUpload && !disabled;

    return (
        <div className="flex min-w-0 flex-col gap-1">
            <div className="flex min-w-0 items-center gap-1.5">
                {artifact ? (
                    <>
                        <FileBadge
                            className="max-w-full min-w-0 shrink"
                            fileName={artifact.filename}
                            title={artifactLabel(artifact)}
                            leadingIcon={<Paperclip aria-hidden />}
                            onClick={onDownload && !busy ? () => void run(onDownload) : undefined}
                            onRemove={onRemove && !disabled && !busy ? () => void run(onRemove) : undefined}
                        />
                        {canPick && (
                            <Button variant="ghost" size="sm" disabled={busy} onClick={() => inputRef.current?.click()}>
                                Replace
                            </Button>
                        )}
                    </>
                ) : (
                    <Button variant="ghost" size="sm" disabled={disabled || busy} onClick={() => inputRef.current?.click()}>
                        <Paperclip className="size-3.5" aria-hidden />
                        Attach
                    </Button>
                )}
                {canPick && (
                    <input
                        ref={inputRef}
                        type="file"
                        className="hidden"
                        accept={accept}
                        aria-label={artifact ? `Replace ${subject}` : `Attach ${subject}`}
                        onChange={event => {
                            const file = event.target.files?.[0];
                            if (file) upload(file);
                            event.target.value = "";
                        }}
                    />
                )}
            </div>

            {shownError && (
                <p className="text-xs break-words text-(--error-wMain)" role="alert">
                    {shownError}
                </p>
            )}
        </div>
    );
}
