// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import type { Session } from "@/types";

export interface UseMoveSessionReturn {
    isMoveDialogOpen: boolean;
    sessionToMove: Session | null;
    handleMoveConfirm: (targetProjectId: string | null) => Promise<void>;
    closeMoveDialog: () => void;
}

export function useMoveSession(): UseMoveSessionReturn {
    const { addNotification } = useChatContext();
    const [isMoveDialogOpen, setIsMoveDialogOpen] = useState(false);
    const [sessionToMove, setSessionToMove] = useState<Session | null>(null);

    const handleOpenMoveDialog = useCallback((event: CustomEvent<{ session: Session }>) => {
        setSessionToMove(event.detail.session);
        setIsMoveDialogOpen(true);
    }, []);

    useEffect(() => {
        window.addEventListener("open-move-session-dialog", handleOpenMoveDialog as EventListener);
        return () => {
            window.removeEventListener("open-move-session-dialog", handleOpenMoveDialog as EventListener);
        };
    }, [handleOpenMoveDialog]);

    const handleMoveConfirm = useCallback(
        async (targetProjectId: string | null) => {
            if (!sessionToMove) return;

            try {
                await api.webui.patch(`/api/v1/sessions/${sessionToMove.id}/project`, { projectId: targetProjectId });

                window.dispatchEvent(
                    new CustomEvent("session-updated", {
                        detail: {
                            sessionId: sessionToMove.id,
                            projectId: targetProjectId,
                        },
                    })
                );

                addNotification?.("Session moved successfully", "success");
            } catch (error) {
                console.error("Failed to move session:", error);
                reportAndNotifyError(addNotification, "Failed to move session", "api");
            }
        },
        [sessionToMove, addNotification]
    );

    const closeMoveDialog = useCallback(() => {
        setIsMoveDialogOpen(false);
        setSessionToMove(null);
    }, []);

    return {
        isMoveDialogOpen,
        sessionToMove,
        handleMoveConfirm,
        closeMoveDialog,
    };
}
