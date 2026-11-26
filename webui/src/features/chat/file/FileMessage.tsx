// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type MouseEvent, useCallback, useContext, useMemo } from "react";
import { Download, Eye } from "lucide-react";

import { useSessionArtifacts } from "@/api/artifacts";

import { ChatContext, type ChatContextValue } from "@/providers/contexts/ChatContext";

import type { ArtifactInfo } from "@/types";
import { Button } from "@/ui";

import { getFileIcon } from "./fileUtils";

interface FileMessageProps {
    filename: string;
    mimeType?: string;
    className?: string;
    onDownload?: () => void;
    isEmbedded?: boolean;
    readOnly?: boolean;
}

export const FileMessage = ({ filename, mimeType, className, onDownload, isEmbedded = false, readOnly = false }: Readonly<FileMessageProps>) => {
    const chatContext = useContext(ChatContext) as ChatContextValue | undefined;
    const hasContext = chatContext !== undefined && !readOnly;

    const isSharedSession = hasContext ? chatContext.isSharedSession : false;
    const { data: cachedArtifacts } = useSessionArtifacts(hasContext ? chatContext.sessionId : undefined, { networkFetch: !isSharedSession });
    const artifacts = cachedArtifacts ?? [];
    const setPreviewArtifact = hasContext ? chatContext.setPreviewArtifact : undefined;
    const openSidePanelTab = hasContext ? chatContext.openSidePanelTab : undefined;

    const artifact: ArtifactInfo | undefined = useMemo(() => artifacts.find((a: ArtifactInfo) => a.filename === filename), [artifacts, filename]);
    const FileIcon = useMemo(() => getFileIcon(artifact || { filename, mimeType: mimeType || "", size: 0, lastModified: "" }), [artifact, filename, mimeType]);

    const handlePreviewClick = useCallback(
        (e: MouseEvent) => {
            e.stopPropagation();
            if (artifact && setPreviewArtifact && openSidePanelTab) {
                openSidePanelTab("files");
                setPreviewArtifact(artifact);
            }
        },
        [artifact, openSidePanelTab, setPreviewArtifact]
    );

    const handleDownloadClick = useCallback(() => {
        if (onDownload) {
            onDownload();
        }
    }, [onDownload]);

    return (
        <div className={`flex h-11 max-w-xs flex-shrink items-center gap-2 rounded-lg bg-(--secondary-w20) px-2 py-1 ${className || ""}`}>
            {FileIcon}
            <span className="min-w-0 flex-1 truncate text-sm leading-9" title={filename}>
                <strong>
                    <code>{filename}</code>
                </strong>
            </span>

            {artifact && !isEmbedded && !readOnly && setPreviewArtifact && (
                <Button variant="ghost" onClick={handlePreviewClick} tooltip="Preview">
                    <Eye className="h-4 w-4" />
                </Button>
            )}

            {onDownload && (
                <Button variant="ghost" onClick={handleDownloadClick} tooltip="Download file">
                    <Download className="h-4 w-4" />
                </Button>
            )}
        </div>
    );
};
