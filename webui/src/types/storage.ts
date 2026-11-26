// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Part } from "./a2a";
import type { FileAttachment } from "./chat";

export interface MessageBubble {
    id: string;
    type: "user" | "agent" | "artifact_notification";
    text: string;
    parts?: Part[];
    files?: FileAttachment[];
    uploadedFiles?: Array<{ name: string; type: string }>;
    artifactNotification?: { name: string; version?: number };
    isError?: boolean;
    displayHtml?: string;
    contextQuote?: string;
    contextQuoteSourceId?: string;
    senderDisplayName?: string;
    senderEmail?: string;
}

export interface TaskMetadata {
    schemaVersion: number;
    status?: string;
    agentName?: string;
    feedback?: {
        type: "up" | "down";
        text?: string;
        submitted?: boolean;
    };
    [key: string]: unknown;
}

export interface StoredTaskData {
    taskId: string;
    messageBubbles: string;
    taskMetadata?: string | null;
    createdAt: number;
    userMessage?: string;
}

export interface ParsedTaskData extends Omit<StoredTaskData, "messageBubbles" | "taskMetadata"> {
    messageBubbles: MessageBubble[];
    taskMetadata: TaskMetadata | null;
}
