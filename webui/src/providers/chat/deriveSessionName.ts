// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MessageFE, TextPart } from "@/types";
import { internalToDisplayText } from "@/utils";

const SESSION_NAME_MAX_LENGTH = 100;

export function deriveSessionNameFromFirstMessage({ userMessage, files, autoTitleEnabled }: { userMessage: MessageFE; files: File[]; autoTitleEnabled: boolean }): string {
    if (autoTitleEnabled) return "New Chat";

    const textParts = userMessage.parts.filter(p => p.kind === "text") as TextPart[];
    const combinedText = textParts
        .map(p => p.text)
        .join(" ")
        .trim();

    if (combinedText) {
        let displayText = internalToDisplayText(combinedText);
        displayText = displayText.replace(/<user_id:[^>]*>?/g, "").trim();
        return displayText.length > SESSION_NAME_MAX_LENGTH ? `${displayText.substring(0, SESSION_NAME_MAX_LENGTH)}...` : displayText;
    }

    if (files.length === 1) return files[0].name;
    if (files.length > 1) return `${files[0].name} +${files.length - 1} more`;

    return "New Chat";
}
