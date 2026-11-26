// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { type ArtifactWithSession, isProjectArtifact } from "@/api/artifacts";

import { acquireFetchSlotOrAbort, releaseFetchSlot } from "@/utils/artifactFetchSemaphore";

import { Spinner } from "@/ui";
import { cn, getArtifactContent } from "@/utils";

import { AttachmentCardShell, AttachmentInlineText } from "./AttachmentCardShell";
import { getExtensionLabel, isImageType, MAX_THUMBNAIL_FILE_BYTES, supportsTextPreview } from "./attachmentUtils";
import { DocumentThumbnail, supportsThumbnail } from "./DocumentThumbnail";
import { getFileTypeColor } from "./FileIcon";

interface ArtifactAttachmentCardProps {
    artifact: ArtifactWithSession;
    onClick?: () => void;
    onRemove?: () => void;
}

function decodeBase64Snippet(base64: string): string {
    try {
        const cleaned = base64.slice(0, 1400).replace(/\s/g, "");
        const len = cleaned.length - (cleaned.length % 4);
        if (len === 0) return "";
        const binary = atob(cleaned.slice(0, len));
        const bytes = new Uint8Array(binary.length);
        for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
        return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
    } catch {
        return "";
    }
}

export const ArtifactAttachmentCard: React.FC<ArtifactAttachmentCardProps> = ({ artifact, onClick, onRemove }) => {
    const tooLargeToThumb = typeof artifact.size === "number" && artifact.size > MAX_THUMBNAIL_FILE_BYTES;

    const isImage = !tooLargeToThumb && isImageType(artifact.mimeType, artifact.filename);
    const isDoc = !tooLargeToThumb && supportsThumbnail(artifact.filename, artifact.mimeType);
    const isText = !tooLargeToThumb && !isDoc && !isImage && supportsTextPreview(artifact.mimeType, artifact.filename);
    const needsFetch = isImage || isDoc || isText;

    const projectId = isProjectArtifact(artifact) && artifact.projectId ? artifact.projectId : undefined;
    const sessionId = projectId ? undefined : artifact.sessionId;

    const { data, isLoading, isError } = useQuery({
        queryKey: ["artifact-attachment-preview", projectId ?? null, sessionId ?? null, artifact.filename, "latest"],
        queryFn: async ({ signal }) => {
            const acquired = await acquireFetchSlotOrAbort(signal);
            if (!acquired) throw new Error("aborted");
            try {
                return await getArtifactContent({ filename: artifact.filename, sessionId, projectId, version: "latest" });
            } finally {
                releaseFetchSlot();
            }
        },
        enabled: needsFetch,
        staleTime: 5 * 60 * 1000,
        retry: 1,
    });

    const textSnippet = useMemo(() => {
        if (!isText || !data?.content) return null;
        const decoded = decodeBase64Snippet(data.content);
        if (!decoded) return null;
        const lines = decoded.split("\n").map(line => line.trimEnd());
        return lines.join("\n");
    }, [isText, data?.content]);

    const imageDataUrl = useMemo(() => {
        if (!isImage || !data?.content) return null;
        return `data:${data.mimeType || artifact.mimeType};base64,${data.content}`;
    }, [isImage, data, artifact.mimeType]);

    const [docFailed, setDocFailed] = useState(false);
    useEffect(() => {
        setDocFailed(false);
    }, [artifact.filename, artifact.sessionId, artifact.projectId]);

    const inlineText = isText && !!textSnippet;
    const textLines = useMemo(() => (textSnippet ? textSnippet.split("\n") : []), [textSnippet]);

    const preview = (() => {
        if (needsFetch && isLoading) {
            return (
                <div className="flex h-full items-center justify-center">
                    <Spinner size="small" variant="muted" />
                </div>
            );
        }

        if (isImage && imageDataUrl && !isError) {
            return <img src={imageDataUrl} alt={artifact.filename} className="h-full w-full object-cover" />;
        }

        if (isDoc && data?.content && !isError && !docFailed) {
            return <DocumentThumbnail content={data.content} filename={artifact.filename} mimeType={artifact.mimeType} width={200} height={80} className="h-full w-full" onError={() => setDocFailed(true)} />;
        }

        return (
            <div className="flex h-full w-full items-center justify-center">
                <span className={cn("rounded px-2 py-0.5 text-[10px] font-bold text-(--darkSurface-text)", getFileTypeColor(artifact.mimeType, artifact.filename))}>{getExtensionLabel(artifact.filename)}</span>
            </div>
        );
    })();

    const scopeLabel = artifact.projectName || artifact.sessionName;
    const tooltipText = `Preview ${artifact.filename}${scopeLabel ? ` · ${scopeLabel}` : ""}`;

    return (
        <AttachmentCardShell
            filename={artifact.filename}
            preview={preview}
            inlineText={inlineText ? <AttachmentInlineText lines={textLines} /> : undefined}
            tooltipText={tooltipText}
            onClick={onClick}
            onRemove={onRemove}
            removeTooltip="Remove attached artifact"
        />
    );
};
