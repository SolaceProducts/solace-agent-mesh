// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";

import { downloadSharedArtifact, useForkSharedChat, useSharedSessionView } from "@/api/share";

import { downloadBlob } from "@/utils/download";
import { extractRagDataFromTasks } from "@/utils/taskUtils";

import type { SharedArtifact } from "@/types/share";
import type { MessageBubble } from "@/types/storage";

import type { ArtifactInfo, ArtifactPart, MessageFE, PartFE } from "@/types";

export { formatDateYMD } from "@/utils/format";

function convertToArtifactInfo(artifact: SharedArtifact): ArtifactInfo {
    return {
        filename: artifact.filename,
        mimeType: artifact.mimeType,
        size: artifact.size,
        lastModified: artifact.lastModified || new Date().toISOString(),
        version: artifact.version ?? undefined,
        versionCount: artifact.versionCount ?? undefined,
        description: artifact.description,
        source: artifact.source ?? undefined,
    };
}

export function useSharedSession() {
    const { shareId } = useParams<{ shareId: string }>();
    const navigate = useNavigate();

    const sessionQuery = useSharedSessionView(shareId || "");
    const forkMutation = useForkSharedChat();

    const session = sessionQuery.data ?? null;
    const loading = sessionQuery.isLoading;
    const error = sessionQuery.error ? (sessionQuery.error instanceof Error ? sessionQuery.error.message : "Failed to load shared session") : null;
    const isForking = forkMutation.isPending;

    const [isSidePanelCollapsed, setIsSidePanelCollapsed] = useState(true);
    const [activeSidePanelTab, setActiveSidePanelTab] = useState<"files" | "workflow" | "sources">("files");
    const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);

    const handleSharedArtifactDownload = useCallback(
        async (artifact: ArtifactInfo) => {
            if (!shareId) return;
            try {
                const blob = await downloadSharedArtifact(shareId, artifact.filename);
                downloadBlob(blob, artifact.filename);
            } catch (err) {
                console.error("Failed to download artifact:", err);
            }
        },
        [shareId]
    );

    const handleForkChat = useCallback(async () => {
        if (!shareId || isForking) return;

        try {
            const result = await forkMutation.mutateAsync(shareId);
            const newSessionId = result?.sessionId;
            navigate("/chat");
            setTimeout(() => {
                window.dispatchEvent(new CustomEvent("new-chat-session"));
                if (newSessionId) {
                    window.dispatchEvent(new CustomEvent("switch-to-session", { detail: { sessionId: newSessionId } }));
                }
            }, 200);
        } catch (err) {
            console.error("Failed to fork chat:", err);
        }
    }, [shareId, isForking, navigate, forkMutation]);

    const ragData = useMemo(() => {
        if (!session) return [];
        return extractRagDataFromTasks(session.tasks);
    }, [session]);

    const convertedArtifacts = useMemo(() => {
        if (!session?.artifacts) return [];
        return session.artifacts.map(convertToArtifactInfo);
    }, [session?.artifacts]);

    const messages: MessageFE[] = useMemo(() => {
        if (!session) return [];
        const result: MessageFE[] = [];

        for (const task of session.tasks) {
            try {
                const taskId = task.workflowTaskId || task.id;
                const bubbles = typeof task.messageBubbles === "string" ? JSON.parse(task.messageBubbles) : task.messageBubbles;

                if (!Array.isArray(bubbles)) continue;

                (bubbles as MessageBubble[]).forEach((bubble: MessageBubble) => {
                    const parts: PartFE[] = [];
                    const isUser = bubble.type === "user";

                    const uploadedFiles: File[] = [];
                    if (bubble.uploadedFiles && Array.isArray(bubble.uploadedFiles)) {
                        for (const f of bubble.uploadedFiles) {
                            const fileObj = f as { name?: string; filename?: string; mimeType?: string; type?: string };
                            const fileName = fileObj.name || fileObj.filename || "Attached file";
                            const fileType = fileObj.mimeType || fileObj.type || "";
                            uploadedFiles.push(new File([], fileName, { type: fileType }));
                        }
                    }

                    if (bubble.parts && Array.isArray(bubble.parts)) {
                        for (const part of bubble.parts) {
                            const partObj = part as {
                                kind?: string;
                                text?: string;
                                file?: { name?: string; filename?: string; mimeType?: string };
                                artifact?: { name?: string; filename?: string; status?: string };
                                metadata?: Record<string, unknown>;
                            };
                            if (partObj.kind === "text" && partObj.text) {
                                parts.push({ kind: "text", text: partObj.text });
                            } else if (partObj.kind === "file" && partObj.file) {
                                parts.push({
                                    kind: "file",
                                    file: {
                                        name: partObj.file.name || partObj.file.filename || "Attached file",
                                        mimeType: partObj.file.mimeType,
                                        uri: "",
                                    },
                                    metadata: partObj.metadata,
                                });
                            } else if (partObj.kind === "artifact") {
                                const artifactData = partObj.artifact || partObj;
                                const artifactName = (artifactData as { name?: string; filename?: string }).name || (artifactData as { name?: string; filename?: string }).filename || "Artifact";
                                const fullArtifact = convertedArtifacts.find(a => a.filename === artifactName);
                                parts.push({
                                    kind: "artifact",
                                    status: ((artifactData as { status?: string }).status as ArtifactPart["status"]) || "completed",
                                    name: artifactName,
                                    tags: (artifactData as { tags?: string[] }).tags,
                                    file: fullArtifact
                                        ? {
                                              name: fullArtifact.filename,
                                              mimeType: fullArtifact.mimeType,
                                          }
                                        : { name: artifactName },
                                } as ArtifactPart);
                            }
                        }
                    } else if (bubble.text) {
                        parts.push({ kind: "text", text: bubble.text });
                    }

                    result.push({
                        taskId,
                        createdAt: Date.parse(task.createdAt) || 0,
                        role: isUser ? "user" : "agent",
                        isUser,
                        isComplete: true,
                        isError: bubble.isError || false,
                        displayHtml: bubble.displayHtml,
                        contextQuote: bubble.contextQuote,
                        contextQuoteSourceId: bubble.contextQuoteSourceId,
                        senderDisplayName: bubble.senderDisplayName,
                        senderEmail: bubble.senderEmail,
                        uploadedFiles: uploadedFiles.length > 0 ? uploadedFiles : undefined,
                        parts,
                        metadata: {
                            messageId: bubble.id,
                            sessionId: task.sessionId,
                        },
                    });
                });
            } catch (e) {
                console.error("Failed to parse message bubbles:", e);
            }
        }
        return result;
    }, [session, convertedArtifacts]);

    const lastMessageIndexByTaskId = useMemo(() => {
        const map = new Map<string, number>();
        messages.forEach((message, index) => {
            if (message.taskId) {
                map.set(message.taskId, index);
            }
        });
        return map;
    }, [messages]);

    const hasRagSources = ragData.length > 0;

    const toggleSidePanel = useCallback(() => {
        setIsSidePanelCollapsed(prev => !prev);
    }, []);

    const openSidePanelTab = useCallback((tab: "files" | "workflow" | "sources") => {
        setIsSidePanelCollapsed(false);
        setActiveSidePanelTab(tab);
    }, []);

    const sessionIdForProvider = session?.tasks[0]?.sessionId || session?.id || "";

    const handleProviderTabOpen = useCallback(
        (tab: string) => {
            if (tab === "activity") {
                openSidePanelTab("workflow");
            } else if (tab === "rag") {
                openSidePanelTab("sources");
            } else if (tab === "files") {
                openSidePanelTab("files");
            }
        },
        [openSidePanelTab]
    );

    return {
        shareId,
        navigate,
        session,
        loading,
        error,
        isForking,

        isSidePanelCollapsed,
        activeSidePanelTab,
        setActiveSidePanelTab,
        selectedTaskId,
        setSelectedTaskId,
        toggleSidePanel,
        openSidePanelTab,

        convertedArtifacts,
        ragData,
        hasRagSources,
        messages,
        lastMessageIndexByTaskId,
        sessionIdForProvider,

        handleSharedArtifactDownload,
        handleForkChat,
        handleProviderTabOpen,
    };
}
