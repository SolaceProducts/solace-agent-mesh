// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Part } from "./a2a";
import type { A2UISurface } from "./a2ui";
import type { BuilderComponentState } from "./builder";
import type { A2AEventSSEPayload } from "./sse";

export interface MessageChip {
    emoji?: string;
    label: string;
    kind?: string;
}

export interface TaskSubject {
    id: string;
    type: "agent" | "workflow";
    name: string;
}

export interface TaskTrigger {
    kind?: string;
    channel?: string;
    entrypointId?: string;
    entrypointName?: string;
    route?: string;
    event?: string;
    deliveryId?: string;
}

export interface TaskFE {
    taskId: string;
    subject?: TaskSubject;
    trigger?: TaskTrigger;
    userId?: string;
    initialRequestText: string;
    events: A2AEventSSEPayload[];
    eventCount?: number;
    inputTokens?: number;
    outputTokens?: number;
    firstSeen: Date;
    lastUpdated: Date;
    parentTaskId?: string | null;
    sessionId?: string;
    sessionName?: string;
    status?: string;
    endTime?: string;
    needsHistoricalHydration?: boolean;
}

export interface TaskStoreState {
    tasks: Record<string, TaskFE>;
    taskOrder: string[];
}

export interface ToolEvent {
    toolName: string;
    data: unknown;
}

export interface ArtifactInfo {
    filename: string;
    mimeType: string;
    size: number;
    lastModified: string;
    uri?: string;
    version?: number;
    versionCount?: number;
    description?: string | null;
    schema?: string | null | object;
    accumulatedContent?: string;
    isAccumulatedContentPlainText?: boolean;
    creationStatus?: "in-progress" | "completed" | "failed";
    isDisplayed?: boolean;
    needsEmbedResolution?: boolean;
    source?: string;
    tags?: string[];
    sourceProjectId?: string;
}

export interface FileAttachment {
    name: string;
    content?: string;
    mimeType?: string;
    lastModified?: string;
    size?: number;
    uri?: string;
    url?: string;
}

export interface AttachedArtifactRef {
    uri: string;
    filename: string;
    mimeType: string;
}

export interface ArtifactPart {
    kind: "artifact";
    status: "in-progress" | "completed" | "failed";
    name: string;
    description?: string;
    bytesTransferred?: number;
    file?: FileAttachment;
    error?: string;
    tags?: string[];
}

export type PartFE = Part | ArtifactPart;

export interface ProgressUpdate {
    type: "status" | "tool_call" | "tool_result" | "artifact" | "delegation" | "thinking" | "builder_component";
    text: string;
    timestamp: number;
    expandableContent?: string;
    isExpandableComplete?: boolean;
    builderComponentId?: string;
    builderComponentState?: BuilderComponentState;
}

export interface MessageFE {
    taskId?: string;
    createdAt?: number;
    role?: "user" | "agent";
    isStatusBubble?: boolean;
    progressUpdates?: ProgressUpdate[];
    thinkingContent?: string;
    isThinkingComplete?: boolean;
    isUser: boolean;
    isStatusMessage?: boolean;
    isThinkingMessage?: boolean;
    isComplete?: boolean;
    isError?: boolean;
    isWarning?: boolean;
    uploadedFiles?: File[];
    attachedArtifacts?: AttachedArtifactRef[];
    toolEvents?: ToolEvent[];
    displayHtml?: string;
    contextQuote?: string;
    contextQuoteSourceId?: string;
    authenticationLink?: {
        url: string;
        text: string;
        targetAgent?: string;
        serviceName?: string;
        gatewayTaskId?: string;
        credentialKey?: string;
        authenticationAttempted?: boolean;
        rejected?: boolean;
        outcome?: "authenticated" | "failed";
    };
    senderDisplayName?: string;
    senderEmail?: string;
    userInputRequest?: {
        requestId: string;
        expiresAt: string;
        source: "ask_user_question" | "tool_approval";
        surface: A2UISurface;
        taskId: string;
        agentName: string;
        responded?: boolean;
        timedOut?: boolean;
        cancelled?: boolean;
        responseText?: string;
    };
    metadata?: {
        messageId?: string;
        sessionId?: string;
        lastProcessedEventSequence?: number;
        hidden?: boolean;
        chip?: MessageChip;
        [key: string]: unknown;
    };
    parts: PartFE[];
}
