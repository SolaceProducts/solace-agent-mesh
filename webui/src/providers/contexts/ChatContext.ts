// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { createContext, type FormEvent, type ReactNode, useContext } from "react";
import type { NavigateFunction, useLoaderData } from "react-router-dom";

import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

import type { useConnectorSchemas } from "@/features/connectors/hooks/useConnectorSchemas";
import type { useGatewaySchemas } from "@/features/entrypoints/hooks/useGatewaySchemas";

import type { Agent, AgentCardInfo, ArtifactInfo, AttachedArtifactRef, BackgroundTaskNotification, BackgroundTaskState, Connector, FileAttachment, Gateway, MessageFE, Notification, RAGSearchResult, Session, TaskFE, Toolset } from "@/types";

export interface CustomSidePanelTab {
    id: string;
    label: string;
    icon: React.ComponentType<{ className?: string }>;
    content: ReactNode;
    badge?: string;
}

export type SidePanelTabId = "files" | "activity" | "rag" | "builder";

export interface PendingPromptData {
    promptText: string;
    groupId: string;
    groupName: string;
    agentName?: string;
}

export interface ChatState {
    configCollectFeedback: boolean;
    sessionId: string;
    sessionName: string | null;
    messages: MessageFE[];
    isResponding: boolean;
    isCollaborativeSession: boolean;
    isSharedSession: boolean;
    hasSharedEditors: boolean;
    currentUserEmail: string;
    sessionOwnerName: string | null;
    sessionOwnerEmail: string | null;
    currentTaskId: string | null;
    selectedAgentName: string;
    notifications: Notification[];
    isCancelling: boolean;
    latestStatusText: string | null;
    isLoadingSession: boolean;
    agents: AgentCardInfo[];
    agentNameDisplayNameMap: Record<string, string>;
    taskIdInSidePanel: string | null;
    activityEventTasks: Record<string, TaskFE>;
    ragData: RAGSearchResult[];
    ragEnabled: boolean;
    expandedDocumentFilename: string | null;
    isSidePanelCollapsed: boolean;
    activeSidePanelTab: SidePanelTabId;
    customSidePanelTabs?: CustomSidePanelTab[];
    isDeleteModalOpen: boolean;
    artifactToDelete: ArtifactInfo | null;
    sessionToDelete: Session | null;
    isArtifactEditMode: boolean;
    selectedArtifactFilenames: Set<string>;
    isBatchDeleteModalOpen: boolean;
    previewArtifact: ArtifactInfo | null;
    previewedArtifactAvailableVersions: number[] | null;
    currentPreviewedVersionNumber: number | null;
    previewFileContent: FileAttachment | null;
    submittedFeedback: Record<string, { type: "up" | "down"; text: string }>;
    pendingPrompt: PendingPromptData | null;
    sessionListExcludeAgentIds?: string[];
    agentSessionRoutes?: Record<string, string>;
    backgroundTasks: BackgroundTaskState[];
    backgroundNotifications: BackgroundTaskNotification[];
    hasModelConfigWrite: boolean;
    turnDividerIndex: number | null;

    platformConfigured: boolean;

    connectorSchemas: ReturnType<typeof useConnectorSchemas>["connectorSchemas"];
    gatewaySchemas: ReturnType<typeof useGatewaySchemas>["gatewaySchemas"];

    agentToEdit: Agent | null;
    connectorToEdit: Connector | null;
    gatewayToEdit: Gateway | null;
    customToolsetToEdit: Toolset | null;
    newlyCreatedToolsetId: string | null;
}

export interface ChatActions {
    setSessionId: React.Dispatch<React.SetStateAction<string>>;
    setSessionName: React.Dispatch<React.SetStateAction<string | null>>;
    setMessages: React.Dispatch<React.SetStateAction<MessageFE[]>>;
    setTaskIdInSidePanel: React.Dispatch<React.SetStateAction<string | null>>;
    handleNewSession: (preserveProjectContext?: boolean) => Promise<void>;
    startNewChatWithPrompt: (promptData: PendingPromptData) => void;
    clearPendingPrompt: () => void;
    handleSwitchSession: (sessionId: string) => Promise<boolean>;
    handleSubmit: (
        event: FormEvent,
        files?: File[] | null,
        message?: string | null,
        overrideSessionId?: string | null,
        displayHtml?: string | null,
        contextQuote?: string | null,
        contextQuoteSourceId?: string | null,
        artifactReferences?: AttachedArtifactRef[] | null,
        extraMetadata?: Record<string, unknown>
    ) => Promise<void>;
    handleCancel: () => void;
    addNotification: (message: string, type?: "success" | "info" | "warning") => void;
    setSelectedAgentName: React.Dispatch<React.SetStateAction<string>>;
    uploadArtifactFile: (file: File, overrideSessionId?: string, description?: string, silent?: boolean) => Promise<{ uri: string; sessionId: string } | { error: string } | null>;
    setIsSidePanelCollapsed: React.Dispatch<React.SetStateAction<boolean>>;
    setActiveSidePanelTab: React.Dispatch<React.SetStateAction<SidePanelTabId>>;
    openSidePanelTab: (tab: SidePanelTabId) => void;

    openDeleteModal: (artifact: ArtifactInfo) => void;
    closeDeleteModal: () => void;
    confirmDelete: () => Promise<void>;
    openSessionDeleteModal: (session: Session) => void;
    closeSessionDeleteModal: () => void;
    confirmSessionDelete: () => Promise<void>;

    setIsArtifactEditMode: React.Dispatch<React.SetStateAction<boolean>>;
    setSelectedArtifactFilenames: React.Dispatch<React.SetStateAction<Set<string>>>;
    handleDeleteSelectedArtifacts: () => void;
    confirmBatchDeleteArtifacts: () => Promise<void>;
    setIsBatchDeleteModalOpen: React.Dispatch<React.SetStateAction<boolean>>;

    setPreviewArtifact: (artifact: ArtifactInfo | null) => void;
    openArtifactForPreview: (artifactFilename: string, sessionIdOverride?: string) => Promise<FileAttachment | null>;
    navigateArtifactVersion: (artifactFilename: string, targetVersion: number, sessionIdOverride?: string) => Promise<FileAttachment | null>;

    markArtifactAsDisplayed: (filename: string, displayed: boolean) => void;
    downloadAndResolveArtifact: (filename: string) => Promise<FileAttachment | null>;

    updateSessionName: (sessionId: string, newName: string) => Promise<void>;
    deleteSession: (sessionId: string) => Promise<void>;
    handleFeedbackSubmit: (taskId: string, feedbackType: "up" | "down", feedbackText: string) => Promise<void>;

    displayError: ({ title, error, errorSource }: { title: string; error: string; errorSource?: ProductAnalyticsErrorSource }) => void;

    isTaskRunningInBackground: (taskId: string) => boolean;

    setExpandedDocumentFilename: React.Dispatch<React.SetStateAction<string | null>>;

    connectorSchemasRefetch: ReturnType<typeof useConnectorSchemas>["refetch"];
    gatewaySchemasRefetch: ReturnType<typeof useGatewaySchemas>["refetch"];

    getConnectorSchema: ReturnType<typeof useConnectorSchemas>["getConnectorSchema"];
    getGatewaySchema: ReturnType<typeof useGatewaySchemas>["getGatewaySchema"];

    setAgentToEdit: React.Dispatch<React.SetStateAction<Agent | null>>;
    setConnectorToEdit: React.Dispatch<React.SetStateAction<Connector | null>>;
    setGatewayToEdit: React.Dispatch<React.SetStateAction<Gateway | null>>;
    setCustomToolsetToEdit: React.Dispatch<React.SetStateAction<Toolset | null>>;
    setNewlyCreatedToolsetId: React.Dispatch<React.SetStateAction<string | null>>;

    handleHeaderClick: () => void;

    handleViewAgent: (agent: Agent) => void;
    handleEditAgent: (agent: Agent) => void;
    handleCreateAgent: (agent: Agent, prompt?: string, replaceInHistoryStack?: boolean) => void;
    handleDeleteAgent: (agent: Agent) => Promise<boolean>;

    handleViewConnector: (connector: Connector) => void;
    handleEditConnector: (connector: Connector) => void;
    handleCreateConnector: (connector: Connector) => void;
    handleDeleteConnector: (connector: Connector) => Promise<void>;

    handleViewCustomToolset: (toolset: Toolset) => void;
    handleEditCustomToolset: (toolset: Toolset) => void;
    handleCreateCustomToolset: () => void;
    handleDeleteCustomToolset: (toolset: Toolset) => Promise<void>;

    navigate: NavigateFunction;
    useLoaderData: typeof useLoaderData;
}

export type ChatContextValue = ChatState & ChatActions;

export const ChatContext = createContext<ChatContextValue | undefined>(undefined);

export const useChatContext = (): ChatContextValue => {
    const context = useContext(ChatContext);
    if (context === undefined) {
        throw new Error("useChatContext must be used within a ChatProvider");
    }
    return context;
};

export const defaultChatContextValue: ChatContextValue = {
    configCollectFeedback: false,
    sessionId: "",
    sessionName: null,
    messages: [],
    isResponding: false,
    isCollaborativeSession: false,
    isSharedSession: false,
    hasSharedEditors: false,
    currentUserEmail: "",
    sessionOwnerName: null,
    sessionOwnerEmail: null,
    currentTaskId: null,
    selectedAgentName: "",
    notifications: [],
    isCancelling: false,
    latestStatusText: null,
    isLoadingSession: false,

    agents: [],
    agentNameDisplayNameMap: {},

    taskIdInSidePanel: null,
    activityEventTasks: {},

    ragData: [],
    ragEnabled: false,
    expandedDocumentFilename: null,

    isSidePanelCollapsed: false,
    activeSidePanelTab: "files",

    isDeleteModalOpen: false,
    artifactToDelete: null,
    sessionToDelete: null,
    isArtifactEditMode: false,
    selectedArtifactFilenames: new Set<string>(),
    isBatchDeleteModalOpen: false,
    previewArtifact: null,
    previewedArtifactAvailableVersions: null,
    currentPreviewedVersionNumber: null,
    previewFileContent: null,
    submittedFeedback: {},
    pendingPrompt: null,
    backgroundTasks: [],
    backgroundNotifications: [],
    hasModelConfigWrite: false,
    turnDividerIndex: null,

    setSessionId: () => {},
    setSessionName: () => {},
    setMessages: () => {},
    setTaskIdInSidePanel: () => {},
    handleNewSession: async () => {},
    startNewChatWithPrompt: () => {},
    clearPendingPrompt: () => {},
    handleSwitchSession: async () => false,
    handleSubmit: async () => {},
    handleCancel: () => {},
    addNotification: () => {},
    setSelectedAgentName: () => {},
    uploadArtifactFile: async () => null,
    setIsSidePanelCollapsed: () => {},
    setActiveSidePanelTab: () => {},
    openSidePanelTab: () => {},
    openDeleteModal: () => {},
    closeDeleteModal: () => {},
    confirmDelete: async () => {},
    openSessionDeleteModal: () => {},
    closeSessionDeleteModal: () => {},
    confirmSessionDelete: async () => {},
    setIsArtifactEditMode: () => {},
    setSelectedArtifactFilenames: () => {},
    handleDeleteSelectedArtifacts: () => {},
    confirmBatchDeleteArtifacts: async () => {},
    setIsBatchDeleteModalOpen: () => {},
    setPreviewArtifact: () => {},
    openArtifactForPreview: async () => null,
    navigateArtifactVersion: async () => null,
    markArtifactAsDisplayed: () => {},
    downloadAndResolveArtifact: async () => null,
    updateSessionName: async () => {},
    deleteSession: async () => {},
    handleFeedbackSubmit: async () => {},
    displayError: () => {},
    isTaskRunningInBackground: () => false,
    setExpandedDocumentFilename: () => {},

    platformConfigured: false,
    connectorSchemas: [],
    getConnectorSchema: () => undefined,
    connectorSchemasRefetch: async () => {},
    gatewaySchemas: [],
    getGatewaySchema: () => undefined,
    gatewaySchemasRefetch: async () => {},
    agentToEdit: null,
    setAgentToEdit: () => {},
    connectorToEdit: null,
    setConnectorToEdit: () => {},
    gatewayToEdit: null,
    setGatewayToEdit: () => {},
    customToolsetToEdit: null,
    setCustomToolsetToEdit: () => {},
    newlyCreatedToolsetId: null,
    setNewlyCreatedToolsetId: () => {},
    handleHeaderClick: () => {},
    handleViewAgent: () => {},
    handleEditAgent: () => {},
    handleCreateAgent: () => {},
    handleDeleteAgent: async () => false,
    handleViewConnector: () => {},
    handleEditConnector: () => {},
    handleCreateConnector: () => {},
    handleDeleteConnector: async () => {},
    handleViewCustomToolset: () => {},
    handleEditCustomToolset: () => {},
    handleCreateCustomToolset: () => {},
    handleDeleteCustomToolset: async () => {},
    navigate: (() => {}) as NavigateFunction,
    useLoaderData: (() => null) as unknown as typeof useLoaderData,
};
