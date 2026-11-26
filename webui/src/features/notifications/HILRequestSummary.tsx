// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import type { NotificationItem } from "@/api/notifications/types";
import { useSessionName } from "@/api/sessions/useSessionName";

import { A2UISurfaceRenderer } from "@/features/chat/a2ui/A2UIRenderer";

import { summarizeHilSurface } from "./hilSummary";

const ignoreAction = () => {};

export function HILRequestSummary({ item, sessionId }: Readonly<{ item: NotificationItem; sessionId: string }>) {
    const chatName = useSessionName(sessionId);
    const summary = useMemo(() => (item.surface ? summarizeHilSurface(item.surface) : null), [item.surface]);
    if (!summary) {
        return null;
    }
    return (
        <div className="flex flex-col gap-3">
            <A2UISurfaceRenderer surface={summary.surface} onAction={ignoreAction} disabled flush />
            <div className="flex flex-col gap-1 text-xs">
                <span>
                    Respond to the request in <span className="font-semibold text-(--primary-text-wMain)">{chatName ?? "its chat"}</span>
                </span>
                {summary.captions.map(caption => (
                    <span key={caption}>{caption}</span>
                ))}
            </div>
        </div>
    );
}
