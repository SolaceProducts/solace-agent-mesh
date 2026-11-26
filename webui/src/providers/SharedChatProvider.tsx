// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { artifactKeys } from "@/api/artifacts";
import { getSharedArtifactContent } from "@/api/share";

import { BuilderProvider } from "@/providers/BuilderProvider";
import { ChatMessageDataProvider } from "@/providers/ChatMessageDataProvider";
import { ChatContext, type ChatContextValue, defaultChatContextValue } from "@/providers/contexts/ChatContext";

import type { ArtifactInfo, FileAttachment, RAGSearchResult } from "@/types";

interface SharedChatProviderProps {
    children: ReactNode;
    artifacts: ArtifactInfo[];
    ragData?: RAGSearchResult[];
    sessionId?: string;
    shareId: string;
    onOpenSidePanelTab?: (tab: string) => void;
    onSetTaskIdInSidePanel?: (taskId: string | null) => void;
    onSwitchSession?: (sessionId: string) => void;
    onNewSession?: () => void;
}

export function SharedChatProvider({ children, artifacts: initialArtifacts, ragData = [], sessionId = "", shareId, onOpenSidePanelTab, onSetTaskIdInSidePanel, onSwitchSession, onNewSession }: SharedChatProviderProps) {
    const queryClient = useQueryClient();
    const downloadAttemptedRef = useRef<Set<string>>(new Set());
    const [previewArtifact, setPreviewArtifact] = useState<ArtifactInfo | null>(null);

    useEffect(() => {
        const seeded = initialArtifacts.map(a => ({ ...a, needsEmbedResolution: true }));
        queryClient.setQueryData(artifactKeys.session(sessionId), seeded);
    }, [initialArtifacts, queryClient, sessionId]);

    const getArtifacts = useCallback((): ArtifactInfo[] => queryClient.getQueryData<ArtifactInfo[]>(artifactKeys.session(sessionId)) ?? [], [queryClient, sessionId]);

    const updateArtifacts = useCallback(
        (mapper: (prev: ArtifactInfo[]) => ArtifactInfo[]) => {
            queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(sessionId), prev => mapper(prev ?? []));
        },
        [queryClient, sessionId]
    );
    const [previewFileContent, setPreviewFileContent] = useState<FileAttachment | null>(null);
    const [previewedArtifactAvailableVersions, setPreviewedArtifactAvailableVersions] = useState<number[] | null>(null);
    const [currentPreviewedVersionNumber, setCurrentPreviewedVersionNumber] = useState<number | null>(null);
    const [expandedDocumentFilename, setExpandedDocumentFilename] = useState<string | null>(null);

    const [taskIdInSidePanel, setTaskIdInSidePanelState] = useState<string | null>(null);

    const setTaskIdInSidePanel = useCallback(
        (value: React.SetStateAction<string | null>) => {
            setTaskIdInSidePanelState(prev => {
                const newValue = typeof value === "function" ? value(prev) : value;
                onSetTaskIdInSidePanel?.(newValue);
                return newValue;
            });
        },
        [onSetTaskIdInSidePanel]
    );

    const getShareArtifactUrl = useCallback(
        (filename: string): string => {
            const encodedFilename = encodeURIComponent(filename);
            return `/api/v1/share/${shareId}/artifacts/${encodedFilename}`;
        },
        [shareId]
    );

    const openArtifactForPreview = useCallback(
        async (artifactFilename: string): Promise<FileAttachment | null> => {
            if (!shareId) return null;

            const artifact = getArtifacts().find(a => a.filename === artifactFilename);
            if (!artifact) return null;

            try {
                const content = await getSharedArtifactContent(shareId, artifactFilename);
                const fileAttachment: FileAttachment = {
                    name: artifact.filename,
                    mimeType: artifact.mimeType,
                    content: content.content,
                    size: artifact.size,
                    url: getShareArtifactUrl(artifact.filename),
                };

                setPreviewFileContent(fileAttachment);
                setPreviewArtifact(artifact);

                if (artifact.versionCount && artifact.versionCount > 1) {
                    const versions = Array.from({ length: artifact.versionCount }, (_, i) => i + 1);
                    setPreviewedArtifactAvailableVersions(versions);
                    setCurrentPreviewedVersionNumber(artifact.version || artifact.versionCount);
                } else {
                    setPreviewedArtifactAvailableVersions(null);
                    setCurrentPreviewedVersionNumber(null);
                }

                return fileAttachment;
            } catch (error) {
                console.error("Failed to fetch artifact content:", error);
                return null;
            }
        },
        [shareId, getArtifacts, getShareArtifactUrl]
    );

    const navigateArtifactVersion = useCallback(async (_artifactFilename: string, _targetVersion: number): Promise<FileAttachment | null> => {
        console.warn("Version navigation is not supported in shared sessions");
        return null;
    }, []);

    const downloadAndResolveArtifact = useCallback(
        async (filename: string): Promise<FileAttachment | null> => {
            if (!shareId) return null;

            if (downloadAttemptedRef.current.has(filename)) {
                return null;
            }

            const artifact = getArtifacts().find(a => a.filename === filename);
            if (!artifact) return null;

            downloadAttemptedRef.current.add(filename);

            try {
                const content = await getSharedArtifactContent(shareId, filename);

                updateArtifacts(prev => prev.map(a => (a.filename === filename ? { ...a, needsEmbedResolution: false } : a)));

                return {
                    name: artifact.filename,
                    mimeType: artifact.mimeType,
                    content: content.content,
                    size: artifact.size,
                    url: getShareArtifactUrl(artifact.filename),
                };
            } catch (error) {
                console.error("Failed to download artifact:", error);

                updateArtifacts(prev => prev.map(a => (a.filename === filename ? { ...a, needsEmbedResolution: false } : a)));

                return null;
            }
        },
        [shareId, getArtifacts, getShareArtifactUrl, updateArtifacts]
    );

    const contextValue: ChatContextValue = useMemo(
        () => ({
            ...defaultChatContextValue,
            sessionId,
            isCollaborativeSession: true,
            isSharedSession: true,
            taskIdInSidePanel,
            ragData,
            ragEnabled: ragData.length > 0,
            expandedDocumentFilename,
            previewArtifact,
            previewedArtifactAvailableVersions,
            currentPreviewedVersionNumber,
            previewFileContent,
            setTaskIdInSidePanel,
            handleNewSession: async () => {
                onNewSession?.();
            },
            handleSwitchSession: async (newSessionId: string) => {
                onSwitchSession?.(newSessionId);
                return true;
            },
            openSidePanelTab: onOpenSidePanelTab || (() => {}),
            setPreviewArtifact,
            openArtifactForPreview,
            navigateArtifactVersion,
            downloadAndResolveArtifact,
            setExpandedDocumentFilename,
        }),
        [
            sessionId,
            ragData,
            taskIdInSidePanel,
            setTaskIdInSidePanel,
            expandedDocumentFilename,
            previewArtifact,
            previewedArtifactAvailableVersions,
            currentPreviewedVersionNumber,
            previewFileContent,
            openArtifactForPreview,
            navigateArtifactVersion,
            downloadAndResolveArtifact,
            onOpenSidePanelTab,
            onSwitchSession,
            onNewSession,
        ]
    );

    return (
        <ChatContext.Provider value={contextValue}>
            <BuilderProvider>
                <ChatMessageDataProvider>{children}</ChatMessageDataProvider>
            </BuilderProvider>
        </ChatContext.Provider>
    );
}
