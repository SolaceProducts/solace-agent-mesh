// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { useNavigate } from "react-router-dom";

import { type NotificationItem, useMarkNotificationRead } from "@/api/notifications";
import { useMarkShareViewed } from "@/api/share";

import { useChatContext } from "@/providers/contexts";

import { useChatRoute } from "@/hooks/useChatRoute";

import { goToArtifactSession } from "@/features/chat/goToArtifactSession";

import { isHilPending } from "./hilState";
import { RESPOND_IN_CHAT_LABEL, VIEW_CHAT_LABEL } from "./notificationRow";

export type NotificationAction = NonNullable<NotificationItem["action"]>;

const DEFAULT_ACTION_LABEL = "Open";

export function actionLabel(action: NotificationAction, n?: NotificationItem): string {
    if (action.sessionId && n && isHilPending(n)) {
        return RESPOND_IN_CHAT_LABEL;
    }
    return action.sessionId || action.shareId ? VIEW_CHAT_LABEL : action.label.trim() || DEFAULT_ACTION_LABEL;
}

export function navigableAction(n: NotificationItem): NotificationAction | undefined {
    return n.action?.sessionId || n.action?.shareId || n.action?.path ? n.action : undefined;
}

export function useOpenNotificationAction(): (n: NotificationItem, action: NotificationAction) => void {
    const navigate = useNavigate();
    const chatRoute = useChatRoute();
    const { agentSessionRoutes, handleSwitchSession } = useChatContext();
    const { mutate: markShareViewed } = useMarkShareViewed();
    const { mutate: markRead } = useMarkNotificationRead();

    return useCallback(
        (n: NotificationItem, action: NotificationAction) => {
            if (n.status === "unread") {
                markRead(n.id);
            }
            if (action.shareId) {
                markShareViewed(action.shareId);
            }
            if (action.sessionId) {
                void goToArtifactSession({ sessionId: action.sessionId, agentId: action.agentId }, agentSessionRoutes, {
                    switchSession: handleSwitchSession,
                    navigate,
                    chatRoute,
                });
                return;
            }
            if (action.shareId) {
                navigate(`/shared-chat/${action.shareId}`);
                return;
            }
            if (action.path) {
                navigate(action.path);
            }
        },
        [agentSessionRoutes, handleSwitchSession, navigate, chatRoute, markShareViewed, markRead]
    );
}
