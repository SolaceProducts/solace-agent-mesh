// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { uuid } from "@/utils/uuid";

import type { ArtifactPart, MessageFE, TextPart } from "@/types";

export function serializeChatMessage(message: MessageFE) {
    let combinedText = "";
    const parts = message.parts || [];

    for (const part of parts) {
        if (part.kind === "text") {
            combinedText += (part as TextPart).text;
        } else if (part.kind === "artifact") {
            const artifactPart = part as ArtifactPart;
            combinedText += `«artifact_return:${artifactPart.name}»`;
        }
    }

    const renderMetadata: Record<string, unknown> = {};
    if (message.metadata?.hidden) renderMetadata.hidden = message.metadata.hidden;
    if (message.metadata?.chip) renderMetadata.chip = message.metadata.chip;
    if (message.metadata?.applyChangesCard) renderMetadata.applyChangesCard = message.metadata.applyChangesCard;

    return {
        id: message.metadata?.messageId || `msg-${uuid()}`,
        type: message.isUser ? "user" : "agent",
        text: combinedText,
        parts: message.parts,
        uploadedFiles: message.uploadedFiles?.map(f => ({
            name: f.name,
            type: f.type,
        })),
        attachedArtifacts: message.attachedArtifacts,
        isError: message.isError,
        isWarning: message.isWarning,
        displayHtml: message.displayHtml,
        contextQuote: message.contextQuote,
        contextQuoteSourceId: message.contextQuoteSourceId,
        ...(message.progressUpdates && message.progressUpdates.length > 0 ? { progressUpdates: message.progressUpdates } : {}),
        ...((message.thinkingContent?.length ?? 0) > 0 ? { thinkingContent: message.thinkingContent, isThinkingComplete: message.isThinkingComplete ?? true } : {}),
        ...(message.userInputRequest ? { userInputRequest: message.userInputRequest } : {}),
        ...(message.authenticationLink && (message.authenticationLink.outcome || message.authenticationLink.rejected) ? { authenticationLink: message.authenticationLink } : {}),
        ...(Object.keys(renderMetadata).length > 0 ? { metadata: renderMetadata } : {}),
    };
}
