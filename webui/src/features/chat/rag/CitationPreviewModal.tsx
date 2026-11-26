// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo, useRef, useState } from "react";
import DOMPurify from "dompurify";

import { useSessionArtifactContent } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts";

import { useScrollToHighlight } from "@/hooks/useScrollToHighlight";

import { getArtifactUrl, validIdOrUndefined } from "@/utils/file";
import { highlightCitationsInText } from "@/utils/highlightUtils";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, VisuallyHidden } from "@/ui/dialog";

import { FileIcon } from "@/features/chat/file/FileIcon";
import { ContentRenderer } from "@/features/chat/preview/ContentRenderer";
import { NoPreviewState } from "@/features/chat/preview/PreviewStates";
import { decodeBase64Content, getRenderType } from "@/features/chat/preview/previewUtils";
import type { CitationMapEntry } from "@/features/chat/preview/renderers/PdfRenderer";

import { EmptyState, MessageBanner } from "@/common";
import type { RAGSource } from "@/types";

export interface CitationPreviewModalProps {
    isOpen: boolean;
    onClose: () => void;
    filename: string;
    locationLabel: string;
    initialLocation: number;
    citations: RAGSource[];
    fileExtension?: string;
}

export const CitationPreviewModal: React.FC<CitationPreviewModalProps> = ({ isOpen, onClose, filename, locationLabel, initialLocation, citations }) => {
    const { sessionId } = useChatContext();

    const [renderError, setRenderError] = useState<string | null>(null);
    const textContentRef = useRef<HTMLDivElement>(null);

    const fileVersion: number | undefined = citations[0]?.metadata?.file_version ?? undefined;

    const { data: artifactData, isLoading, error: fetchError } = useSessionArtifactContent(isOpen ? sessionId : null, isOpen ? filename : null, fileVersion);

    const renderType = (() => {
        const lowerFilename = filename.toLowerCase();
        if (lowerFilename.endsWith(".pdf")) return "pdf";
        return getRenderType(filename, artifactData?.mimeType);
    })();

    const fileUrl = useMemo(() => {
        if (!validIdOrUndefined(sessionId)) return null;
        return getArtifactUrl({ filename, sessionId, version: fileVersion ?? "latest" });
    }, [filename, sessionId, fileVersion]);

    const citationMaps = useMemo((): CitationMapEntry[] => {
        return citations.flatMap(c => (c.metadata?.citation_map as CitationMapEntry[]) || []);
    }, [citations]);

    const processedContent = useMemo(() => {
        if (!artifactData?.content || !renderType) return "";
        if (renderType === "pdf") return "";

        if (["docx", "pptx", "xlsx"].includes(renderType)) {
            return artifactData.content;
        }

        let decodedContent: string;
        try {
            decodedContent = decodeBase64Content(artifactData.content);
        } catch {
            return artifactData.content;
        }

        if (["text", "markdown"].includes(renderType)) {
            const highlighted = highlightCitationsInText(decodedContent, citations);
            return DOMPurify.sanitize(highlighted, { ALLOWED_TAGS: ["mark"], ALLOWED_ATTR: ["class"] });
        }

        return decodedContent;
    }, [artifactData?.content, renderType, citations]);

    const error = fetchError || renderError;

    const shouldScrollText = !isLoading && !error && (renderType === "text" || renderType === "markdown") && isOpen;
    useScrollToHighlight(textContentRef, "mark", shouldScrollText, [isOpen, processedContent, citations]);

    return (
        <Dialog open={isOpen} onOpenChange={open => !open && onClose()}>
            <DialogContent className="flex h-[80vh] min-w-[60vw] flex-col">
                <DialogHeader className="flex-none border-b pb-4">
                    <div className="flex items-center justify-between">
                        <div className="flex items-center gap-3">
                            <FileIcon filename={filename} variant="compact" />
                            <div className="flex flex-col gap-0.5">
                                <DialogTitle className="text-base font-semibold">{filename}</DialogTitle>
                                <DialogDescription className="flex items-center gap-1.5 text-sm text-(--secondary-text-wMain)">{locationLabel}</DialogDescription>
                            </div>
                        </div>
                    </div>
                </DialogHeader>

                <div className="relative flex-1 overflow-hidden">
                    {isLoading && <EmptyState variant="loading" title="Loading document..." />}

                    {error && <EmptyState variant="error" title={error instanceof Error ? error.message : String(error)} />}

                    {!isLoading && !error && renderType && (
                        <div className="h-full overflow-auto">
                            {renderType === "pdf" ? (
                                fileUrl ? (
                                    <ContentRenderer
                                        content=""
                                        rendererType={renderType}
                                        mimeType={artifactData?.mimeType}
                                        url={fileUrl}
                                        filename={filename}
                                        setRenderError={setRenderError}
                                        initialPage={initialLocation}
                                        citationMaps={citationMaps}
                                        disableInteractionModes={true}
                                    />
                                ) : (
                                    <MessageBanner variant="warning" message="Unable to preview PDF: No active session" />
                                )
                            ) : renderType === "text" || renderType === "markdown" ? (
                                <div ref={textContentRef} className="p-4">
                                    <pre className="whitespace-pre-wrap select-text focus-visible:outline-none" style={{ overflowWrap: "anywhere" }} dangerouslySetInnerHTML={{ __html: processedContent }} />
                                </div>
                            ) : (
                                <ContentRenderer content={processedContent} rendererType={renderType} mimeType={artifactData?.mimeType} setRenderError={setRenderError} />
                            )}
                        </div>
                    )}

                    {!isLoading && !error && !renderType && <NoPreviewState />}
                </div>

                <DialogFooter className="flex-none border-t pt-4">
                    <p className="flex-1 text-xs text-(--secondary-text-wMain)">This is a preview of the document containing the citation</p>
                    <Button variant="outline" onClick={onClose}>
                        Close
                    </Button>
                </DialogFooter>

                <VisuallyHidden>
                    <DialogDescription>
                        Preview of {filename} showing citation from {locationLabel}
                    </DialogDescription>
                </VisuallyHidden>
            </DialogContent>
        </Dialog>
    );
};
