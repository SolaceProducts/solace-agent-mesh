// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { DataPart, Part, TextPart } from "@/types/a2a";
import type { MessageFE } from "@/types/chat";

const BUILDER_ACTIVATE_SIGNAL_TYPE = "request_build_activate";

export function filterRenderableDataParts(parts: Part[], hasDeepResearchProgress: boolean): Part[] {
    return parts.filter(p => {
        if (p.kind === "data") {
            const dataPart = p as DataPart;
            const dataType = dataPart.data && (dataPart.data as Record<string, unknown>).type;
            return dataType === "deep_research_progress" || dataType === "deep_research_plan" || dataType === "compaction_notification" || dataType === "redirect" || dataType === BUILDER_ACTIVATE_SIGNAL_TYPE;
        }
        if (p.kind === "text" && hasDeepResearchProgress) {
            return false;
        }
        return true;
    });
}

export function checkHasVisibleContent(parts: Part[]): boolean {
    return parts.some(
        p =>
            (p.kind === "text" && (p as TextPart).text.trim()) ||
            p.kind === "file" ||
            (p.kind === "data" &&
                (p as DataPart).data &&
                ((p as DataPart).data.type === "deep_research_progress" ||
                    (p as DataPart).data.type === "deep_research_plan" ||
                    (p as DataPart).data.type === "compaction_notification" ||
                    (p as DataPart).data.type === "redirect" ||
                    (p as DataPart).data.type === "request_build_activate"))
    );
}

export function isCompactionNotificationBubble(lastMessage: MessageFE | undefined, taskId: string, newContentParts: Part[]): boolean {
    return !!lastMessage && !lastMessage.isUser && lastMessage.taskId === taskId && newContentParts.length === 1 && newContentParts[0].kind === "data" && (newContentParts[0] as DataPart).data?.type === "compaction_notification";
}
