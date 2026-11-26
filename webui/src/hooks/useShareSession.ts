// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import type { Session } from "@/types";

export interface UseShareSessionReturn {
    isShareDialogOpen: boolean;
    sessionToShare: Session | null;
    setShareDialogOpen: (open: boolean) => void;
    closeShareDialog: () => void;
}

export function useShareSession(): UseShareSessionReturn {
    const [isShareDialogOpen, setIsShareDialogOpen] = useState(false);
    const [sessionToShare, setSessionToShare] = useState<Session | null>(null);

    const handleOpenShareDialog = useCallback((event: CustomEvent<{ session: Session }>) => {
        setSessionToShare(event.detail.session);
        setIsShareDialogOpen(true);
    }, []);

    useEffect(() => {
        globalThis.addEventListener("open-share-session-dialog", handleOpenShareDialog as EventListener);
        return () => {
            globalThis.removeEventListener("open-share-session-dialog", handleOpenShareDialog as EventListener);
        };
    }, [handleOpenShareDialog]);

    const setShareDialogOpen = useCallback((open: boolean) => {
        setIsShareDialogOpen(open);
        if (!open) {
            setSessionToShare(null);
        }
    }, []);

    const closeShareDialog = useCallback(() => {
        setIsShareDialogOpen(false);
        setSessionToShare(null);
    }, []);

    return {
        isShareDialogOpen,
        sessionToShare,
        setShareDialogOpen,
        closeShareDialog,
    };
}
