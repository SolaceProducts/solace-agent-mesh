// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useRef, useState } from "react";
import type { NavigateFunction } from "react-router-dom";

import { type ArtifactWithSession, getArtifactApiUrl } from "@/api/artifacts";
import { api, getErrorFromResponse } from "@/api/client";

import { useChatRoute } from "@/hooks/useChatRoute";

import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

import { getErrorMessage } from "@/utils";

import { mergeAttachments } from "../file/attachmentUtils";
import { goToArtifactSession } from "../goToArtifactSession";
import type { PasteMetadata } from "../paste/PasteActionDialog";
import type { PastedTextItem } from "../paste/pasteUtils";

interface UseChatAttachmentsOptions {
    isResponding: boolean;
    addNotification: (message: string, type?: "success" | "info" | "warning") => void;
    displayError: (error: { title: string; error: string; errorSource?: ProductAnalyticsErrorSource }) => void;
    handleSwitchSession: (sessionId: string) => Promise<void | boolean> | void;
    navigate: NavigateFunction;
    agentSessionRoutes?: Record<string, string>;
}

export interface ChatAttachmentsCapture {
    files: File[];
    artifactRefs: ArtifactWithSession[];
    pastedTextItems: PastedTextItem[];
}

export interface UseChatAttachmentsResult {
    selectedFiles: File[];
    selectedArtifactRefs: ArtifactWithSession[];
    pendingPastedTextItems: PastedTextItem[];
    selectedPendingPasteId: string | null;
    previewingArtifact: ArtifactWithSession | null;
    previewingLocalFile: File | null;
    showAttachArtifactDialog: boolean;
    showArtifactForm: boolean;

    setPendingPastedTextItems: React.Dispatch<React.SetStateAction<PastedTextItem[]>>;
    setSelectedArtifactRefs: React.Dispatch<React.SetStateAction<ArtifactWithSession[]>>;
    setPreviewingArtifact: (a: ArtifactWithSession | null) => void;
    setPreviewingLocalFile: (f: File | null) => void;
    setShowAttachArtifactDialog: (b: boolean) => void;

    addFiles: (incoming: File[], reservedNames: Iterable<string>) => void;
    handleAttachArtifacts: (artifactsToAttach: ArtifactWithSession[]) => void;
    handleRemoveArtifactRef: (uri: string) => void;
    handleRemoveFile: (index: number) => void;
    handleRemovePendingPaste: (id: string) => void;
    handlePendingPasteClick: (id: string) => void;
    handleSaveMetadata: (metadata: PasteMetadata) => void;
    handleCancelArtifactForm: () => void;
    handlePreviewArtifactDownload: (artifact: ArtifactWithSession) => Promise<void>;
    handlePreviewGoToChat: (artifact: ArtifactWithSession) => Promise<void>;
    handlePreviewGoToProject: (artifact: ArtifactWithSession) => void;

    captureAndClearForSubmit: () => ChatAttachmentsCapture;
    restoreFromCapture: (capture: ChatAttachmentsCapture) => void;
}

export function useChatAttachments({ addNotification, displayError, handleSwitchSession, navigate, agentSessionRoutes }: UseChatAttachmentsOptions): UseChatAttachmentsResult {
    const chatRoute = useChatRoute();
    const [selectedFiles, setSelectedFiles] = useState<File[]>([]);
    const [selectedArtifactRefs, setSelectedArtifactRefs] = useState<ArtifactWithSession[]>([]);
    const [pendingPastedTextItems, setPendingPastedTextItems] = useState<PastedTextItem[]>([]);
    const [selectedPendingPasteId, setSelectedPendingPasteId] = useState<string | null>(null);
    const [previewingArtifact, setPreviewingArtifact] = useState<ArtifactWithSession | null>(null);
    const [previewingLocalFile, setPreviewingLocalFile] = useState<File | null>(null);
    const [showAttachArtifactDialog, setShowAttachArtifactDialog] = useState(false);
    const [showArtifactForm, setShowArtifactForm] = useState(false);

    const userClearedFiles = useRef(false);
    const userClearedArtifactRefs = useRef(false);
    const userClearedPastedText = useRef(false);

    const addFiles = useCallback((incoming: File[], reservedNames: Iterable<string>) => {
        setSelectedFiles(prev => mergeAttachments(prev, incoming, reservedNames));
    }, []);

    const handleAttachArtifacts = useCallback((artifactsToAttach: ArtifactWithSession[]) => {
        setSelectedArtifactRefs(prev => {
            const existingUris = new Set(prev.map(a => a.uri).filter(Boolean));
            const deduped = artifactsToAttach.filter(a => a.uri && !existingUris.has(a.uri));
            return deduped.length ? [...prev, ...deduped] : prev;
        });
    }, []);

    const handleRemoveArtifactRef = useCallback((uri: string) => {
        userClearedArtifactRefs.current = true;
        setSelectedArtifactRefs(prev => prev.filter(a => a.uri !== uri));
    }, []);

    const handleRemoveFile = useCallback((index: number) => {
        userClearedFiles.current = true;
        setSelectedFiles(prev => prev.filter((_, i) => i !== index));
    }, []);

    const handleRemovePendingPaste = useCallback((id: string) => {
        userClearedPastedText.current = true;
        setPendingPastedTextItems(prev => prev.filter(item => item.id !== id));
    }, []);

    const handlePendingPasteClick = useCallback((id: string) => {
        setSelectedPendingPasteId(id);
        setShowArtifactForm(true);
    }, []);

    const handleSaveMetadata = useCallback(
        (metadata: PasteMetadata) => {
            if (!selectedPendingPasteId) return;
            setPendingPastedTextItems(prev =>
                prev.map(item =>
                    item.id === selectedPendingPasteId
                        ? {
                              ...item,
                              content: metadata.content,
                              filename: metadata.filename,
                              mimeType: metadata.mimeType,
                              description: metadata.description,
                              isConfigured: true,
                          }
                        : item
                )
            );
            setSelectedPendingPasteId(null);
            setShowArtifactForm(false);
        },
        [selectedPendingPasteId]
    );

    const handleCancelArtifactForm = useCallback(() => {
        setSelectedPendingPasteId(null);
        setShowArtifactForm(false);
    }, []);

    const handlePreviewArtifactDownload = useCallback(
        async (artifact: ArtifactWithSession) => {
            try {
                const response = await api.webui.get(getArtifactApiUrl(artifact), { fullResponse: true });
                if (!response.ok) {
                    throw new Error(await getErrorFromResponse(response));
                }
                const blob = await response.blob();
                const downloadUrl = window.URL.createObjectURL(blob);
                const link = document.createElement("a");
                link.href = downloadUrl;
                link.download = artifact.filename;
                document.body.appendChild(link);
                link.click();
                document.body.removeChild(link);
                window.URL.revokeObjectURL(downloadUrl);
                addNotification(`Downloaded ${artifact.filename}`, "success");
            } catch (error) {
                displayError({ title: "Download Failed", error: getErrorMessage(error), errorSource: "api" });
            }
        },
        [addNotification, displayError]
    );

    const handlePreviewGoToChat = useCallback(
        async (artifact: ArtifactWithSession) => {
            setPreviewingArtifact(null);
            await goToArtifactSession(artifact, agentSessionRoutes, { switchSession: handleSwitchSession, navigate, chatRoute });
        },
        [handleSwitchSession, navigate, chatRoute, agentSessionRoutes]
    );

    const handlePreviewGoToProject = useCallback(
        (artifact: ArtifactWithSession) => {
            setPreviewingArtifact(null);
            navigate(`/projects/${artifact.projectId}`);
        },
        [navigate]
    );

    const captureAndClearForSubmit = useCallback((): ChatAttachmentsCapture => {
        userClearedFiles.current = false;
        userClearedArtifactRefs.current = false;
        userClearedPastedText.current = false;

        const capture: ChatAttachmentsCapture = {
            files: [...selectedFiles],
            artifactRefs: [...selectedArtifactRefs],
            pastedTextItems: [...pendingPastedTextItems],
        };

        setSelectedFiles([]);
        setSelectedArtifactRefs([]);
        setPendingPastedTextItems([]);

        return capture;
    }, [selectedFiles, selectedArtifactRefs, pendingPastedTextItems]);

    const restoreFromCapture = useCallback((capture: ChatAttachmentsCapture) => {
        if (!userClearedFiles.current) {
            setSelectedFiles(current => (current.length > 0 ? current : capture.files));
        }
        if (!userClearedArtifactRefs.current) {
            setSelectedArtifactRefs(current => (current.length > 0 ? current : capture.artifactRefs));
        }
        if (!userClearedPastedText.current) {
            setPendingPastedTextItems(current => (current.length > 0 ? current : capture.pastedTextItems));
        }
    }, []);

    return {
        selectedFiles,
        selectedArtifactRefs,
        pendingPastedTextItems,
        selectedPendingPasteId,
        previewingArtifact,
        previewingLocalFile,
        showAttachArtifactDialog,
        showArtifactForm,

        setPendingPastedTextItems,
        setSelectedArtifactRefs,
        setPreviewingArtifact,
        setPreviewingLocalFile,
        setShowAttachArtifactDialog,

        addFiles,
        handleAttachArtifacts,
        handleRemoveArtifactRef,
        handleRemoveFile,
        handleRemovePendingPaste,
        handlePendingPasteClick,
        handleSaveMetadata,
        handleCancelArtifactForm,
        handlePreviewArtifactDownload,
        handlePreviewGoToChat,
        handlePreviewGoToProject,

        captureAndClearForSubmit,
        restoreFromCapture,
    };
}
