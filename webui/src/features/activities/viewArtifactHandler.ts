// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { NavigateFunction } from "react-router-dom";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { type ArtifactInfo } from "@/types/chat";

interface ArtifactWithSession extends ArtifactInfo {
    sessionId: string;
}

export async function viewArtifactHandler(artifact: ArtifactInfo, version: number | undefined, chatContext: ChatContextValue, navigate: NavigateFunction): Promise<void> {
    const sessionId = (artifact as ArtifactWithSession).sessionId;
    if (!sessionId) return;

    await chatContext.handleSwitchSession(sessionId);
    chatContext.setActiveSidePanelTab("files");
    chatContext.setIsSidePanelCollapsed(false);
    chatContext.openArtifactForPreview(artifact.filename, sessionId);
    if (version !== undefined) {
        chatContext.navigateArtifactVersion(artifact.filename, version, sessionId);
    }
    navigate("/chat");
    requestAnimationFrame(() => {
        requestAnimationFrame(() => {
            chatContext.openSidePanelTab("files");
        });
    });
}
