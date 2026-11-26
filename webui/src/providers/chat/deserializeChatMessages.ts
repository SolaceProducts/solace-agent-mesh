// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BUILD_MANIFEST_ARTIFACT_FILENAME, BUILDER_PRIVATE_ARTIFACTS } from "@/features/builder/builderArtifacts";
import { partArtifactName, partTags } from "@/features/chat/artifactTags";

import type { AttachedArtifactRef, MessageFE, PartFE } from "@/types";

import { removeArtifactMarkers, stripVersionSuffix } from "./artifactMarkers";

interface StoredFileAttachment {
    name: string;
    type?: string;
    uri?: string;
    size?: number;
}

interface StoredUploadedFile {
    name: string;
    type: string;
}

interface StoredBubble {
    id?: string;
    type: "user" | "agent";
    text?: string;
    parts?: PartFE[];
    files?: StoredFileAttachment[];
    uploadedFiles?: StoredUploadedFile[];
    attachedArtifacts?: AttachedArtifactRef[];
    artifactNotification?: unknown;
    isError?: boolean;
    isWarning?: boolean;
    displayHtml?: string;
    contextQuote?: string;
    contextQuoteSourceId?: string;
    senderDisplayName?: string;
    senderEmail?: string;
    progressUpdates?: MessageFE["progressUpdates"];
    thinkingContent?: string;
    isThinkingComplete?: boolean;
    userInputRequest?: MessageFE["userInputRequest"];
    authenticationLink?: MessageFE["authenticationLink"];
    metadata?: MessageFE["metadata"];

    sender_display_name?: string;
    sender_email?: string;
}

interface StoredTask {
    taskId: string;
    messageBubbles: StoredBubble[];
    taskMetadata?: unknown;
    createdAt: number | string;
}

const normalizeCreatedAt = (v: number | string): number => (typeof v === "number" ? v : Date.parse(v) || 0);

const ARTIFACT_RETURN_REGEX = /«artifact_return:([^»]+)»/g;
const ARTIFACT_REGEX = /«artifact:([^»]+)»/g;

function isSuppressedArtifactPart(filename: string): boolean {
    const clean = stripVersionSuffix(filename);
    if (clean === BUILD_MANIFEST_ARTIFACT_FILENAME) return false;
    return BUILDER_PRIVATE_ARTIFACTS.has(clean);
}

function createArtifactPart(filename: string, sessionId: string, tags?: string[]) {
    const cleanFilename = stripVersionSuffix(filename);
    return {
        kind: "artifact" as const,
        status: "completed" as const,
        name: cleanFilename,
        file: {
            name: cleanFilename,
            uri: `artifact://${sessionId}/${cleanFilename}`,
        },
        ...(tags ? { tags } : {}),
    };
}

function structuralArtifactTags(parts: PartFE[] | undefined): Map<string, string[]> {
    const byFilename = new Map<string, string[]>();
    for (const part of parts ?? []) {
        const name = partArtifactName(part);
        const tags = partTags(part);
        if (name && tags?.length) {
            byFilename.set(stripVersionSuffix(name), tags);
        }
    }
    return byFilename;
}

function extractArtifactMarkers(text: string, sessionId: string, addedArtifacts: Set<string>, processedParts: PartFE[], tagsByFilename: Map<string, string[]>): void {
    ARTIFACT_RETURN_REGEX.lastIndex = 0;
    ARTIFACT_REGEX.lastIndex = 0;

    let match;
    while ((match = ARTIFACT_RETURN_REGEX.exec(text)) !== null) {
        const artifactFilename = match[1];
        const normalizedFilename = stripVersionSuffix(artifactFilename);

        if (!addedArtifacts.has(normalizedFilename)) {
            addedArtifacts.add(normalizedFilename);
            if (!isSuppressedArtifactPart(artifactFilename)) {
                processedParts.push(createArtifactPart(artifactFilename, sessionId, tagsByFilename.get(normalizedFilename)));
            }
        }
    }

    while ((match = ARTIFACT_REGEX.exec(text)) !== null) {
        const artifactFilename = match[1];
        if (!addedArtifacts.has(artifactFilename)) {
            addedArtifacts.add(artifactFilename);
            if (!isSuppressedArtifactPart(artifactFilename)) {
                processedParts.push(createArtifactPart(artifactFilename, sessionId, tagsByFilename.get(stripVersionSuffix(artifactFilename))));
            }
        }
    }
}

export function deserializeChatMessages(task: StoredTask, sessionId: string): MessageFE[] {
    return task.messageBubbles.map((bubble, bubbleIndex) => {
        const processedParts: PartFE[] = [];
        const originalParts = bubble.parts ?? [{ kind: "text" as const, text: bubble.text ?? "" }];
        const addedArtifacts = new Set<string>();
        const tagsByFilename = structuralArtifactTags(bubble.parts);

        if (bubble.text) {
            extractArtifactMarkers(bubble.text, sessionId, addedArtifacts, processedParts, tagsByFilename);
        }

        for (const part of originalParts) {
            if (part.kind === "text" && "text" in part && part.text) {
                let textContent = part.text as string;

                extractArtifactMarkers(textContent, sessionId, addedArtifacts, processedParts, tagsByFilename);

                textContent = removeArtifactMarkers(textContent);
                textContent = textContent.replaceAll(/«{1,3}\s*status_update:[^»]+»{1,3}\n?/g, "");

                if (textContent.trim()) {
                    processedParts.push({ kind: "text", text: textContent });
                }
            } else if (part.kind === "artifact" && "name" in part) {
                const artifactName = part.name as string;
                if (artifactName && !addedArtifacts.has(artifactName)) {
                    addedArtifacts.add(artifactName);
                    if (!isSuppressedArtifactPart(artifactName)) {
                        processedParts.push(part);
                    }
                }
            } else if (part.kind === "file") {
                const fileName = stripVersionSuffix(part.file?.name ?? "");
                if (fileName && (BUILDER_PRIVATE_ARTIFACTS.has(fileName) || addedArtifacts.has(fileName))) {
                    continue;
                }
                if (fileName) {
                    addedArtifacts.add(fileName);
                }
                processedParts.push(part);
            } else if (part.kind === "data" && (part as { data?: { type?: string } }).data?.type === "request_build_activate") {
                continue;
            } else {
                processedParts.push(part);
            }
        }

        return {
            taskId: task.taskId,
            createdAt: normalizeCreatedAt(task.createdAt),
            role: bubble.type === "user" ? "user" : "agent",
            parts: processedParts,
            isUser: bubble.type === "user",
            isComplete: true,
            files: bubble.files,
            uploadedFiles: bubble.uploadedFiles as MessageFE["uploadedFiles"],
            attachedArtifacts: bubble.attachedArtifacts,
            artifactNotification: bubble.artifactNotification,
            isError: bubble.isError,
            isWarning: bubble.isWarning,
            displayHtml: bubble.displayHtml,
            contextQuote: bubble.contextQuote,
            contextQuoteSourceId: bubble.contextQuoteSourceId,
            senderDisplayName: bubble.senderDisplayName ?? bubble.sender_display_name,
            senderEmail: bubble.senderEmail ?? bubble.sender_email,
            ...(bubble.progressUpdates && bubble.progressUpdates.length > 0 ? { progressUpdates: bubble.progressUpdates } : {}),
            ...((bubble.thinkingContent?.length ?? 0) > 0 ? { thinkingContent: bubble.thinkingContent, isThinkingComplete: bubble.isThinkingComplete ?? true } : {}),
            ...(bubble.userInputRequest ? { userInputRequest: { ...bubble.userInputRequest, responded: bubble.userInputRequest.responded || bubbleIndex < task.messageBubbles.length - 1 } } : {}),
            ...(bubble.authenticationLink ? { authenticationLink: bubble.authenticationLink } : {}),
            metadata: {
                ...bubble.metadata,
                messageId: bubble.id,
                sessionId,
                lastProcessedEventSequence: 0,
            },
        };
    });
}
