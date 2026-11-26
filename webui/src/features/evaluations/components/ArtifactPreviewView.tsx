// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Loader2 } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { getErrorMessage } from "@/utils/api";
import { cn } from "@/utils/cnTailwind";
import { downloadBlob } from "@/utils/download";
import { formatBytes } from "@/utils/format";

import { Button } from "@/ui/button";
import type { EvalArtifact } from "@/types/eval";

import { ArtifactDetails } from "@/features/chat/artifact/ArtifactDetails";
import { ContentRenderer } from "@/features/chat/preview";

import { ConfirmationDialog } from "@/common";

import { coerceArtifactVersion, evalArtifactRenderType } from "../utils";

const BASE64_RENDERER_TYPES = new Set(["image", "audio", "docx", "pptx", "xlsx"]);
const URL_ONLY_RENDERER_TYPES = new Set(["pdf"]);

interface ArtifactPreviewViewProps {
    readonly artifact: EvalArtifact;
    readonly fetchBlob: () => Promise<Blob>;
    readonly cacheKey: readonly unknown[];
    readonly lastModified?: string;
    readonly onClose: () => void;
    readonly onDelete?: () => Promise<void> | void;
}

export function ArtifactPreviewView({ artifact, fetchBlob, cacheKey, lastModified, onClose, onDelete }: ArtifactPreviewViewProps) {
    const { displayError } = useChatContext();
    const [detailsExpanded, setDetailsExpanded] = useState(false);
    const queryClient = useQueryClient();
    const [deleteOpen, setDeleteOpen] = useState(false);
    const [deleting, setDeleting] = useState(false);

    const artifactInfo = useMemo(
        () => ({
            filename: artifact.filename,
            mimeType: artifact.mimeType ?? "",
            size: artifact.size ?? 0,
            lastModified: lastModified ?? "",
            version: coerceArtifactVersion(artifact.version),
        }),
        [artifact.filename, artifact.mimeType, artifact.size, lastModified, artifact.version]
    );

    const handleDownload = async () => {
        try {
            const blob = await queryClient.fetchQuery({ queryKey: cacheKey, queryFn: fetchBlob });
            downloadBlob(blob, artifact.filename);
        } catch (e) {
            displayError({ title: "Download failed", error: getErrorMessage(e), errorSource: "api" });
        }
    };

    const handleDelete = onDelete ? () => setDeleteOpen(true) : undefined;

    return (
        <div className="flex h-full min-h-0 flex-col bg-(--background-w10)">
            <div className="flex shrink-0 items-center gap-2 border-b px-4 py-3">
                <Button variant="ghost" size="sm" onClick={onClose} title="Back">
                    <ArrowLeft className="size-4" aria-hidden />
                </Button>
                <span className="text-sm font-semibold text-(--primary-text-wMain)">Preview</span>
            </div>

            <div className="shrink-0 border-b px-4 py-3">
                <ArtifactDetails artifactInfo={artifactInfo} isPreview isExpanded={detailsExpanded} setIsExpanded={setDetailsExpanded} onDownload={handleDownload} onDelete={handleDelete} />
            </div>

            {detailsExpanded && (
                <div className="shrink-0 border-b bg-(--background-w20) px-4 py-3 text-sm">
                    <dl className="grid grid-cols-2 gap-x-4 gap-y-3">
                        <div className="col-span-2">
                            <dt className="text-xs text-(--secondary-text-wMain)">Description</dt>
                            <dd className="mt-0.5 text-(--primary-text-wMain)">—</dd>
                        </div>
                        <div>
                            <dt className="text-xs text-(--secondary-text-wMain)">Size</dt>
                            <dd className="mt-0.5 text-(--primary-text-wMain)">{artifact.size != null ? formatBytes(artifact.size) : "—"}</dd>
                        </div>
                        <div>
                            <dt className="text-xs text-(--secondary-text-wMain)">Type</dt>
                            <dd className="mt-0.5 truncate text-(--primary-text-wMain)" title={artifact.mimeType}>
                                {artifact.mimeType ?? "—"}
                            </dd>
                        </div>
                    </dl>
                </div>
            )}

            <div className="min-h-0 flex-1 overflow-auto p-4">
                <ArtifactPreviewBody artifact={artifact} fetchBlob={fetchBlob} cacheKey={cacheKey} />
            </div>

            {onDelete && (
                <ConfirmationDialog
                    open={deleteOpen}
                    onOpenChange={open => {
                        if (!open && !deleting) setDeleteOpen(false);
                    }}
                    title="Delete File"
                    content={
                        <>
                            This action cannot be undone. This file will be permanently deleted: <strong>{artifact.filename}</strong>.
                        </>
                    }
                    actionLabels={{ confirm: "Delete" }}
                    isLoading={deleting}
                    onCancel={() => setDeleteOpen(false)}
                    onConfirm={async () => {
                        setDeleting(true);
                        try {
                            await onDelete();
                        } finally {
                            setDeleting(false);
                            setDeleteOpen(false);
                        }
                    }}
                />
            )}
        </div>
    );
}

interface ArtifactPreviewBodyProps {
    readonly artifact: EvalArtifact;
    readonly fetchBlob: () => Promise<Blob>;
    readonly cacheKey: readonly unknown[];
}

function ArtifactPreviewBody({ artifact, fetchBlob, cacheKey }: ArtifactPreviewBodyProps) {
    const rendererType = useMemo(() => evalArtifactRenderType(artifact.filename, artifact.mimeType), [artifact.filename, artifact.mimeType]);

    const {
        data: blob,
        isPending: fetching,
        error: fetchError,
    } = useQuery({
        queryKey: cacheKey,
        queryFn: fetchBlob,
        enabled: rendererType !== null,
    });

    const [transformed, setTransformed] = useState<{ content: string; url: string } | null>(null);
    const [renderError, setRenderError] = useState<string | null>(null);

    useEffect(() => {
        if (!blob || !rendererType) {
            setTransformed(null);
            return;
        }
        const url = URL.createObjectURL(blob);
        let cancelled = false;
        setRenderError(null);

        void (async () => {
            try {
                let content: string;
                if (URL_ONLY_RENDERER_TYPES.has(rendererType)) {
                    content = "url-based-content";
                } else if (BASE64_RENDERER_TYPES.has(rendererType)) {
                    const buf = await blob.arrayBuffer();
                    const bytes = new Uint8Array(buf);
                    let binary = "";
                    const chunkSize = 0x8000;
                    for (let i = 0; i < bytes.length; i += chunkSize) {
                        binary += String.fromCharCode.apply(null, bytes.subarray(i, i + chunkSize) as unknown as number[]);
                    }
                    content = btoa(binary);
                } else {
                    content = await blob.text();
                }
                if (!cancelled) setTransformed({ content, url });
            } catch (e) {
                if (!cancelled) setRenderError(e instanceof Error ? e.message : "Failed to read artifact content.");
            }
        })();

        return () => {
            cancelled = true;
            URL.revokeObjectURL(url);
        };
    }, [blob, rendererType]);

    if (!rendererType) {
        return <div className={cn("flex h-full items-center justify-center text-sm text-(--secondary-text-wMain)")}>No preview available for this file type.</div>;
    }
    if (fetching || (blob && !transformed && !renderError)) {
        return (
            <div className="flex h-full items-center justify-center">
                <Loader2 className="size-6 animate-spin text-(--secondary-text-wMain)" aria-hidden />
            </div>
        );
    }
    if (fetchError) {
        return <div className="text-sm text-(--error-wMain)">Failed to load: {fetchError instanceof Error ? fetchError.message : String(fetchError)}</div>;
    }
    if (renderError) {
        return <div className="text-sm text-(--error-wMain)">{renderError}</div>;
    }
    if (!transformed) return null;

    return <ContentRenderer content={transformed.content} rendererType={rendererType} mimeType={artifact.mimeType} url={transformed.url} filename={artifact.filename} setRenderError={setRenderError} />;
}
