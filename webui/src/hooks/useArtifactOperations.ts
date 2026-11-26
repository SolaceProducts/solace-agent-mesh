// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { artifactKeys } from "@/api/artifacts";
import { api } from "@/api/client";

import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

import type { ArtifactInfo, FileAttachment } from "@/types";
import { blobToBase64, getErrorMessage } from "@/utils";

interface UseArtifactOperationsOptions {
    sessionId: string;
    addNotification: (message: string, type?: "success" | "info" | "warning") => void;
    setError: (error: { title: string; error: string; errorSource?: ProductAnalyticsErrorSource }) => void;
    previewArtifact: { filename: string } | null;
    closePreview: () => void;
}

interface UseArtifactOperationsReturn {
    uploadArtifactFile: (file: File, overrideSessionId?: string, description?: string, silent?: boolean) => Promise<{ uri: string; sessionId: string } | { error: string } | null>;

    isDeleteModalOpen: boolean;
    artifactToDelete: ArtifactInfo | null;
    openDeleteModal: (artifact: ArtifactInfo) => void;
    closeDeleteModal: () => void;
    confirmDelete: () => Promise<void>;

    isArtifactEditMode: boolean;
    setIsArtifactEditMode: React.Dispatch<React.SetStateAction<boolean>>;
    selectedArtifactFilenames: Set<string>;
    setSelectedArtifactFilenames: React.Dispatch<React.SetStateAction<Set<string>>>;
    isBatchDeleteModalOpen: boolean;
    setIsBatchDeleteModalOpen: React.Dispatch<React.SetStateAction<boolean>>;
    handleDeleteSelectedArtifacts: () => void;
    confirmBatchDeleteArtifacts: () => Promise<void>;

    downloadAndResolveArtifact: (filename: string) => Promise<FileAttachment | null>;
}

const getFileAttachment = (artifactInfos: ArtifactInfo[], filename: string, mimeType: string, content: string): FileAttachment => {
    const artifactInfo = artifactInfos.find(a => a.filename === filename);
    return {
        name: filename,
        mimeType: mimeType,
        content: content,
        lastModified: artifactInfo?.lastModified || new Date().toISOString(),
    };
};

export const useArtifactOperations = ({ sessionId, addNotification, setError, previewArtifact, closePreview }: UseArtifactOperationsOptions): UseArtifactOperationsReturn => {
    const queryClient = useQueryClient();

    const [isDeleteModalOpen, setIsDeleteModalOpen] = useState(false);
    const [artifactToDelete, setArtifactToDelete] = useState<ArtifactInfo | null>(null);
    const [isBatchDeleteModalOpen, setIsBatchDeleteModalOpen] = useState(false);

    const [isArtifactEditMode, setIsArtifactEditMode] = useState(false);
    const [selectedArtifactFilenames, setSelectedArtifactFilenames] = useState<Set<string>>(new Set());

    const artifactDownloadInProgressRef = useRef<Map<string, Promise<FileAttachment | null>>>(new Map());

    const sessionIdRef = useRef(sessionId);
    useEffect(() => {
        sessionIdRef.current = sessionId;
    }, [sessionId]);

    useEffect(() => {
        artifactDownloadInProgressRef.current.clear();
    }, [sessionId]);

    const getArtifacts = useCallback((): ArtifactInfo[] => {
        return queryClient.getQueryData<ArtifactInfo[]>(artifactKeys.session(sessionId)) ?? [];
    }, [queryClient, sessionId]);

    const artifactsRefetch = useCallback(async (): Promise<void> => {
        await queryClient.invalidateQueries({ queryKey: artifactKeys.session(sessionId) });
    }, [queryClient, sessionId]);

    const uploadArtifactFile = useCallback(
        async (file: File, overrideSessionId?: string, description?: string, silent: boolean = false): Promise<{ uri: string; sessionId: string } | { error: string } | null> => {
            const effectiveSessionId = overrideSessionId || sessionId;
            const formData = new FormData();
            formData.append("uploadFile", file);
            formData.append("filename", file.name);
            formData.append("sessionId", effectiveSessionId || "");

            if (description) {
                const metadata = { description };
                formData.append("metadataJson", JSON.stringify(metadata));
            }

            try {
                const response = await api.webui.post("/api/v1/artifacts/upload", formData, { fullResponse: true });

                if (response.status === 413) {
                    const errorData = await response.json().catch(() => ({ message: `Failed to upload ${file.name}.` }));
                    const errorMessage = errorData.message || `File "${file.name}" exceeds the maximum allowed size.`;
                    setError({ title: "File Upload Failed", error: errorMessage, errorSource: "api" });
                    return { error: errorMessage };
                }

                if (!response.ok) {
                    throw new Error(
                        await response
                            .json()
                            .then((d: { message?: string }) => d.message)
                            .catch(() => `Failed to upload ${file.name}.`)
                    );
                }

                const result = await response.json();
                if (!silent) {
                    addNotification(`File "${file.name}" uploaded.`, "success");
                }
                await artifactsRefetch();
                return result.uri && result.sessionId ? { uri: result.uri, sessionId: result.sessionId } : null;
            } catch (error) {
                const errorMessage = getErrorMessage(error, `Failed to upload "${file.name}".`);
                setError({ title: "File Upload Failed", error: errorMessage, errorSource: "api" });
                return { error: errorMessage };
            }
        },
        [sessionId, addNotification, artifactsRefetch, setError]
    );

    const deleteArtifactInternal = useCallback(
        async (filename: string) => {
            try {
                await api.webui.delete(`/api/v1/artifacts/${sessionId}/${encodeURIComponent(filename)}`);
                addNotification(`File "${filename}" deleted.`, "success");
                artifactsRefetch();
            } catch (error) {
                setError({ title: "File Deletion Failed", error: getErrorMessage(error, `Failed to delete ${filename}.`), errorSource: "api" });
            }
        },
        [sessionId, addNotification, artifactsRefetch, setError]
    );

    const openDeleteModal = useCallback((artifact: ArtifactInfo) => {
        setArtifactToDelete(artifact);
        setIsDeleteModalOpen(true);
    }, []);

    const closeDeleteModal = useCallback(() => {
        setArtifactToDelete(null);
        setIsDeleteModalOpen(false);
    }, []);

    const confirmDelete = useCallback(async () => {
        if (artifactToDelete) {
            const isCurrentlyPreviewed = previewArtifact?.filename === artifactToDelete.filename;

            await deleteArtifactInternal(artifactToDelete.filename);

            if (isCurrentlyPreviewed) {
                closePreview();
            }
        }
        closeDeleteModal();
    }, [artifactToDelete, deleteArtifactInternal, closeDeleteModal, previewArtifact, closePreview]);

    const handleDeleteSelectedArtifacts = useCallback(() => {
        if (selectedArtifactFilenames.size === 0) {
            return;
        }
        setIsBatchDeleteModalOpen(true);
    }, [selectedArtifactFilenames]);

    const confirmBatchDeleteArtifacts = useCallback(async () => {
        setIsBatchDeleteModalOpen(false);
        const filenamesToDelete = Array.from(selectedArtifactFilenames);
        let successCount = 0;
        let errorCount = 0;

        for (const filename of filenamesToDelete) {
            try {
                await api.webui.delete(`/api/v1/artifacts/${sessionId}/${encodeURIComponent(filename)}`);
                successCount++;
            } catch (error: unknown) {
                console.error(error);
                errorCount++;
            }
        }

        if (successCount > 0) addNotification(`${successCount} files(s) deleted.`, "success");
        if (errorCount > 0) {
            setError({ title: "File Deletion Failed", error: `${errorCount} file(s) failed to delete.`, errorSource: "api" });
        }

        artifactsRefetch();
        setSelectedArtifactFilenames(new Set());
        setIsArtifactEditMode(false);
    }, [selectedArtifactFilenames, addNotification, artifactsRefetch, sessionId, setError]);

    const downloadAndResolveArtifact = useCallback(
        async (filename: string): Promise<FileAttachment | null> => {
            const existing = artifactDownloadInProgressRef.current.get(filename);
            if (existing) {
                return existing;
            }

            const downloadSessionId = sessionId;
            const downloadPromise = (async () => {
                try {
                    const cachedArtifacts = getArtifacts();
                    const artifact = cachedArtifacts.find(art => art.filename === filename);
                    if (!artifact) {
                        console.debug(`Artifact ${filename} not in state yet; returning null`);
                        return null;
                    }

                    const { data: availableVersions = [] } = await api.webui.get<{ data: number[] }>(`/api/v1/artifacts/${sessionId}/${encodeURIComponent(filename)}/versions`);
                    if (availableVersions.length === 0) {
                        return null;
                    }

                    const latestVersion = Math.max(...availableVersions);
                    const contentResponse = await api.webui.get(`/api/v1/artifacts/${sessionId}/${encodeURIComponent(filename)}/versions/${latestVersion}`, { fullResponse: true });
                    if (!contentResponse.ok) {
                        throw new Error(`Failed to fetch artifact content: ${contentResponse.statusText}`);
                    }

                    const blob = await contentResponse.blob();
                    const base64Content = await blobToBase64(blob);
                    const fileData = getFileAttachment(cachedArtifacts, filename, artifact.mimeType || "application/octet-stream", base64Content);

                    if (downloadSessionId === sessionIdRef.current) {
                        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(downloadSessionId), prev =>
                            (prev ?? []).map(art =>
                                art.filename === filename
                                    ? {
                                          ...art,
                                          accumulatedContent: undefined,
                                          needsEmbedResolution: false,
                                      }
                                    : art
                            )
                        );
                    }

                    return fileData;
                } catch (error) {
                    setError({ title: "File Download Failed", error: getErrorMessage(error, `Failed to download ${filename}.`), errorSource: "api" });
                    return null;
                } finally {
                    artifactDownloadInProgressRef.current.delete(filename);
                }
            })();

            artifactDownloadInProgressRef.current.set(filename, downloadPromise);
            return downloadPromise;
        },
        [sessionId, getArtifacts, queryClient, setError]
    );

    return {
        uploadArtifactFile,
        isDeleteModalOpen,
        artifactToDelete,
        openDeleteModal,
        closeDeleteModal,
        confirmDelete,
        isArtifactEditMode,
        setIsArtifactEditMode,
        selectedArtifactFilenames,
        setSelectedArtifactFilenames,
        isBatchDeleteModalOpen,
        setIsBatchDeleteModalOpen,
        handleDeleteSelectedArtifacts,
        confirmBatchDeleteArtifacts,
        downloadAndResolveArtifact,
    };
};
