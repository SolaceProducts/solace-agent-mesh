// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useMemo } from "react";

import { useArtifactsWithWorking } from "@/api/artifacts";

import { useChatContext } from "@/providers/contexts/ChatContext";
import { type ChatMessageData, ChatMessageDataContext } from "@/providers/contexts/ChatMessageDataContext";

export function ChatMessageDataProvider({ children }: Readonly<{ children: ReactNode }>) {
    const {
        ragData,
        sessionId,
        selectedAgentName,
        isCollaborativeSession,
        hasSharedEditors,
        currentUserEmail,
        agentNameDisplayNameMap,
        configCollectFeedback,
        submittedFeedback,
        openSidePanelTab,
        setTaskIdInSidePanel,
        setExpandedDocumentFilename,
        handleFeedbackSubmit,
        addNotification,
        setPreviewArtifact,
        openDeleteModal,
        markArtifactAsDisplayed,
        downloadAndResolveArtifact,
        navigateArtifactVersion,
        displayError,
    } = useChatContext();

    const { artifacts, allArtifacts, showWorkingArtifacts } = useArtifactsWithWorking();

    const value = useMemo<ChatMessageData>(
        () => ({
            ragData,
            sessionId,
            selectedAgentName,
            isCollaborativeSession,
            hasSharedEditors,
            currentUserEmail,
            agentNameDisplayNameMap,
            configCollectFeedback,
            submittedFeedback,
            openSidePanelTab,
            setTaskIdInSidePanel,
            setExpandedDocumentFilename,
            handleFeedbackSubmit,
            addNotification,
            setPreviewArtifact,
            openDeleteModal,
            markArtifactAsDisplayed,
            downloadAndResolveArtifact,
            navigateArtifactVersion,
            displayError,
            artifacts,
            allArtifacts,
            showWorkingArtifacts,
        }),
        [
            ragData,
            sessionId,
            selectedAgentName,
            isCollaborativeSession,
            hasSharedEditors,
            currentUserEmail,
            agentNameDisplayNameMap,
            configCollectFeedback,
            submittedFeedback,
            openSidePanelTab,
            setTaskIdInSidePanel,
            setExpandedDocumentFilename,
            handleFeedbackSubmit,
            addNotification,
            setPreviewArtifact,
            openDeleteModal,
            markArtifactAsDisplayed,
            downloadAndResolveArtifact,
            navigateArtifactVersion,
            displayError,
            artifacts,
            allArtifacts,
            showWorkingArtifacts,
        ]
    );

    return <ChatMessageDataContext.Provider value={value}>{children}</ChatMessageDataContext.Provider>;
}
