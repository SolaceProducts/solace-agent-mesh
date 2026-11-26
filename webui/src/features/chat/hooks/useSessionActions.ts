// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";

import { useRenameSessionWithAI } from "@/api/sessions";

import { useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import { getErrorMessage } from "@/utils/api";

import { useTitleGeneration } from "@/hooks";
import type { Session } from "@/types";

import { collectMessageTexts } from "../recentChats/recentChatsPageUtils";

export interface UseSessionActionsReturn {
    editingSessionId: string | null;
    editingSessionName: string;
    setEditingSessionName: (name: string) => void;
    inputRef: React.RefObject<HTMLInputElement | null>;
    beginRename: (session: Session) => void;
    commitRename: () => Promise<void>;
    cancelRename: () => void;
    renameWithAI: (session: Session) => void;
    regeneratingTitleForSession: string | null;
    move: (session: Session) => void;
    remove: (session: Session) => void;
    goToProject: (session: Session) => void;
    share: (session: Session) => void;
}

export function useSessionActions(): UseSessionActionsReturn {
    const navigate = useNavigate();
    const { updateSessionName, openSessionDeleteModal, addNotification } = useChatContext();
    const { generateTitle } = useTitleGeneration();
    const renameWithAIMutation = useRenameSessionWithAI();

    const [editingSessionId, setEditingSessionId] = useState<string | null>(null);
    const [editingSessionName, setEditingSessionName] = useState<string>("");
    const [regeneratingTitleForSession, setRegeneratingTitleForSession] = useState<string | null>(null);
    const inputRef = useRef<HTMLInputElement | null>(null);
    const editingIdRef = useRef<string | null>(null);
    const originalNameRef = useRef<string>("");

    useEffect(() => {
        if (editingSessionId && inputRef.current) {
            inputRef.current.focus();
            inputRef.current.select();
        }
    }, [editingSessionId]);

    const beginRename = useCallback((session: Session) => {
        editingIdRef.current = session.id;
        originalNameRef.current = session.name ?? "";
        setEditingSessionId(session.id);
        setEditingSessionName(session.name ?? "");
    }, []);

    const cancelRename = useCallback(() => {
        editingIdRef.current = null;
        setEditingSessionId(null);
    }, []);

    const commitRename = useCallback(async () => {
        const sessionIdToUpdate = editingIdRef.current;
        if (!sessionIdToUpdate) return;
        editingIdRef.current = null;
        setEditingSessionId(null);
        const newName = editingSessionName.trim();
        if (!newName || newName === originalNameRef.current.trim()) return;
        await updateSessionName(sessionIdToUpdate, newName);
    }, [editingSessionName, updateSessionName]);

    const renameWithAI = useCallback(
        (session: Session) => {
            if (renameWithAIMutation.isPending) {
                addNotification?.("AI rename already in progress", "info");
                return;
            }

            setRegeneratingTitleForSession(session.id);

            renameWithAIMutation.mutate(session.id, {
                onSuccess: async data => {
                    const tasks = data.data || [];

                    if (tasks.length === 0) {
                        addNotification?.("No messages found in this session", "warning");
                        setRegeneratingTitleForSession(null);
                        return;
                    }

                    const allMessages = collectMessageTexts(tasks);

                    if (allMessages.length === 0) {
                        addNotification?.("No text content found in session", "warning");
                        setRegeneratingTitleForSession(null);
                        return;
                    }

                    const userMessages = allMessages.filter((_, idx) => idx % 2 === 0);
                    const agentMessages = allMessages.filter((_, idx) => idx % 2 === 1);
                    const userSummary = userMessages.slice(-3).join(" | ");
                    const agentSummary = agentMessages.slice(-3).join(" | ");

                    await generateTitle(session.id, userSummary, agentSummary, session.name || "New Chat", true);
                    setRegeneratingTitleForSession(null);
                },
                onError: error => {
                    console.error("Error regenerating title:", error);
                    reportAndNotifyError(addNotification, `Failed to regenerate title: ${getErrorMessage(error, "Unknown error")}`, "api");
                    setRegeneratingTitleForSession(null);
                },
            });
        },
        [renameWithAIMutation, generateTitle, addNotification]
    );

    const move = useCallback((session: Session) => {
        globalThis.dispatchEvent(new CustomEvent("open-move-session-dialog", { detail: { session } }));
    }, []);

    const remove = useCallback(
        (session: Session) => {
            openSessionDeleteModal(session);
        },
        [openSessionDeleteModal]
    );

    const goToProject = useCallback(
        (session: Session) => {
            if (!session.project?.id) return;
            navigate(`/projects/${session.project.id}`);
        },
        [navigate]
    );

    const share = useCallback((session: Session) => {
        globalThis.dispatchEvent(new CustomEvent("open-share-session-dialog", { detail: { session } }));
    }, []);

    return {
        editingSessionId,
        editingSessionName,
        setEditingSessionName,
        inputRef,
        beginRename,
        commitRename,
        cancelRename,
        renameWithAI,
        regeneratingTitleForSession,
        move,
        remove,
        goToProject,
        share,
    };
}
