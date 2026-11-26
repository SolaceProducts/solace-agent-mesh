// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { notificationKeys } from "@/api/notifications";

import { useSSESubscription } from "@/providers/SSEProvider";

export const INBOX_REFETCH_DEBOUNCE_MS = 300;

export function useLiveNotificationInbox(): void {
    const queryClient = useQueryClient();
    const pendingRefetch = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
    const refetchInbox = useCallback(() => {
        clearTimeout(pendingRefetch.current);
        pendingRefetch.current = setTimeout(() => {
            queryClient.invalidateQueries({ queryKey: notificationKeys.inboxes() });
        }, INBOX_REFETCH_DEBOUNCE_MS);
    }, [queryClient]);
    useEffect(() => () => clearTimeout(pendingRefetch.current), []);

    const { connectionState } = useSSESubscription({
        endpoint: "/api/v1/sse/notifications",
        eventType: "notification_created",
        onMessage: refetchInbox,
    });

    const missedEvents = useRef(false);
    useEffect(() => {
        if (connectionState === "connected") {
            if (missedEvents.current) {
                refetchInbox();
            }
            missedEvents.current = false;
        } else if (connectionState === "reconnecting" || connectionState === "error") {
            missedEvents.current = true;
        }
    }, [connectionState, refetchInbox]);
}
