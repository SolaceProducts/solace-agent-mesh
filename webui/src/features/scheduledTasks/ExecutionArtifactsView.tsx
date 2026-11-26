// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";
import { ArrowLeft, Download, Loader2 } from "lucide-react";

import { useExecutionArtifacts } from "@/api/scheduled-tasks";

import { downloadFile } from "@/utils/download";
import { getArtifactContent, getArtifactUrl } from "@/utils/file";

import { ArtifactBar } from "@/features/chat/artifact/ArtifactBar";
import { ContentRenderer } from "@/features/chat/preview/ContentRenderer";
import { canPreviewArtifact, getFileContent, getRenderType } from "@/features/chat/preview/previewUtils";

import { MessageBanner } from "@/common";
import type { ArtifactInfo, FileAttachment } from "@/types";
import { Button } from "@/ui";

interface ExecutionArtifactsViewProps {
    sessionId: string | null | undefined;
}

export const ExecutionArtifactsView: React.FC<ExecutionArtifactsViewProps> = ({ sessionId }) => {
    const { data: artifacts, isLoading, error } = useExecutionArtifacts(sessionId);
    const [previewArtifact, setPreviewArtifact] = useState<ArtifactInfo | null>(null);

    const handleDownload = async (artifact: ArtifactInfo) => {
        if (!sessionId) return;
        await downloadFile({ name: artifact.filename, mimeType: artifact.mimeType, size: artifact.size, uri: artifact.uri }, sessionId);
    };

    if (isLoading) {
        return (
            <div className="flex h-32 items-center justify-center">
                <Loader2 className="h-5 w-5 animate-spin text-(--secondary-text-wMain)" />
            </div>
        );
    }

    if (error) {
        return <MessageBanner variant="error" message={error instanceof Error ? error.message : "Failed to load artifacts"} />;
    }

    if (previewArtifact && sessionId) {
        return (
            <div className="flex h-full flex-col">
                <div className="mb-3 flex items-center gap-2">
                    <Button variant="ghost" size="icon" onClick={() => setPreviewArtifact(null)} aria-label="Back to artifact list">
                        <ArrowLeft className="h-4 w-4" />
                    </Button>
                    <div className="text-base font-semibold">Preview {previewArtifact.filename}</div>
                </div>
                <div className="flex-1 overflow-hidden rounded-md border bg-(--background-w10)">
                    <ArtifactPreviewBody artifact={previewArtifact} sessionId={sessionId} onDownload={() => handleDownload(previewArtifact)} />
                </div>
            </div>
        );
    }

    if (!artifacts || artifacts.length === 0) {
        return <div className="rounded-md border bg-(--background-w10) p-6 text-sm text-(--secondary-text-wMain) italic">No artifacts produced by this execution.</div>;
    }

    return (
        <div className="space-y-2">
            {artifacts.map(artifact => {
                const artifactKey = artifact.uri ?? `${artifact.filename}@${artifact.version ?? "latest"}`;
                return (
                    <div key={artifactKey} className="overflow-hidden rounded-md border">
                        <ArtifactBar
                            filename={artifact.filename}
                            mimeType={artifact.mimeType}
                            size={artifact.size}
                            status="completed"
                            context="chat"
                            actions={{
                                onPreview: () => setPreviewArtifact(artifact),
                                onDownload: () => handleDownload(artifact),
                            }}
                        />
                    </div>
                );
            })}
        </div>
    );
};

export const ExecutionInlineArtifacts: React.FC<{ sessionId: string | null | undefined }> = ({ sessionId }) => {
    const { data: artifacts, isLoading, error } = useExecutionArtifacts(sessionId);
    const [expanded, setExpanded] = useState<Record<string, boolean>>({});

    const handleDownload = async (artifact: ArtifactInfo) => {
        if (!sessionId) return;
        await downloadFile({ name: artifact.filename, mimeType: artifact.mimeType, size: artifact.size, uri: artifact.uri }, sessionId);
    };

    if (isLoading) {
        return (
            <div className="flex h-24 items-center justify-center">
                <Loader2 className="h-4 w-4 animate-spin text-(--secondary-text-wMain)" />
            </div>
        );
    }

    if (error) {
        return <MessageBanner variant="error" message={error instanceof Error ? error.message : "Failed to load artifacts"} />;
    }

    if (!artifacts || artifacts.length === 0) return null;

    return (
        <div className="space-y-2">
            {artifacts.map(artifact => {
                const artifactKey = artifact.uri ?? `${artifact.filename}@${artifact.version ?? "latest"}`;
                const isExpanded = !!expanded[artifactKey];
                const expandedBody = isExpanded ? (
                    <div className="border-t bg-(--background-w10) p-4">
                        <ArtifactPreviewBody artifact={artifact} sessionId={sessionId!} onDownload={() => handleDownload(artifact)} />
                    </div>
                ) : null;
                return (
                    <div key={artifactKey} className="overflow-hidden rounded-md border border-(--secondary-w40)">
                        <ArtifactBar
                            filename={artifact.filename}
                            mimeType={artifact.mimeType}
                            size={artifact.size}
                            status="completed"
                            context="chat"
                            expandable
                            expanded={isExpanded}
                            onToggleExpand={() => setExpanded(prev => ({ ...prev, [artifactKey]: !prev[artifactKey] }))}
                            expandedContent={expandedBody}
                            actions={{
                                onDownload: () => handleDownload(artifact),
                            }}
                        />
                    </div>
                );
            })}
        </div>
    );
};

export const ArtifactPreviewBody: React.FC<{ artifact: ArtifactInfo; sessionId: string; onDownload: () => void }> = ({ artifact, sessionId, onDownload }) => {
    const preview = useMemo(() => canPreviewArtifact(artifact), [artifact]);
    const [content, setContent] = useState<FileAttachment | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const artifactVersion: number | "latest" = artifact.version ?? "latest";

    useEffect(() => {
        if (!preview.canPreview) return;
        let cancelled = false;
        setIsLoading(true);
        setError(null);
        getArtifactContent({ filename: artifact.filename, sessionId, version: artifactVersion })
            .then(({ content: base64, mimeType }) => {
                if (cancelled) return;
                setContent({ name: artifact.filename, mimeType, content: base64 });
            })
            .catch(err => {
                if (cancelled) return;
                setError(err instanceof Error ? err.message : "Failed to load artifact content");
            })
            .finally(() => {
                if (!cancelled) setIsLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [artifact.filename, artifactVersion, preview.canPreview, sessionId]);

    if (!preview.canPreview) {
        return <PreviewUnavailable message={preview.reason ?? ""} onDownload={onDownload} />;
    }

    if (isLoading) {
        return (
            <div className="flex min-h-32 items-center justify-center p-8">
                <Loader2 className="h-6 w-6 animate-spin text-(--secondary-text-wMain)" />
            </div>
        );
    }

    if (error) {
        return (
            <div className="flex min-h-32 flex-col">
                <MessageBanner variant="error" message="Error rendering preview" />
                <div className="flex flex-1 items-center justify-center p-8 text-sm text-(--secondary-text-wMain)">{error}</div>
            </div>
        );
    }

    const effectiveMime = content?.mimeType || artifact.mimeType;
    const rendererType = getRenderType(artifact.filename, effectiveMime);
    const fileContent = getFileContent(content);
    const previewUrl = rendererType === "pdf" ? getArtifactUrl({ filename: artifact.filename, sessionId, version: artifactVersion }) : undefined;

    if (!rendererType || !fileContent) {
        return <PreviewUnavailable message="No preview available" onDownload={onDownload} />;
    }

    return (
        <div className="h-full w-full overflow-auto p-4">
            <ContentRenderer content={fileContent} rendererType={rendererType} mimeType={effectiveMime} url={previewUrl} filename={artifact.filename} setRenderError={setError} />
        </div>
    );
};

const PreviewUnavailable: React.FC<{ message: string; onDownload: () => void }> = ({ message, onDownload }) => (
    <div className="flex min-h-32 w-full flex-col items-center justify-center gap-2 p-4">
        <div className="mb-1 font-semibold">Preview Unavailable</div>
        <div className="text-sm text-(--secondary-text-wMain)">{message}</div>
        <Button onClick={onDownload}>
            <Download className="h-4 w-4" />
            Download
        </Button>
    </div>
);
