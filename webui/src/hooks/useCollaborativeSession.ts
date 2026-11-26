// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { api } from "@/api/client";

import { useAuthContext } from "@/providers/contexts";

const CURRENT_USER_WITH_EMAIL_ENDPOINT = "/api/v1/auth/me";

interface Session {
    userId?: string;
    ownerDisplayName?: string | null;
    ownerEmail?: string | null;
}

interface CollaborativeSessionState {
    isCollaborativeSession: boolean;
    hasSharedEditors: boolean;
    currentUserEmail: string;
    sessionOwnerName: string | null;
    sessionOwnerEmail: string | null;
}

interface UseCollaborativeSessionReturn extends CollaborativeSessionState {
    detectCollaborativeSession: (session: Session | null, sessionId: string) => Promise<void>;
    resetCollaborativeState: () => void;
    getCurrentUserId: () => string | null;
}

export function useCollaborativeSession(sessionId: string): UseCollaborativeSessionReturn {
    const { userInfo } = useAuthContext();
    const [isCollaborativeSession, setIsCollaborativeSession] = useState(false);
    const [hasSharedEditors, setHasSharedEditors] = useState(false);
    const [currentUserEmail, setCurrentUserEmail] = useState("");
    const [sessionOwnerName, setSessionOwnerName] = useState<string | null>(null);
    const [sessionOwnerEmail, setSessionOwnerEmail] = useState<string | null>(null);
    const currentUserIdFromAuth = useRef("");

    useEffect(() => {
        api.webui
            .get(CURRENT_USER_WITH_EMAIL_ENDPOINT)
            .then((data: { id?: string; email?: string }) => {
                if (data?.email) setCurrentUserEmail(data.email);
                if (data?.id) currentUserIdFromAuth.current = data.id;
            })
            .catch(() => {});
    }, []);

    const getCurrentUserId = useCallback((): string | null => {
        return typeof userInfo?.username === "string" ? userInfo.username : currentUserIdFromAuth.current || null;
    }, [userInfo]);

    const resetCollaborativeState = useCallback(() => {
        setIsCollaborativeSession(false);
        setHasSharedEditors(false);
        setSessionOwnerName(null);
        setSessionOwnerEmail(null);
    }, []);

    const detectCollaborativeSession = useCallback(
        async (session: Session | null, newSessionId: string) => {
            setHasSharedEditors(false);

            const currentUserId = getCurrentUserId();
            const sessionOwnerId = session?.userId;

            if (currentUserId && sessionOwnerId && currentUserId !== sessionOwnerId) {
                setIsCollaborativeSession(true);
                setSessionOwnerName(session?.ownerDisplayName || sessionOwnerId);
                setSessionOwnerEmail(session?.ownerEmail || sessionOwnerId);
            } else {
                setIsCollaborativeSession(false);
                setSessionOwnerName(null);
                setSessionOwnerEmail(null);

                try {
                    const { getShareLinkForSession, getShareUsers } = await import("@/api/share");
                    const link = await getShareLinkForSession(newSessionId);
                    if (link) {
                        const usersResponse = await getShareUsers(link.id);
                        const hasEditors = (usersResponse.users || []).some(u => u.accessLevel === "resource_editor");
                        setHasSharedEditors(hasEditors);
                    }
                } catch {
                    setHasSharedEditors(false);
                }
            }
        },
        [getCurrentUserId]
    );

    useEffect(() => {
        const handleShareUpdated = async (event: Event) => {
            const detail = (event as CustomEvent).detail;
            if (detail?.sessionId === sessionId && !isCollaborativeSession) {
                try {
                    const { getShareLinkForSession, getShareUsers } = await import("@/api/share");
                    const link = await getShareLinkForSession(sessionId);
                    if (link) {
                        const usersResponse = await getShareUsers(link.id);
                        const hasEditors = (usersResponse.users || []).some(u => u.accessLevel === "resource_editor");
                        setHasSharedEditors(hasEditors);
                    } else {
                        setHasSharedEditors(false);
                    }
                } catch {}
            }
        };
        window.addEventListener("share-updated", handleShareUpdated);
        return () => window.removeEventListener("share-updated", handleShareUpdated);
    }, [sessionId, isCollaborativeSession]);

    return {
        isCollaborativeSession,
        hasSharedEditors,
        currentUserEmail,
        sessionOwnerName,
        sessionOwnerEmail,
        detectCollaborativeSession,
        resetCollaborativeState,
        getCurrentUserId,
    };
}
