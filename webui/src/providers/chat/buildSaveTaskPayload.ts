// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MessageFE, RAGSearchResult, TextPart } from "@/types";
import { CURRENT_SCHEMA_VERSION } from "@/utils";

import { serializeChatMessage } from "./serializeChatMessage";

export interface SaveTaskPayload {
    taskId: string;
    userMessage: string;
    messageBubbles: ReturnType<typeof serializeChatMessage>[];
    taskMetadata: {
        schemaVersion: number;
        status: "pending" | "completed" | "error" | "completed_with_warnings" | "failed" | "cancelled";
        agentName?: string;
        ragData?: RAGSearchResult[];
    };
}

export function buildSaveTaskPayload({ taskId, taskMessages, agentName, ragData }: { taskId: string; taskMessages: MessageFE[]; agentName?: string; ragData?: RAGSearchResult[] }): SaveTaskPayload | null {
    if (taskMessages.length === 0) return null;

    const messageBubbles = taskMessages.map(serializeChatMessage);

    const userMessage = taskMessages.find(m => m.isUser);
    const userMessageText =
        userMessage?.parts
            ?.filter(p => p.kind === "text")
            .map(p => (p as TextPart).text)
            .join("") || "";

    const hasError = taskMessages.some(m => m.isError);
    const taskStatus = hasError ? "error" : "completed";

    return {
        taskId,
        userMessage: userMessageText,
        messageBubbles,
        taskMetadata: {
            schemaVersion: CURRENT_SCHEMA_VERSION,
            status: taskStatus,
            agentName,
            ragData: ragData && ragData.length > 0 ? ragData : undefined,
        },
    };
}
