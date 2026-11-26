// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef } from "react";

import type { ApiError } from "@/api/client";
import { type NotificationItem, useRespondToPrompt } from "@/api/notifications";

import { useChatContext } from "@/providers/contexts";

import { A2UISurfaceRenderer } from "@/features/chat/a2ui/A2UIRenderer";
import { buildAnsweredResponseText } from "@/features/chat/a2ui/summary";
import type { SurfaceAction } from "@/features/chat/a2ui/types";
import { AnsweredBanner } from "@/features/chat/a2ui/UserInputMessage";

import type { ChatRequests, InputRequestSource } from "./usePendingChatRequests";

export function PendingChatRequests({ waiting, answered, markAnswered }: Readonly<ChatRequests>) {
    if (waiting.length === 0 && answered.length === 0) {
        return null;
    }
    return (
        <div className="flex flex-col gap-4" data-testid="pending-chat-requests">
            {answered.map(a => (
                <AnsweredBanner key={a.id} source={a.source} responseText={a.responseText} />
            ))}
            {waiting.map(n => (
                <PendingChatRequest key={n.id} item={n} markAnswered={markAnswered} />
            ))}
        </div>
    );
}

function sourceOf(context: Record<string, unknown>): InputRequestSource {
    return typeof context.approved === "boolean" ? "tool_approval" : "ask_user_question";
}

function PendingChatRequest({ item, markAnswered }: Readonly<{ item: NotificationItem; markAnswered: ChatRequests["markAnswered"] }>) {
    const respond = useRespondToPrompt();
    const { displayError } = useChatContext();

    const ctaRef = useRef<HTMLDivElement>(null);
    useEffect(() => {
        ctaRef.current?.scrollIntoView({ block: "end" });
    }, []);

    const handleAction = useCallback(
        (action: SurfaceAction) => {
            const cancelled = action.eventName === "cancel";
            const source = sourceOf(action.context);
            respond.mutate(
                { id: item.id, status: cancelled ? "cancelled" : "answered", data: cancelled ? {} : action.context },
                {
                    onSuccess: () => {
                        const summary = cancelled || !item.surface ? "" : buildAnsweredResponseText(item.surface, source, action.context);
                        markAnswered({ id: item.id, source, responseText: summary || action.completionText });
                    },
                    onError: err => {
                        const { status, message } = err as ApiError;
                        displayError({ title: "Failed to submit response", error: status ? message : "Please try again.", errorSource: "api" });
                    },
                }
            );
        },
        [item.id, item.surface, respond, displayError, markAnswered]
    );

    if (!item.surface) {
        return null;
    }
    return (
        <div ref={ctaRef}>
            <A2UISurfaceRenderer surface={item.surface} onAction={handleAction} disabled={respond.isPending} />
        </div>
    );
}
