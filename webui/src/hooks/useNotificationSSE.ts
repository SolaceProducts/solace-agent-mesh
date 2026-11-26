// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { scheduledTaskKeys } from "@/api/scheduled-tasks/keys";
import { shareKeys } from "@/api/share/keys";

import { useChatContext } from "@/providers/contexts";
import { useSSESubscription } from "@/providers/SSEProvider";

export function useNotificationSSE(): void {
    const queryClient = useQueryClient();
    const { addNotification } = useChatContext();

    const onChatShared = useCallback(
        (event: MessageEvent) => {
            queryClient.invalidateQueries({ queryKey: shareKeys.sharedWithMeAll() });

            let payload: { title?: string; ownerEmail?: string; reason?: string };
            try {
                payload = JSON.parse(event.data);
            } catch {
                return;
            }
            if (payload?.reason !== "shared") return;
            addNotification(`${payload.ownerEmail ?? "Someone"} shared "${payload.title ?? "a chat"}" with you`, "info");
        },
        [queryClient, addNotification]
    );

    const invalidateScheduledTasks = useCallback(() => {
        queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.all });
    }, [queryClient]);

    const onSessionCreated = useCallback(() => {
        window.dispatchEvent(new CustomEvent("new-chat-session"));
        invalidateScheduledTasks();
    }, [invalidateScheduledTasks]);

    useSSESubscription({
        endpoint: "/api/v1/sse/notifications",
        eventType: "session_created",
        onMessage: onSessionCreated,
    });

    useSSESubscription({
        endpoint: "/api/v1/sse/notifications",
        eventType: "execution_queued",
        onMessage: invalidateScheduledTasks,
    });

    useSSESubscription({
        endpoint: "/api/v1/sse/notifications",
        eventType: "execution_started",
        onMessage: invalidateScheduledTasks,
    });

    useSSESubscription({
        endpoint: "/api/v1/sse/notifications",
        eventType: "task_auto_paused",
        onMessage: invalidateScheduledTasks,
    });

    useSSESubscription({
        endpoint: "/api/v1/sse/notifications",
        eventType: "chat_shared",
        onMessage: onChatShared,
    });
}
