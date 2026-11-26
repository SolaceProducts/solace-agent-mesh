// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";

import { Spinner } from "@/ui";
import { cn } from "@/utils";

import { AttachmentCardShell, AttachmentInlineText } from "./AttachmentCardShell";
import { getExtensionLabel, isImageType, MAX_THUMBNAIL_FILE_BYTES, supportsTextPreview } from "./attachmentUtils";
import { DocumentThumbnail, supportsThumbnail } from "./DocumentThumbnail";
import { getFileTypeColor } from "./FileIcon";

const BLOB_REVOKE_DELAY_MS = 60_000;

interface FileUploadCardProps {
    file: File;
    onRemove?: () => void;
    onClick?: () => void;
}

export const FileUploadCard: React.FC<FileUploadCardProps> = ({ file, onRemove, onClick }) => {
    const tooSmallToThumb = file.size === 0;
    const tooLargeToThumb = file.size > MAX_THUMBNAIL_FILE_BYTES;

    const isImage = !tooLargeToThumb && isImageType(file.type, file.name);
    const isDoc = !tooLargeToThumb && supportsThumbnail(file.name, file.type);
    const isText = !tooLargeToThumb && !tooSmallToThumb && !isDoc && !isImage && supportsTextPreview(file.type, file.name);

    const imageObjectUrl = useMemo(() => (isImage ? URL.createObjectURL(file) : null), [isImage, file]);
    useEffect(() => {
        return () => {
            if (imageObjectUrl) URL.revokeObjectURL(imageObjectUrl);
        };
    }, [imageObjectUrl]);

    const [docBase64, setDocBase64] = useState<string | null>(null);
    const [docLoading, setDocLoading] = useState(false);
    const [docFailed, setDocFailed] = useState(false);
    useEffect(() => {
        if (!isDoc) return;
        let cancelled = false;
        setDocLoading(true);
        setDocFailed(false);
        const reader = new FileReader();
        reader.onload = () => {
            if (cancelled) return;
            const result = reader.result as string;
            const commaIdx = result.indexOf(",");
            setDocBase64(commaIdx >= 0 ? result.substring(commaIdx + 1) : result);
            setDocLoading(false);
        };
        reader.onerror = () => {
            if (!cancelled) setDocLoading(false);
        };
        reader.readAsDataURL(file);
        return () => {
            cancelled = true;
            reader.abort();
        };
    }, [isDoc, file]);

    const [textSnippet, setTextSnippet] = useState<string | null>(null);
    const [textLoading, setTextLoading] = useState(false);
    useEffect(() => {
        if (!isText) return;
        let cancelled = false;
        setTextLoading(true);
        file.slice(0, 2048)
            .text()
            .then(text => {
                if (!cancelled) {
                    setTextSnippet(text);
                    setTextLoading(false);
                }
            })
            .catch(() => {
                if (!cancelled) setTextLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [isText, file]);

    const textLines = useMemo(() => (textSnippet ? textSnippet.split("\n").map(l => l.trimEnd()) : []), [textSnippet]);
    const inlineText = isText && textLines.length > 0;

    const openBlobInNewTab = () => {
        const url = URL.createObjectURL(file);
        const tab = window.open(url, "_blank", "noopener,noreferrer");
        window.setTimeout(() => URL.revokeObjectURL(url), BLOB_REVOKE_DELAY_MS);
        if (!tab) URL.revokeObjectURL(url);
    };
    const handleClick = onClick ?? openBlobInNewTab;

    const preview = (() => {
        if (isImage && imageObjectUrl) {
            return <img src={imageObjectUrl} alt={file.name} className="h-full w-full object-cover" />;
        }

        if (isDoc && !docFailed) {
            if (docLoading || !docBase64) {
                return (
                    <div className="flex h-full items-center justify-center">
                        <Spinner size="small" variant="muted" />
                    </div>
                );
            }
            return <DocumentThumbnail content={docBase64} filename={file.name} mimeType={file.type} width={200} height={80} className="h-full w-full" onError={() => setDocFailed(true)} />;
        }

        if (isText && textLoading) {
            return (
                <div className="flex h-full items-center justify-center">
                    <Spinner size="small" variant="muted" />
                </div>
            );
        }

        return (
            <div className="flex h-full w-full items-center justify-center">
                <span className={cn("rounded px-2 py-0.5 text-[10px] font-bold text-(--darkSurface-text)", getFileTypeColor(file.type, file.name))}>{getExtensionLabel(file.name)}</span>
            </div>
        );
    })();

    return <AttachmentCardShell filename={file.name} preview={preview} inlineText={inlineText ? <AttachmentInlineText lines={textLines} /> : undefined} onClick={handleClick} onRemove={onRemove} removeTooltip="Remove file" />;
};
