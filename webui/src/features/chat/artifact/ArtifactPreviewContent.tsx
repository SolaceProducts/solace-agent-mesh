// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useRef, useState } from "react";
import { Loader2 } from "lucide-react";

import { useChatContext } from "@/providers/contexts";

import { isDeepResearchReportFilename } from "@/utils/deepResearchUtils";

import { MessageBanner } from "@/common";
import type { ArtifactInfo, FileAttachment } from "@/types";

import { ContentRenderer } from "../preview/ContentRenderer";
import { canPreviewArtifact, getFileContent, getRenderType } from "../preview/previewUtils";

import { ArtifactPreviewDownload } from "./ArtifactPreviewDownload";
import { ArtifactTransitionOverlay } from "./ArtifactTransitionOverlay";

const EmptyState: React.FC<{ children?: React.ReactNode }> = ({ children }) => {
    return <div className="flex h-[50vh] items-center justify-center text-(--secondary-text-wMain)">{children || "No preview available"}</div>;
};

export const ArtifactPreviewContent: React.FC<{ artifact: ArtifactInfo }> = ({ artifact }) => {
    const { openArtifactForPreview, previewFileContent, markArtifactAsDisplayed, downloadAndResolveArtifact, ragData } = useChatContext();
    const preview = useMemo(() => canPreviewArtifact(artifact), [artifact]);

    const artifactRagData = useMemo(() => {
        if (!isDeepResearchReportFilename(artifact.filename) || !ragData || ragData.length === 0) {
            return undefined;
        }

        const matchingRagData = ragData.find(r => {
            const artifactFilenameFromRag = r.metadata?.artifactFilename as string | undefined;
            return artifactFilenameFromRag === artifact.filename;
        });

        if (!matchingRagData) {
            const deepResearchRagData = ragData.filter(r => r.searchType === "deep_research");
            if (deepResearchRagData.length === 1) {
                return deepResearchRagData[0];
            }
        }

        return matchingRagData;
    }, [artifact.filename, ragData]);

    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [cachedContent, setCachedContent] = useState<FileAttachment | null>(null);
    const [isDownloading, setIsDownloading] = useState(false);

    const isFetchingRef = useRef(false);
    const lastFetchedFilenameRef = useRef<string | null>(null);
    const isDownloadingRef = useRef(false);

    useEffect(() => {
        markArtifactAsDisplayed(artifact.filename, true);

        return () => {
            markArtifactAsDisplayed(artifact.filename, false);
        };
    }, [artifact.filename, markArtifactAsDisplayed]);

    useEffect(() => {
        setIsLoading(false);
        setError(null);
        setCachedContent(null);
        isFetchingRef.current = false;
        lastFetchedFilenameRef.current = null;
    }, [artifact.filename]);

    useEffect(() => {
        if (artifact.accumulatedContent) {
            const cachedFile: FileAttachment = {
                name: artifact.filename,
                mimeType: artifact.mimeType,
                content: artifact.accumulatedContent,
                lastModified: artifact.lastModified,
                // @ts-expect-error - Add custom property to track if content is plain text
                isPlainText: artifact.isAccumulatedContentPlainText,
            };
            setCachedContent(cachedFile);
        }
    }, [artifact.accumulatedContent, artifact.filename, artifact.mimeType, artifact.lastModified, artifact.isAccumulatedContentPlainText]);

    useEffect(() => {
        async function fetchData() {
            if (isFetchingRef.current && lastFetchedFilenameRef.current === artifact.filename) {
                return;
            }

            isFetchingRef.current = true;
            lastFetchedFilenameRef.current = artifact.filename;

            try {
                setIsLoading(true);
                setError(null);

                if (!artifact.accumulatedContent) {
                    await openArtifactForPreview(artifact.filename);
                } else {
                    setIsLoading(false);
                }
            } catch (err) {
                console.error("Error fetching artifact content:", err);
                setError(err instanceof Error ? err.message : "Failed to load artifact content");
            } finally {
                setIsLoading(false);
            }
        }

        if (preview?.canPreview) {
            fetchData();
        }
    }, [artifact.filename, openArtifactForPreview, preview, artifact.accumulatedContent]);

    useEffect(() => {
        async function triggerDownload() {
            if (artifact.needsEmbedResolution && !isDownloading && !isDownloadingRef.current) {
                isDownloadingRef.current = true;
                setIsDownloading(true);
                try {
                    const resolvedContent = await downloadAndResolveArtifact(artifact.filename);
                    if (resolvedContent) {
                        setCachedContent({ ...resolvedContent, isPlainText: false } as FileAttachment & { isPlainText: boolean });
                    }
                } catch (err) {
                    console.error(`[ArtifactPreviewContent] Error downloading ${artifact.filename}:`, err);
                } finally {
                    setIsDownloading(false);
                    isDownloadingRef.current = false;
                }
            }
        }

        triggerDownload();
    }, [artifact.needsEmbedResolution, artifact.filename, downloadAndResolveArtifact, isDownloading]);

    if (error) {
        return (
            <div className="flex h-full w-full flex-col">
                <MessageBanner variant="error" message="Error rendering preview" />
                <EmptyState>No preview available</EmptyState>
            </div>
        );
    }

    if (isLoading) {
        return (
            <EmptyState>
                <Loader2 className="h-6 w-6 animate-spin text-(--secondary-text-wMain)" />
            </EmptyState>
        );
    }

    if (!preview.canPreview) {
        return <ArtifactPreviewDownload artifact={artifact} message={preview.reason ?? ""} />;
    }

    const contentSource = cachedContent || (previewFileContent?.name === artifact.filename ? previewFileContent : null);
    const effectiveMimeType = contentSource?.mimeType || artifact.mimeType;
    const rendererType = getRenderType(artifact.filename, effectiveMimeType);
    const content = getFileContent(contentSource);
    const effectiveUrl = contentSource?.url || previewFileContent?.url;

    if (!rendererType || !content) {
        return <EmptyState>No preview available</EmptyState>;
    }

    return (
        <div className="relative h-full w-full">
            <ContentRenderer
                content={content}
                rendererType={rendererType}
                mimeType={contentSource?.mimeType}
                url={effectiveUrl}
                filename={artifact.filename}
                setRenderError={setError}
                isStreaming={artifact.creationStatus === "in-progress"}
                ragData={artifactRagData}
            />
            <ArtifactTransitionOverlay isVisible={isDownloading} message="Resolving embeds..." />
        </div>
    );
};
