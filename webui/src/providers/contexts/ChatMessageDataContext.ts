// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

import type { ArtifactInfo } from "@/types";

import type { ChatContextValue } from "./ChatContext";

export type ChatMessageData = Pick<
    ChatContextValue,
    | "ragData"
    | "sessionId"
    | "selectedAgentName"
    | "isCollaborativeSession"
    | "hasSharedEditors"
    | "currentUserEmail"
    | "agentNameDisplayNameMap"
    | "configCollectFeedback"
    | "submittedFeedback"
    | "openSidePanelTab"
    | "setTaskIdInSidePanel"
    | "setExpandedDocumentFilename"
    | "handleFeedbackSubmit"
    | "addNotification"
    | "setPreviewArtifact"
    | "openDeleteModal"
    | "markArtifactAsDisplayed"
    | "downloadAndResolveArtifact"
    | "navigateArtifactVersion"
    | "displayError"
> & {
    artifacts: ArtifactInfo[];
    allArtifacts: ArtifactInfo[];
    showWorkingArtifacts: boolean;
};

export const ChatMessageDataContext = createContext<ChatMessageData | undefined>(undefined);

export const useChatMessageData = (): ChatMessageData => {
    const context = useContext(ChatMessageDataContext);
    if (context === undefined) {
        throw new Error("useChatMessageData must be used within a ChatMessageDataProvider");
    }
    return context;
};
