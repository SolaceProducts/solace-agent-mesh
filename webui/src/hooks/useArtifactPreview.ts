// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useRef, useState } from "react";

import { api } from "@/api/client";
import type { PaginatedResponse } from "@/api/common";

import type { ArtifactInfo, FileAttachment } from "@/types";
import { getArtifactContent, getArtifactUrl, getErrorMessage } from "@/utils";

export interface ArtifactPreviewState {
    filename: string | null;
    availableVersions: number[] | null;
    currentVersion: number | null;
    content: FileAttachment | null;
}

interface UseArtifactPreviewOptions {
    sessionId: string;
    projectId?: string;
    artifacts: ArtifactInfo[];
    setError?: (error: { title: string; error: string }) => void;
}

interface UseArtifactPreviewReturn {
    preview: ArtifactPreviewState;
    previewArtifact: ArtifactInfo | null;
    isLoading: boolean;

    openPreview: (filename: string, sessionIdOverride?: string) => Promise<FileAttachment | null>;
    navigateToVersion: (filename: string, version: number, sessionIdOverride?: string) => Promise<FileAttachment | null>;
    closePreview: () => void;
    setPreviewByArtifact: (artifact: ArtifactInfo | null) => void;
}

export const useArtifactPreview = ({ sessionId, projectId, artifacts, setError }: UseArtifactPreviewOptions): UseArtifactPreviewReturn => {
    const [preview, setPreview] = useState<ArtifactPreviewState>({
        filename: null,
        availableVersions: null,
        currentVersion: null,
        content: null,
    });

    const [isLoading, setIsLoading] = useState(false);

    const fetchInProgressRef = useRef<Set<string>>(new Set());

    const previewArtifact = useMemo(() => {
        if (!preview.filename) return null;
        return artifacts.find(a => a.filename === preview.filename) || null;
    }, [artifacts, preview.filename]);

    const getFileAttachment = useCallback(
        (filename: string, mimeType: string, content: string, version?: number, sessionIdOverride?: string): FileAttachment => {
            const artifactInfo = artifacts.find(a => a.filename === filename);
            const artifactUrl = getArtifactUrl({
                filename,
                sessionId: sessionIdOverride ?? sessionId,
                projectId,
                version,
            });
            return {
                name: filename,
                mimeType: mimeType,
                content: content,
                lastModified: artifactInfo?.lastModified || new Date().toISOString(),
                url: artifactUrl,
            };
        },
        [artifacts, sessionId, projectId]
    );

    const openPreview = useCallback(
        async (filename: string, sessionIdOverride?: string): Promise<FileAttachment | null> => {
            if (fetchInProgressRef.current.has(filename)) {
                return null;
            }

            const effectiveSessionId = sessionIdOverride ?? sessionId;

            fetchInProgressRef.current.add(filename);
            setIsLoading(true);

            if (preview.filename !== filename) {
                setPreview({
                    filename,
                    availableVersions: null,
                    currentVersion: null,
                    content: null,
                });
            }

            try {
                const versionsUrl = getArtifactUrl({
                    filename,
                    sessionId: effectiveSessionId,
                    projectId,
                });
                const versionsResponse: PaginatedResponse<number> = await api.webui.get(versionsUrl);
                const availableVersions = versionsResponse?.data ?? [];

                if (!availableVersions || availableVersions.length === 0) {
                    return null;
                }

                const sortedVersions = [...availableVersions].sort((a, b) => a - b);
                const latestVersion = Math.max(...availableVersions);

                const { content, mimeType } = await getArtifactContent({
                    filename,
                    sessionId: effectiveSessionId,
                    projectId,
                    version: latestVersion,
                });

                const fileData = getFileAttachment(filename, mimeType, content, latestVersion, effectiveSessionId);
                const isProjectArtifactPreview = !!projectId && (!effectiveSessionId || effectiveSessionId === "null" || effectiveSessionId === "undefined");

                setPreview({
                    filename,
                    availableVersions: isProjectArtifactPreview ? [latestVersion] : sortedVersions,
                    currentVersion: latestVersion,
                    content: fileData,
                });

                return fileData;
            } catch (error) {
                const errorMessage = getErrorMessage(error, "Failed to load artifact preview.");
                setError?.({ title: "Artifact Preview Failed", error: errorMessage });
                return null;
            } finally {
                setIsLoading(false);
                fetchInProgressRef.current.delete(filename);
            }
        },
        [sessionId, projectId, preview.filename, getFileAttachment, setError]
    );

    const navigateToVersion = useCallback(
        async (filename: string, targetVersion: number, sessionIdOverride?: string): Promise<FileAttachment | null> => {
            setIsLoading(true);

            const effectiveSessionId = sessionIdOverride ?? sessionId;

            try {
                let availableVersions = preview.availableVersions;
                if (!availableVersions?.includes(targetVersion)) {
                    const versionsUrl = getArtifactUrl({ filename, sessionId: effectiveSessionId, projectId });
                    const versionsResponse: PaginatedResponse<number> = await api.webui.get(versionsUrl);
                    availableVersions = [...(versionsResponse?.data ?? [])].sort((a, b) => a - b);
                }

                if (!availableVersions.includes(targetVersion)) {
                    console.warn(`Requested version ${targetVersion} not available for ${filename}`);
                    setError?.({
                        title: "Artifact Version Unavailable",
                        error: `Version ${targetVersion} of "${filename}" isn't available yet — it may still be processing. Try again in a moment.`,
                    });
                    return null;
                }

                const { content, mimeType } = await getArtifactContent({
                    filename,
                    sessionId: effectiveSessionId,
                    projectId,
                    version: targetVersion,
                });

                const fileData = getFileAttachment(filename, mimeType, content, targetVersion, effectiveSessionId);

                setPreview(prev => {
                    if (prev.filename !== null && prev.filename !== filename) return prev;
                    return {
                        ...prev,
                        filename,
                        availableVersions,
                        currentVersion: targetVersion,
                        content: fileData,
                    };
                });

                return fileData;
            } catch (error) {
                const errorMessage = getErrorMessage(error, "Failed to fetch artifact version.");
                setError?.({ title: "Artifact Version Preview Failed", error: errorMessage });
                return null;
            } finally {
                setIsLoading(false);
            }
        },
        [sessionId, projectId, preview.availableVersions, getFileAttachment, setError]
    );

    const closePreview = useCallback(() => {
        setPreview({
            filename: null,
            availableVersions: null,
            currentVersion: null,
            content: null,
        });
    }, []);

    const setPreviewByArtifact = useCallback(
        (artifact: ArtifactInfo | null) => {
            if (artifact) {
                if (preview.filename !== artifact.filename) {
                    setPreview({
                        filename: artifact.filename,
                        availableVersions: null,
                        currentVersion: null,
                        content: null,
                    });
                }
            } else {
                closePreview();
            }
        },
        [preview.filename, closePreview]
    );

    return {
        preview,
        previewArtifact,
        isLoading,
        openPreview,
        navigateToVersion,
        closePreview,
        setPreviewByArtifact,
    };
};
