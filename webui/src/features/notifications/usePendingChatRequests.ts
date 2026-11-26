// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useState } from "react";

import { type NotificationItem, useNotificationInbox } from "@/api/notifications";

import { useIsNotificationsEnabled } from "@/hooks/useIsNotificationsEnabled";

import type { MessageFE } from "@/types";

import { isHilPending } from "./hilState";

const HIL_CATEGORY = "hil";
const PENDING_PAGE_SIZE = 50;

export type InputRequestSource = NonNullable<MessageFE["userInputRequest"]>["source"];

export interface AnsweredChatRequest {
    id: string;
    sessionId: string;
    source: InputRequestSource;
    responseText: string;
}

export interface ChatRequests {
    waiting: NotificationItem[];
    answered: AnsweredChatRequest[];
    markAnswered: (answer: Omit<AnsweredChatRequest, "sessionId">) => void;
}

export function pendingChatRequests(items: readonly NotificationItem[], sessionId: string | undefined, liveRequestIds: ReadonlySet<string>): NotificationItem[] {
    if (!sessionId) {
        return [];
    }
    return items.filter(n => isHilPending(n) && n.action?.sessionId === sessionId && !liveRequestIds.has(n.requestId ?? ""));
}

export function usePendingChatRequests(sessionId: string | undefined, liveRequestIds: ReadonlySet<string>): ChatRequests {
    const enabled = useIsNotificationsEnabled();
    const inbox = useNotificationInbox(1, PENDING_PAGE_SIZE, false, false, HIL_CATEGORY, enabled && Boolean(sessionId));
    const items = inbox.data?.data;
    const [answeredHere, setAnsweredHere] = useState<readonly AnsweredChatRequest[]>([]);

    const markAnswered = useCallback(
        (answer: Omit<AnsweredChatRequest, "sessionId">) => {
            if (sessionId) setAnsweredHere(prev => [...prev, { ...answer, sessionId }]);
        },
        [sessionId]
    );

    return useMemo(() => {
        const answered = answeredHere.filter(a => a.sessionId === sessionId);
        const answeredIds = new Set(answered.map(a => a.id));
        const waiting = pendingChatRequests(items ?? [], sessionId, liveRequestIds).filter(n => !answeredIds.has(n.id));
        return { waiting, answered, markAnswered };
    }, [items, sessionId, liveRequestIds, answeredHere, markAnswered]);
}
