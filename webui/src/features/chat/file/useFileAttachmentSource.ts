// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import { type ArtifactWithSession, isProjectArtifact } from "@/api/artifacts";
import { api } from "@/api/client";
import type { PaginatedResponse } from "@/api/common";

import { canPreviewArtifact, getRenderType } from "@/features/chat/preview/previewUtils";

import type { FileAttachment } from "@/types";
import { getArtifactContent, getArtifactUrl } from "@/utils";

const PREVIEW_CACHE_MS = 5 * 60 * 1000;

export interface FileAttachmentSourceState {
    fileContent: FileAttachment | null;
    isLoading: boolean;
    error: string | null;
    setError: (err: string | null) => void;
    canPreview: boolean;
    rendererType: ReturnType<typeof getRenderType>;
}

export interface ArtifactSourceState extends FileAttachmentSourceState {
    availableVersions: number[];
    currentVersion: number | null;
    setCurrentVersion: (v: number) => void;
}

export function useLocalFileSource(file: File): FileAttachmentSourceState {
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [fileContent, setFileContent] = useState<FileAttachment | null>(null);

    const rendererType = useMemo(() => getRenderType(file.name, file.type), [file.name, file.type]);
    const canPreview = !!rendererType;

    const blobUrl = useMemo(() => URL.createObjectURL(file), [file]);
    useEffect(() => () => URL.revokeObjectURL(blobUrl), [blobUrl]);

    useEffect(() => {
        if (!canPreview) {
            setIsLoading(false);
            return;
        }

        let cancelled = false;
        setIsLoading(true);
        setError(null);

        const reader = new FileReader();
        reader.onload = () => {
            if (cancelled) return;
            const result = reader.result as string;
            const commaIdx = result.indexOf(",");
            const base64 = commaIdx >= 0 ? result.substring(commaIdx + 1) : result;

            setFileContent({
                name: file.name,
                mimeType: file.type || "application/octet-stream",
                content: base64,
                lastModified: new Date(file.lastModified).toISOString(),
                size: file.size,
                url: blobUrl,
            });
            setIsLoading(false);
        };
        reader.onerror = () => {
            if (cancelled) return;
            setError("Failed to read file");
            setIsLoading(false);
        };
        reader.readAsDataURL(file);

        return () => {
            cancelled = true;
            reader.abort();
        };
    }, [file, blobUrl, canPreview]);

    return { fileContent, isLoading, error, setError, canPreview, rendererType };
}

export function useArtifactSource(artifact: ArtifactWithSession): ArtifactSourceState {
    const queryClient = useQueryClient();
    const [renderError, setRenderError] = useState<string | null>(null);
    const [currentVersion, setCurrentVersion] = useState<number | null>(null);

    const projectId = isProjectArtifact(artifact) && artifact.projectId ? artifact.projectId : undefined;
    const sessionId = projectId ? undefined : artifact.sessionId;

    const preview = useMemo(() => canPreviewArtifact(artifact), [artifact]);
    const canPreview = !!preview?.canPreview;
    const rendererType = useMemo(() => getRenderType(artifact.filename, artifact.mimeType), [artifact.filename, artifact.mimeType]);

    useEffect(() => {
        setCurrentVersion(null);
        setRenderError(null);
    }, [artifact.filename, sessionId, projectId]);

    const versionsQuery = useQuery({
        queryKey: ["artifact-versions", projectId ?? null, sessionId ?? null, artifact.filename],
        queryFn: async (): Promise<number[]> => {
            const url = getArtifactUrl({ filename: artifact.filename, sessionId, projectId });
            const response: PaginatedResponse<number> = await api.webui.get(url);
            return [...(response?.data ?? [])].sort((a, b) => b - a);
        },
        enabled: canPreview,
        staleTime: PREVIEW_CACHE_MS,
    });

    const availableVersions = useMemo(() => versionsQuery.data ?? [], [versionsQuery.data]);

    useEffect(() => {
        if (currentVersion === null && availableVersions.length > 0) {
            setCurrentVersion(availableVersions[0]);
        }
    }, [availableVersions, currentVersion]);

    const contentQuery = useQuery({
        queryKey: ["artifact-content", projectId ?? null, sessionId ?? null, artifact.filename, currentVersion],
        queryFn: () => getArtifactContent({ filename: artifact.filename, sessionId, projectId, version: currentVersion ?? undefined }),
        enabled: canPreview && currentVersion !== null,
        staleTime: PREVIEW_CACHE_MS,
    });

    useEffect(() => {
        if (currentVersion === null || availableVersions.length === 0) return;
        const idx = availableVersions.indexOf(currentVersion);
        const adjacent = [availableVersions[idx - 1], availableVersions[idx + 1]].filter((v): v is number => v !== undefined && v !== currentVersion);
        for (const v of adjacent) {
            queryClient.prefetchQuery({
                queryKey: ["artifact-content", projectId ?? null, sessionId ?? null, artifact.filename, v],
                queryFn: () => getArtifactContent({ filename: artifact.filename, sessionId, projectId, version: v }),
                staleTime: PREVIEW_CACHE_MS,
            });
        }
    }, [currentVersion, availableVersions, queryClient, artifact.filename, sessionId, projectId]);

    const fileContent: FileAttachment | null = useMemo(() => {
        if (!contentQuery.data || currentVersion === null) return null;
        const artifactUrl = getArtifactUrl({ filename: artifact.filename, sessionId, projectId, version: currentVersion });
        return {
            name: artifact.filename,
            mimeType: contentQuery.data.mimeType,
            content: contentQuery.data.content,
            lastModified: artifact.lastModified,
            url: artifactUrl,
        };
    }, [contentQuery.data, currentVersion, artifact.filename, artifact.lastModified, sessionId, projectId]);

    const fetchError = versionsQuery.error ?? contentQuery.error;
    const error = renderError ?? (fetchError instanceof Error ? fetchError.message : fetchError ? "Failed to load artifact" : null);
    const isLoading = canPreview && (versionsQuery.isLoading || (currentVersion !== null && contentQuery.isLoading));

    return {
        fileContent,
        isLoading,
        error,
        setError: setRenderError,
        canPreview,
        rendererType,
        availableVersions,
        currentVersion,
        setCurrentVersion,
    };
}
