// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any */
import { type FormEvent, type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useBooleanFlagDetails } from "@openfeature/react-sdk";
import { type QueryClient, useQueryClient } from "@tanstack/react-query";
import { Hammer } from "lucide-react";
import { useLoaderData, useNavigate } from "react-router-dom";

import { rereadFreeModelAfterReply } from "@/api/activation/reread";
import { markFreeCreditsUsedUp } from "@/api/activation/usedUpMark";
import { useAgentCards } from "@/api/agent-cards";
import { agentsApi, invalidateAgentRelations, useAgents, useUserAgentCards } from "@/api/agents";
import { artifactKeys, finalizeInProgressArtifacts, finalizeSessionArtifacts, useSessionArtifacts, useSessionArtifactsWithWorking } from "@/api/artifacts";
import { budgetStatusKeys, isBudgetRefusal, isBudgetSubmitRefusal } from "@/api/budget-status";
import { builderKeys } from "@/api/builder";
import { api } from "@/api/client";
import { connectorKeys } from "@/api/connectors";
import { connectorsApi } from "@/api/connectors/versioned";
import { remoteAgentsApi, useRemoteAgentCards, useRemoteAgents } from "@/api/remote-agents";
import { sessionKeys } from "@/api/sessions";
import { toolsetsApi, useCustomToolsets } from "@/api/toolsets";

import { BuilderModeEffects, BuilderSessionRedirector, BuilderTabAutoSwitch } from "@/providers/builder/BuilderEffects";
import { buildErrorMessage, buildSaveTaskPayload, type ChatEffect, deserializeChatMessages, isSubmissionDropped, mergeAgentCards, processChatEvent, selectInitialAgent, stampMissingCreatedAt } from "@/providers/chat";
import { ChatMessageDataProvider } from "@/providers/ChatMessageDataProvider";
import { UI_MODE, useUIModeContext } from "@/providers/contexts";
import { BUILDER_AGENT_NAME, BUILDER_AGENT_SESSION_ROUTES, BUILDER_SESSION_LIST_EXCLUDE_AGENT_IDS, useBuilderContext } from "@/providers/contexts/BuilderContext";
import { ChatContext, type ChatContextValue, type CustomSidePanelTab, defaultChatContextValue, type PendingPromptData, type SidePanelTabId } from "@/providers/contexts/ChatContext";
import { useConfigContext } from "@/providers/contexts/ConfigContext";
import { useProjectContext } from "@/providers/contexts/ProjectContext";
import { useTaskContext } from "@/providers/contexts/TaskContext";
import { useUserContext } from "@/providers/contexts/UserContext";
import { registerProjectDeletedCallback } from "@/providers/ProjectProvider";
import { applyActivityEventFrame } from "@/providers/task/activityEventStore";

import { useArtifactOperations } from "@/hooks/useArtifactOperations";
import { useArtifactPreview } from "@/hooks/useArtifactPreview";
import { useAutoGenerateTitle } from "@/hooks/useAutoGenerateTitle";
import { useBackgroundTaskMonitor } from "@/hooks/useBackgroundTaskMonitor";
import { useBuilderEnabled } from "@/hooks/useBuilderEnabled";
import { useCacheUserId } from "@/hooks/useCacheUserId";
import { useCollaborativeSession } from "@/hooks/useCollaborativeSession";
import { useSseReconnectGuard } from "@/hooks/useSseReconnectGuard";

import { ErrorDialog, useErrorDialog } from "@/common/dialogs";
import { report, reportFirstEver } from "@/common/product-analytics/report";
import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";
import { reportErrorDisplayed } from "@/common/product-analytics/reportErrorDisplayed";

import { getApiBearerToken, getErrorMessage } from "@/utils/api";
import { getLastUsedAgentName } from "@/utils/lastUsedAgent";
import { sanitizeRagData } from "@/utils/sourceUrlHelpers";
import { migrateTask } from "@/utils/taskMigration";
import { extractRagDataFromTasks } from "@/utils/taskUtils";
import { uuid } from "@/utils/uuid";

import { freeModelErrorKind } from "@/features/activation/freeModelErrors";
import { isBuilderConfigMime } from "@/features/builder/builderUtils";
import { TextSelectionProvider } from "@/features/chat/selection/TextSelectionProvider";
import { resolveWelcomeMessage } from "@/features/chat/welcomeMessage";
import { useConnectorSchemas } from "@/features/connectors/hooks/useConnectorSchemas";
import { useGatewaySchemas } from "@/features/entrypoints/hooks/useGatewaySchemas";

import type {
    Agent,
    AgentCardInfo,
    ArtifactInfo,
    ArtifactPart,
    AttachedArtifactRef,
    CancelTaskRequest,
    Connector,
    Gateway,
    Message,
    MessageFE,
    Notification,
    Part,
    Project,
    RAGSearchResult,
    RemoteAgent,
    SendStreamingMessageRequest,
    SendStreamingMessageSuccessResponse,
    Session,
    StoredTaskData,
    Task,
    TaskFE,
    TextPart,
    Toolset,
} from "@/types";

import { buildSubmitMessageMetadata, buildUserMessage, extractResponseTraceId, finalizeSubmittedTask, processSubmissionFiles } from "./chat/submitMessage";
import { buildSubmittedTrace, takeSubmittedTrace } from "./chat/submittedTrace";

const EMPTY_USER_AGENT_CARDS: AgentCardInfo[] = [];
const EMPTY_YAML_CARDS: AgentCardInfo[] = [];
const EMPTY_REMOTE_AGENTS: RemoteAgent[] = [];
const EMPTY_REMOTE_AGENT_CARDS: AgentCardInfo[] = [];
const EMPTY_ARTIFACTS: ArtifactInfo[] = [];
const BUILDER_FORBIDDEN_MESSAGE = "You don't have permission to use the Builder.";

let notificationIdCounter = 0;

type BufferedTaskData = { eventsBuffered: boolean; events: any[] };
type ReplayTask = { taskId: string; events: any[] };
type FeedbackRow = { taskId?: string; rating?: string; comment?: string | null };
type SaveTaskFn = (taskData: { taskId: string; userMessage?: string; messageBubbles: any[]; taskMetadata?: any }, overrideSessionId?: string) => Promise<boolean>;
type SetMessagesFn = (data: MessageFE[] | ((prev: MessageFE[]) => MessageFE[])) => void;

function parseAndMigrateStoredTasks(rawTasks: Array<Record<string, unknown> & { id?: string; taskId?: string }>) {
    const parsedTasks = rawTasks.map(task => ({
        ...(task as unknown as StoredTaskData),
        taskId: task.taskId ?? task.id ?? "",
        messageBubbles: JSON.parse(task.messageBubbles as string),
        taskMetadata: task.taskMetadata ? JSON.parse(task.taskMetadata as string) : null,
    }));
    return parsedTasks.map(migrateTask);
}

async function fetchBufferedEventsForSession(sessionId: string): Promise<Map<string, BufferedTaskData>> {
    const tasksWithBufferedEvents = new Map<string, BufferedTaskData>();
    try {
        const response = await api.webui.get(`/api/v1/sessions/${sessionId}/events/unconsumed?includeEvents=true`);
        if (response.hasEvents && response.eventsByTask) {
            for (const [taskId, taskEvents] of Object.entries(response.eventsByTask)) {
                const typedEvents = taskEvents as BufferedTaskData;
                if (typedEvents.eventsBuffered && typedEvents.events.length > 0) {
                    console.debug(`[loadSessionTasks] Task ${taskId} has ${typedEvents.events.length} buffered events (batched)`);
                    tasksWithBufferedEvents.set(taskId, typedEvents);
                }
            }
            console.debug(`[loadSessionTasks] Loaded buffered events for ${tasksWithBufferedEvents.size} tasks in single request`);
        }
    } catch (error) {
        console.warn(`[loadSessionTasks] Buffered event fetch failed:`, error);
    }
    return tasksWithBufferedEvents;
}

function buildMessagesFromTasks(migratedTasks: any[], sessionId: string, tasksWithBufferedEvents: Map<string, BufferedTaskData>): MessageFE[] {
    const allMessages: MessageFE[] = [];
    for (const task of migratedTasks) {
        const taskMessages = deserializeChatMessages(task, sessionId);
        if (tasksWithBufferedEvents.has(task.taskId)) {
            allMessages.push(...taskMessages.filter(m => m.isUser));
        } else {
            allMessages.push(...taskMessages);
        }
    }
    return allMessages;
}

function extractFeedbackRows(feedbackResp: { data?: unknown } | null): FeedbackRow[] {
    if (feedbackResp && !Array.isArray(feedbackResp.data)) {
        console.warn("Unexpected feedback response shape:", feedbackResp);
    }
    return Array.isArray(feedbackResp?.data) ? (feedbackResp.data as FeedbackRow[]) : [];
}

function buildFeedbackMap(feedbackRows: FeedbackRow[]): Record<string, { type: "up" | "down"; text: string }> {
    const feedbackMap: Record<string, { type: "up" | "down"; text: string }> = {};
    for (const f of feedbackRows) {
        if (!f.taskId || (f.rating !== "up" && f.rating !== "down")) continue;
        if (f.taskId in feedbackMap) continue;
        feedbackMap[f.taskId] = { type: f.rating, text: f.comment ?? "" };
    }
    return feedbackMap;
}

function extractLatestAgentName(migratedTasks: any[]): string | null {
    for (let i = migratedTasks.length - 1; i >= 0; i--) {
        let agentMeta = null;
        try {
            agentMeta = typeof migratedTasks[i].taskMetadata === "string" ? JSON.parse(migratedTasks[i].taskMetadata) : migratedTasks[i].taskMetadata;
        } catch {}
        if (agentMeta?.agentName) return agentMeta.agentName;
    }
    return null;
}

function categorizeBufferedTasks(migratedTasks: any[], sessionId: string, tasksWithBufferedEvents: Map<string, BufferedTaskData>): { finalMessages: MessageFE[]; tasksNeedingReplay: ReplayTask[]; tasksNeedingBufferCleanupOnly: string[] } {
    const finalMessages: MessageFE[] = [];
    const tasksNeedingReplay: ReplayTask[] = [];
    const tasksNeedingBufferCleanupOnly: string[] = [];

    for (const task of migratedTasks) {
        const taskMessages = deserializeChatMessages(task, sessionId);
        const bufferedData = tasksWithBufferedEvents.get(task.taskId);
        const agentMessages = taskMessages.filter(m => !m.isUser);
        const hasIncompleteArtifacts = agentMessages.some(msg => msg.parts?.some(part => part.kind === "artifact" && (part as ArtifactPart).status === "in-progress"));
        const needsBufferReplay = bufferedData && (agentMessages.length === 0 || hasIncompleteArtifacts);

        if (needsBufferReplay) {
            const reason = agentMessages.length === 0 ? "no saved agent response" : "incomplete artifacts";
            console.debug(`[loadSessionTasks] Task ${task.taskId} has buffered events and ${reason}, will replay through handleSseMessage`);
            finalMessages.push(...taskMessages.filter(m => m.isUser));
            tasksNeedingReplay.push({ taskId: task.taskId, events: bufferedData.events });
        } else {
            if (bufferedData && agentMessages.length > 0) {
                console.debug(`[loadSessionTasks] Task ${task.taskId} has buffered events but also has ${agentMessages.length} saved agent messages with complete artifacts - preferring chat_tasks data, scheduling buffer cleanup only`);
                tasksNeedingBufferCleanupOnly.push(task.taskId);
            }
            finalMessages.push(...taskMessages);
        }
    }
    return { finalMessages, tasksNeedingReplay, tasksNeedingBufferCleanupOnly };
}

function replayEventsThroughHandler(tasksNeedingReplay: ReplayTask[], handleSseMessageFn: (event: MessageEvent) => void, isReplayingEventsRef: { current: boolean }): void {
    for (const { taskId, events } of tasksNeedingReplay) {
        console.debug(`[loadSessionTasks] Replaying ${events.length} events for task ${taskId}`);
        isReplayingEventsRef.current = true;
        try {
            for (const bufferedEvent of events) {
                const ssePayload = bufferedEvent.data;
                if (ssePayload?.data) {
                    handleSseMessageFn({ data: ssePayload.data } as MessageEvent);
                }
            }
        } finally {
            isReplayingEventsRef.current = false;
        }
    }
}

async function persistReplayedTasks(currentMessages: MessageFE[], taskIdsToSave: string[], sessionId: string, saveTaskToBackend: SaveTaskFn): Promise<void> {
    for (const taskId of taskIdsToSave) {
        const taskMessages = currentMessages.filter(m => m.taskId === taskId && !m.isStatusBubble);
        const payload = buildSaveTaskPayload({ taskId, taskMessages });
        if (payload) {
            console.debug(`[loadSessionTasks] Saving task ${taskId} after replay (${taskMessages.length} messages)`);
            await saveTaskToBackend(payload, sessionId);
        } else {
            console.warn(`[loadSessionTasks] No messages found for task ${taskId} after replay`);
        }
    }
}

function saveReplayedTasksAfterFlush(taskIdsToSave: string[], sessionId: string, setMessages: SetMessagesFn, saveTaskToBackend: SaveTaskFn): void {
    setMessages(currentMessages => {
        setTimeout(() => {
            void persistReplayedTasks(currentMessages, taskIdsToSave, sessionId, saveTaskToBackend);
        }, 0);
        return currentMessages;
    });
}

function scheduleBufferCleanup(taskIds: string[]): void {
    console.debug(`[loadSessionTasks] Scheduling buffer cleanup only for ${taskIds.length} tasks`);
    setTimeout(async () => {
        for (const taskId of taskIds) {
            try {
                await api.webui.delete(`/api/v1/tasks/${taskId}/events/buffered`);
                console.debug(`[loadSessionTasks] Buffer cleanup successful for task ${taskId}`);
            } catch (error) {
                console.error(`[loadSessionTasks] Failed to clean up buffer for task ${taskId}:`, error);
            }
        }
    }, 100);
}

function markFreeCreditsUsedUpOnRefusal(queryClient: QueryClient, failureText: string): void {
    if (freeModelErrorKind(failureText) === "allowance") void markFreeCreditsUsedUp(queryClient);
}

interface ChatProviderProps {
    children: ReactNode;
    disableBuilderBridge?: boolean;
}

export const ChatProvider = ({ children, disableBuilderBridge = false }: ChatProviderProps) => {
    const { configWelcomeMessage, persistenceEnabled, configCollectFeedback, backgroundTasksDefaultTimeoutMs, platformConfigured } = useConfigContext();
    const { value: showThinkingContentEnabled } = useBooleanFlagDetails("show_thinking_content", false);

    const { mode, pinnedAgentName, seedWelcomeBubble } = useUIModeContext();
    const isAgentMode = mode === UI_MODE.agentMode;

    const { activeProject, setActiveProject, projects } = useProjectContext();
    const { registerTaskEarly } = useTaskContext();
    const { ErrorDialog: GlobalErrorDialog, setError } = useErrorDialog();
    const { autoTitleGenerationEnabled, autoGenerateTitle, autoGenerateTitleForTask } = useAutoGenerateTitle();
    const queryClient = useQueryClient();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const builderEnabled = useBuilderEnabled();
    const navigate = useNavigate();

    const [sessionId, setSessionId] = useState<string>("");
    const [messages, _setMessages] = useState<MessageFE[]>([]);
    const [isResponding, setIsResponding] = useState<boolean>(false);
    const [ragData, _setRagData] = useState<RAGSearchResult[]>([]);
    const [ragEnabled] = useState<boolean>(true);
    const [expandedDocumentFilename, setExpandedDocumentFilename] = useState<string | null>(null);
    const [notifications, setNotifications] = useState<Notification[]>([]);
    const [currentTaskId, setCurrentTaskId] = useState<string | null>(null);
    const submittedTaskTrace = useRef<{ taskId: string; traceId: string } | null>(null);
    const [selectedAgentName, setSelectedAgentName] = useState<string>("");
    const cacheUserId = useCacheUserId();
    const [isCancelling, setIsCancelling] = useState<boolean>(false);
    const [taskIdInSidePanel, setTaskIdInSidePanel] = useState<string | null>(null);
    const [activityEventTasks, setActivityEventTasks] = useState<Record<string, TaskFE>>({});
    const [isSidePanelCollapsed, setIsSidePanelCollapsed] = useState<boolean>(true);
    const [activeSidePanelTab, setActiveSidePanelTab] = useState<SidePanelTabId>("files");
    const [submittedFeedback, setSubmittedFeedback] = useState<Record<string, { type: "up" | "down"; text: string }>>({});
    const [pendingPrompt, setPendingPrompt] = useState<PendingPromptData | null>(null);
    const [pendingAgentName, setPendingAgentName] = useState<string | null>(null);
    const [turnDividerIndex, setTurnDividerIndex] = useState<number | null>(null);

    const [agentToEdit, setAgentToEdit] = useState<Agent | null>(null);
    const [connectorToEdit, setConnectorToEdit] = useState<Connector | null>(null);
    const [gatewayToEdit, setGatewayToEdit] = useState<Gateway | null>(null);
    const [customToolsetToEdit, setCustomToolsetToEdit] = useState<Toolset | null>(null);
    const [newlyCreatedToolsetId, setNewlyCreatedToolsetId] = useState<string | null>(null);
    const [errorObject, setErrorObject] = useState<{ title: string; message: string } | null>(null);

    const { isCollaborativeSession, hasSharedEditors, currentUserEmail, sessionOwnerName, sessionOwnerEmail, detectCollaborativeSession, resetCollaborativeState, getCurrentUserId } = useCollaborativeSession(sessionId);
    const { data: artifacts = EMPTY_ARTIFACTS, refetch: artifactsRefetch } = useSessionArtifacts(sessionId);
    const previewableArtifacts = useSessionArtifactsWithWorking(sessionId, artifacts);

    const canReadAgents = userHasCapability(CAPABILITIES.readAgents);
    const canReadToolsets = userHasCapability(CAPABILITIES.readToolsets);
    const canReadConnectors = userHasCapability(CAPABILITIES.readConnectors);
    const canReadGateways = userHasCapability(CAPABILITIES.readGateways);
    const { refetch: customToolsetsRefetch } = useCustomToolsets({ enabled: canReadToolsets });
    const { connectorSchemas, getConnectorSchema, refetch: connectorSchemasRefetch } = useConnectorSchemas({ enabled: canReadConnectors });
    const { data: yamlAgentCards = EMPTY_YAML_CARDS } = useAgentCards();
    const { refetch: userAgentsRefetch } = useAgents({ enabled: canReadAgents });
    const { data: userAgentCards = EMPTY_USER_AGENT_CARDS } = useUserAgentCards({ enabled: canReadAgents });
    const { data: remoteAgents = EMPTY_REMOTE_AGENTS, refetch: remoteAgentsRefetch } = useRemoteAgents();
    const { data: remoteAgentCards = EMPTY_REMOTE_AGENT_CARDS } = useRemoteAgentCards();
    const { gatewaySchemas, getGatewaySchema, refetch: gatewaySchemasRefetch } = useGatewaySchemas({ enabled: canReadGateways });

    const ragDataRef = useRef<RAGSearchResult[]>([]);
    const isCancellingRef = useRef(isCancelling);
    const currentSessionIdRef = useRef(sessionId);
    const startedSessionLoadRef = useRef(0);
    const appliedSessionLoadRef = useRef(0);
    const messagesRef = useRef<MessageFE[]>([]);
    const pendingSessionDividerRef = useRef(false);
    const allArtifactsRef = useRef<ArtifactInfo[]>([]);
    const backgroundTasksRef = useRef<typeof backgroundTasks>([]);
    const showThinkingContentEnabledRef = useRef(showThinkingContentEnabled);
    const currentEventSource = useRef<EventSource | null>(null);
    const savingTasksRef = useRef<Set<string>>(new Set());
    const cancelTimeoutRef = useRef<NodeJS.Timeout | null>(null);
    const isFinalizing = useRef(false);
    const finalizeReportedTaskIds = useRef<Set<string>>(new Set());
    const [latestStatusText, setLatestStatusText] = useState<string | null>(null);
    const sseEventSequenceRef = useRef<number>(0);
    const handleSseMessageRef = useRef<((event: MessageEvent) => void) | null>(null);
    const isReplayingEventsRef = useRef(false);
    const manualRetryTaskIdRef = useRef<string | null>(null);
    const deepResearchQueryHistoryRef = useRef<
        Map<
            string,
            Array<{
                query: string;
                timestamp: string;
                urls: Array<{ url: string; title: string; favicon: string; source_type?: string }>;
            }>
        >
    >(new Map());

    useEffect(() => {
        showThinkingContentEnabledRef.current = showThinkingContentEnabled;
    }, [showThinkingContentEnabled]);
    useEffect(() => {
        isCancellingRef.current = isCancelling;
    }, [isCancelling]);
    useEffect(() => {
        currentSessionIdRef.current = sessionId;
    }, [sessionId]);
    useEffect(() => {
        allArtifactsRef.current = artifacts;
    }, [artifacts]);

    const setMessages = useCallback((data: MessageFE[] | ((prev: MessageFE[]) => MessageFE[])) => {
        _setMessages(prev => {
            const computed = typeof data === "function" ? data(prev) : data;
            const newData = stampMissingCreatedAt(computed, Date.now());
            messagesRef.current = newData;
            return newData;
        });
    }, []);
    const setRagData = useCallback((data: RAGSearchResult[] | ((prev: RAGSearchResult[]) => RAGSearchResult[])) => {
        _setRagData(prev => {
            const raw = typeof data === "function" ? data(prev) : data;
            const newData = sanitizeRagData(raw);
            ragDataRef.current = newData;
            return newData;
        });
    }, []);

    const addNotification = useCallback((message: string, type?: "success" | "info" | "warning") => {
        setNotifications(prev => {
            const existingNotification = prev.find(n => n.message === message);

            if (existingNotification) {
                return prev;
            }

            const id = String(++notificationIdCounter);
            const newNotification = { id, message, type: type || "info" };

            setTimeout(() => {
                setNotifications(current => current.filter(n => n.id !== id));
            }, 4000);

            return [...prev, newNotification];
        });
    }, []);

    const {
        preview: { availableVersions: previewedArtifactAvailableVersions, currentVersion: currentPreviewedVersionNumber, content: previewFileContent },
        previewArtifact,
        openPreview,
        navigateToVersion,
        closePreview,
        setPreviewByArtifact,
    } = useArtifactPreview({
        sessionId,
        projectId: activeProject?.id,
        artifacts: previewableArtifacts,
        setError,
    });

    const {
        uploadArtifactFile,

        isDeleteModalOpen,
        artifactToDelete,
        openDeleteModal,
        closeDeleteModal,
        confirmDelete,

        isArtifactEditMode,
        setIsArtifactEditMode,
        selectedArtifactFilenames,
        setSelectedArtifactFilenames,
        isBatchDeleteModalOpen,
        setIsBatchDeleteModalOpen,
        handleDeleteSelectedArtifacts,
        confirmBatchDeleteArtifacts,

        downloadAndResolveArtifact,
    } = useArtifactOperations({
        sessionId,
        addNotification,
        setError,
        previewArtifact,
        closePreview,
    });

    const allAgentCards = useMemo(() => mergeAgentCards(yamlAgentCards, userAgentCards, remoteAgentCards), [yamlAgentCards, userAgentCards, remoteAgentCards]);
    const agentNameDisplayNameMap = useMemo<Record<string, string>>(() => Object.fromEntries(allAgentCards.filter(a => a.name).map(a => [a.name, a.displayName ?? a.name])), [allAgentCards]);

    const saveTaskToBackend = useCallback(
        async (taskData: { taskId: string; userMessage?: string; messageBubbles: any[]; taskMetadata?: any }, overrideSessionId?: string): Promise<boolean> => {
            const effectiveSessionId = overrideSessionId || sessionId;

            if (!persistenceEnabled || !effectiveSessionId) {
                return false;
            }

            if (savingTasksRef.current.has(taskData.taskId)) {
                return false;
            }

            savingTasksRef.current.add(taskData.taskId);

            try {
                await api.webui.post(`/api/v1/sessions/${effectiveSessionId}/chatTasks`, {
                    id: taskData.taskId,
                    userMessage: taskData.userMessage,
                    messageBubbles: JSON.stringify(taskData.messageBubbles),
                    taskMetadata: taskData.taskMetadata ? JSON.stringify(taskData.taskMetadata) : null,
                    autoGenerateTitle: autoTitleGenerationEnabled,
                });
                return true;
            } catch (error) {
                console.error(`Failed saving task ${taskData.taskId}:`, error);
                return false;
            } finally {
                savingTasksRef.current.delete(taskData.taskId);
            }
        },
        [sessionId, persistenceEnabled, autoTitleGenerationEnabled]
    );

    const scheduleReplayAndSave = useCallback(
        (tasksNeedingReplay: ReplayTask[], targetSessionId: string, loadId: number) => {
            setTimeout(() => {
                if (appliedSessionLoadRef.current !== loadId || currentSessionIdRef.current !== targetSessionId) return;
                const handleSseMessageFn = handleSseMessageRef.current;
                if (!handleSseMessageFn) {
                    console.error(`[loadSessionTasks] handleSseMessageRef not available for replay`);
                    return;
                }
                replayEventsThroughHandler(tasksNeedingReplay, handleSseMessageFn, isReplayingEventsRef);
                const taskIdsToSave = tasksNeedingReplay.map(t => t.taskId);
                saveReplayedTasksAfterFlush(taskIdsToSave, targetSessionId, setMessages, saveTaskToBackend);
            }, 50);
        },
        [setMessages, saveTaskToBackend]
    );

    const applyLoadedMessages = useCallback(
        (migratedTasks: any[], targetSessionId: string, tasksWithBufferedEvents: Map<string, BufferedTaskData>, allMessages: MessageFE[], loadId: number) => {
            if (!(handleSseMessageRef.current && tasksWithBufferedEvents.size > 0)) {
                setMessages(allMessages);
                return;
            }
            console.debug(`[loadSessionTasks] Processing ${migratedTasks.length} tasks with ${tasksWithBufferedEvents.size} having buffered events`);
            const { finalMessages, tasksNeedingReplay, tasksNeedingBufferCleanupOnly } = categorizeBufferedTasks(migratedTasks, targetSessionId, tasksWithBufferedEvents);
            setMessages(finalMessages);
            if (tasksNeedingReplay.length > 0) {
                console.debug(`[loadSessionTasks] Replaying ${tasksNeedingReplay.length} tasks through handleSseMessage`);
                scheduleReplayAndSave(tasksNeedingReplay, targetSessionId, loadId);
            }
            if (tasksNeedingBufferCleanupOnly.length > 0) {
                scheduleBufferCleanup(tasksNeedingBufferCleanupOnly);
            }
        },
        [setMessages, scheduleReplayAndSave]
    );

    const loadSessionTasks = useCallback(
        async (sessionId: string) => {
            const feedbackPromise = api.webui.get(`/api/v1/feedback?sessionId=${sessionId}&pageSize=100`).catch((err: unknown) => {
                console.warn("Failed to load persisted feedback:", err);
                return null;
            });
            const loadId = ++startedSessionLoadRef.current;
            const chatTasksResp = await api.webui.get(`/api/v1/sessions/${sessionId}/chatTasks`, { fullResponse: true });
            if (!chatTasksResp.ok && chatTasksResp.status !== 404) throw new Error(`HTTP ${chatTasksResp.status}`);
            const data = chatTasksResp.ok ? await chatTasksResp.json() : { data: [] };

            if (currentSessionIdRef.current !== sessionId) {
                console.log(`Session ${sessionId} is no longer the active session: ${currentSessionIdRef.current}`);
                return;
            }

            const migratedTasks = parseAndMigrateStoredTasks(data.data || []);

            const tasksWithBufferedEvents = handleSseMessageRef.current ? await fetchBufferedEventsForSession(sessionId) : new Map<string, BufferedTaskData>();

            const allMessages = buildMessagesFromTasks(migratedTasks, sessionId, tasksWithBufferedEvents);

            const feedbackRows = extractFeedbackRows(await feedbackPromise);
            if (currentSessionIdRef.current !== sessionId || loadId < appliedSessionLoadRef.current) return;
            appliedSessionLoadRef.current = loadId;
            setSubmittedFeedback(buildFeedbackMap(feedbackRows));

            const allRagData = extractRagDataFromTasks(migratedTasks);
            if (allRagData.length > 0) {
                setRagData(allRagData);
            }

            const agentName = extractLatestAgentName(migratedTasks);
            if (agentName) {
                setSelectedAgentName(agentName);
            }

            const mostRecentTask = migratedTasks.at(-1);
            if (mostRecentTask) {
                setTaskIdInSidePanel(mostRecentTask.taskId);
            }

            applyLoadedMessages(migratedTasks, sessionId, tasksWithBufferedEvents, allMessages, loadId);
        },
        [setRagData, applyLoadedMessages]
    );

    const authenticatedUserId = getCurrentUserId();
    const {
        backgroundTasks,
        notifications: backgroundNotifications,
        registerBackgroundTask,
        unregisterBackgroundTask,
        updateTaskTimestamp,
        isTaskRunningInBackground,
        checkTaskStatus,
    } = useBackgroundTaskMonitor({
        userId: authenticatedUserId,
        currentSessionId: sessionId,
        onTaskCompleted: useCallback(
            async (taskId: string, taskSessionId: string) => {
                if (typeof window !== "undefined") {
                    window.dispatchEvent(new CustomEvent("background-task-completed", { detail: { taskId } }));
                    window.dispatchEvent(new CustomEvent("new-chat-session"));
                }

                if (currentSessionIdRef.current === taskSessionId) {
                    await loadSessionTasks(taskSessionId);
                } else {
                    addNotification("Background task completed", "success");
                }

                await autoGenerateTitleForTask(taskId, backgroundTasksRef.current);
            },
            [addNotification, autoGenerateTitleForTask, loadSessionTasks]
        ),
        onTaskFailed: useCallback(
            (taskId: string, error: string, _taskSessionId: string) => {
                reportAndNotifyError(addNotification, error, "agent_task");

                if (typeof window !== "undefined") {
                    window.dispatchEvent(
                        new CustomEvent("background-task-completed", {
                            detail: { taskId },
                        })
                    );
                    window.dispatchEvent(new CustomEvent("new-chat-session"));
                }
            },
            [addNotification]
        ),
    });

    useEffect(() => {
        backgroundTasksRef.current = backgroundTasks;
    }, [backgroundTasks]);

    const [sessionName, setSessionName] = useState<string | null>(null);
    const [sessionToDelete, setSessionToDelete] = useState<Session | null>(null);
    const [isLoadingSession, setIsLoadingSession] = useState<boolean>(false);

    const openSidePanelTab = useCallback((tab: SidePanelTabId) => {
        setIsSidePanelCollapsed(false);
        setActiveSidePanelTab(tab);

        if (typeof window !== "undefined") {
            window.dispatchEvent(
                new CustomEvent("expand-side-panel", {
                    detail: { tab },
                })
            );
        }
    }, []);

    const closeCurrentEventSource = useCallback(() => {
        if (cancelTimeoutRef.current) {
            clearTimeout(cancelTimeoutRef.current);
            cancelTimeoutRef.current = null;
        }

        if (currentEventSource.current) {
            currentEventSource.current.close();
            currentEventSource.current = null;
        }
        isFinalizing.current = false;
    }, []);

    const handleSaveTask = useCallback(
        (payload: Extract<ChatEffect, { type: "save-task" }>["payload"]) => {
            const { taskId, messages: taskMessages, sessionId: taskSessionId, selectedAgentName: agentName, ragData: taskRagData } = payload;
            const savePayload = buildSaveTaskPayload({ taskId, taskMessages, agentName, ragData: taskRagData });
            if (!savePayload) return;

            const isBackgroundTask = isTaskRunningInBackground(taskId);
            const userMessageText = savePayload.userMessage;

            saveTaskToBackend(savePayload, taskSessionId)
                .then(async saved => {
                    if (saved) {
                        if (typeof window !== "undefined") {
                            window.dispatchEvent(new CustomEvent("new-chat-session"));
                        }
                    }

                    if (isBackgroundTask) {
                        unregisterBackgroundTask(taskId);
                    }

                    if (taskSessionId) {
                        const agentMessage = taskMessages.find(m => !m.isUser);
                        const agentResponseText =
                            agentMessage?.parts
                                ?.filter(p => p.kind === "text")
                                .map(p => (p as TextPart).text)
                                .join("") || "";

                        autoGenerateTitle(taskSessionId, userMessageText, agentResponseText);
                    }
                })
                .catch(error => {
                    console.error(`[ChatProvider] Error saving task ${taskId}:`, error);
                    if (isBackgroundTask) {
                        unregisterBackgroundTask(taskId);
                    }
                });
        },
        [saveTaskToBackend, isTaskRunningInBackground, unregisterBackgroundTask, autoGenerateTitle]
    );

    const finalizeTask = useCallback(
        (outcome: Extract<ChatEffect, { type: "finalize" }>["payload"]) => {
            const { taskId } = outcome;
            const alreadyReported = taskId !== undefined && finalizeReportedTaskIds.current.has(taskId);
            if (taskId !== undefined) {
                finalizeReportedTaskIds.current.add(taskId);
            }

            if (!alreadyReported) {
                const taskTrace = takeSubmittedTrace(submittedTaskTrace);
                if (outcome.failed) {
                    reportErrorDisplayed(outcome.failureText, "agent_task", taskTrace.traceId);
                    if (!outcome.replayed) markFreeCreditsUsedUpOnRefusal(queryClient, outcome.failureText);
                } else {
                    reportFirstEver("chat:first_response", taskTrace);
                }
            }
            isFinalizing.current = true;
            setTimeout(() => {
                isFinalizing.current = false;
            }, 100);
        },
        [queryClient]
    );

    const executeEffect = useCallback(
        (effect: ChatEffect) => {
            switch (effect.type) {
                case "close-connection":
                    closeCurrentEventSource();
                    break;
                case "stop-responding":
                    setIsResponding(false);
                    break;
                case "clear-task-id":
                    setCurrentTaskId(null);
                    break;
                case "cancel-complete":
                    if (isCancellingRef.current) {
                        addNotification("Task cancelled");
                        if (cancelTimeoutRef.current) clearTimeout(cancelTimeoutRef.current);
                        setIsCancelling(false);
                    }
                    break;
                case "finalize-artifacts": {
                    const finalizeSid = currentSessionIdRef.current;
                    if (finalizeSid) finalizeSessionArtifacts(queryClient, finalizeSid);
                    allArtifactsRef.current = finalizeInProgressArtifacts(allArtifactsRef.current) ?? allArtifactsRef.current;
                    break;
                }
                case "refetch-artifacts":
                    void artifactsRefetch();
                    {
                        const sid = currentSessionIdRef.current;
                        if (sid) {
                            void queryClient.invalidateQueries({ queryKey: [...builderKeys.manifests(), "content", sid] });
                            void queryClient.invalidateQueries({ queryKey: [...builderKeys.manifests(), "versions", sid] });
                        }
                    }
                    break;
                case "save-task":
                    handleSaveTask(effect.payload);
                    break;
                case "unregister-background-task":
                    unregisterBackgroundTask(effect.payload.taskId);
                    break;
                case "update-task-timestamp":
                    updateTaskTimestamp(effect.payload.taskId, effect.payload.timestamp);
                    break;
                case "auto-download-artifact":
                    setTimeout(() => {
                        downloadAndResolveArtifact(effect.payload.filename).catch(err => {
                            console.error(`Auto-download failed for ${effect.payload.filename}:`, err);
                        });
                    }, 100);
                    break;
                case "dispatch-new-chat-session":
                    if (typeof window !== "undefined") {
                        window.dispatchEvent(new CustomEvent("new-chat-session"));
                    }
                    break;
                case "finalize":
                    finalizeTask(effect.payload);
                    break;
                case "rpc-error":
                    if (!effect.payload.replayed) markFreeCreditsUsedUpOnRefusal(queryClient, effect.payload.message);
                    break;
            }
        },

        [addNotification, closeCurrentEventSource, artifactsRefetch, handleSaveTask, queryClient, downloadAndResolveArtifact, updateTaskTimestamp, unregisterBackgroundTask, finalizeTask]
    );

    const handleSseMessage = useCallback(
        (event: MessageEvent) => {
            sseEventSequenceRef.current += 1;

            try {
                const parsed = JSON.parse(event.data);
                const cu = parsed?.context_usage;
                if (cu && typeof cu.sessionId === "string") {
                    const agentName = selectedAgentName.length > 0 ? selectedAgentName : undefined;
                    queryClient.setQueryData(sessionKeys.contextUsage(cu.sessionId, agentName), cu);
                }
            } catch {}

            if (event.type === "final_response") rereadFreeModelAfterReply(queryClient);

            const output = processChatEvent({
                eventData: event.data,
                messages: messagesRef.current,
                ragData: ragDataRef.current,
                artifacts: allArtifactsRef.current,
                flags: {
                    showThinkingContent: showThinkingContentEnabledRef.current,
                    ragEnabled,
                    isReplaying: isReplayingEventsRef.current,
                },
                sessionId: currentSessionIdRef.current,
                selectedAgentName,
                deepResearchQueryHistory: deepResearchQueryHistoryRef.current,
                eventSequence: sseEventSequenceRef.current,
                isTaskRunningInBackground,
            });

            if (output.messages) {
                messagesRef.current = output.messages;
                setMessages(output.messages);
            }
            if (output.ragData) {
                setRagData(output.ragData);
            }
            if (output.artifacts) {
                allArtifactsRef.current = output.artifacts;
                queryClient.setQueryData(artifactKeys.session(sessionId), output.artifacts);
            }
            if (output.latestStatusText !== undefined) {
                setLatestStatusText(output.latestStatusText);
            }
            if (output.deepResearchQueryHistory) {
                deepResearchQueryHistoryRef.current = output.deepResearchQueryHistory;
            }

            for (const effect of output.effects) {
                executeEffect(effect);
            }
        },

        [ragEnabled, selectedAgentName, isTaskRunningInBackground, setMessages, setRagData, executeEffect, queryClient, sessionId]
    );
    const handleNewSessionCore = useCallback(
        async (preserveProjectContext: boolean = false) => {
            const log_prefix = "ChatProvider.handleNewSession:";

            closeCurrentEventSource();

            if (isResponding && currentTaskId && selectedAgentName && !isCancelling) {
                const isBackground = isTaskRunningInBackground(currentTaskId);
                if (!isBackground) {
                    api.webui
                        .post(`/api/v1/tasks/${currentTaskId}:cancel`, {
                            jsonrpc: "2.0",
                            id: `req-${uuid()}`,
                            method: "tasks/cancel",
                            params: { id: currentTaskId },
                        })
                        .catch(error => console.warn(`${log_prefix} Failed to cancel current task:`, error));
                }
            }

            if (cancelTimeoutRef.current) {
                clearTimeout(cancelTimeoutRef.current);
                cancelTimeoutRef.current = null;
            }
            setIsCancelling(false);

            setSessionId("");

            setSessionName(null);

            resetCollaborativeState();

            if (activeProject && !preserveProjectContext) {
                setActiveProject(null);
            } else if (activeProject && preserveProjectContext) {
                console.log(`${log_prefix} Preserving project context: ${activeProject.name}`);
            }

            setSelectedAgentName("");
            setPendingAgentName(null);
            setMessages([]);
            setActivityEventTasks({});
            setTurnDividerIndex(null);
            setIsResponding(false);
            setCurrentTaskId(null);
            setTaskIdInSidePanel(null);
            closePreview();
            isFinalizing.current = false;
            setLatestStatusText(null);

            sseEventSequenceRef.current = 0;
            setRagData([]);
            deepResearchQueryHistoryRef.current.clear();

            if (typeof window !== "undefined") {
                window.dispatchEvent(new CustomEvent("focus-chat-input"));
            }
        },
        [closeCurrentEventSource, isResponding, currentTaskId, selectedAgentName, isCancelling, resetCollaborativeState, activeProject, setMessages, closePreview, setRagData, isTaskRunningInBackground, setActiveProject]
    );

    const handleNewSession = useCallback(
        async (preserveProjectContext: boolean = false) => {
            await handleNewSessionCore(preserveProjectContext);
        },
        [handleNewSessionCore]
    );

    const startNewChatWithPrompt = useCallback(
        (promptData: PendingPromptData) => {
            setPendingPrompt(promptData);
            handleNewSession();
            if (promptData.agentName) setPendingAgentName(promptData.agentName);
        },
        [handleNewSession]
    );

    const clearPendingPrompt = useCallback(() => {
        setPendingPrompt(null);
    }, []);

    const handleSwitchSessionCore = useCallback(
        async (newSessionId: string) => {
            setIsLoadingSession(true);
            if (newSessionId !== currentSessionIdRef.current) appliedSessionLoadRef.current = ++startedSessionLoadRef.current;

            const currentSessionBgTasks = backgroundTasks.filter(t => t.sessionId === sessionId);
            const hasRunningBgTask = currentSessionBgTasks.some(t => t.taskId === currentTaskId);
            if (!hasRunningBgTask && !currentSessionBgTasks.length) {
                setMessages([]);
                setActivityEventTasks({});
            }
            setTurnDividerIndex(null);
            closeCurrentEventSource();

            if (isResponding && currentTaskId && selectedAgentName && !isCancelling && !isTaskRunningInBackground(currentTaskId)) {
                try {
                    await api.webui.post(`/api/v1/tasks/${currentTaskId}:cancel`, {
                        jsonrpc: "2.0",
                        id: `req-${uuid()}`,
                        method: "tasks/cancel",
                        params: { id: currentTaskId },
                    });
                } catch (error) {
                    console.warn("ChatProvider.handleSwitchSession: Failed to cancel current task:", error);
                }
            }

            if (cancelTimeoutRef.current) {
                clearTimeout(cancelTimeoutRef.current);
                cancelTimeoutRef.current = null;
            }
            setIsCancelling(false);

            const switchProjectContext = (session: Session | null) => {
                isSessionSwitchRef.current = true;
                if (!session?.project?.id) {
                    if (activeProject !== null) setActiveProject(null);
                    return;
                }
                if (activeProject?.id === session.project.id) return;
                const project = projects.find((p: Project) => p.id === session.project?.id);
                if (project) setActiveProject(project);
            };

            const reconnectBgTasks = async () => {
                const sessionBgTasks = backgroundTasks.filter(t => t.sessionId === newSessionId);
                for (const bgTask of sessionBgTasks) {
                    const status = await checkTaskStatus(bgTask.taskId);
                    if (status?.isRunning) {
                        setCurrentTaskId(bgTask.taskId);
                        setIsResponding(true);
                        if (bgTask.agentName) setSelectedAgentName(bgTask.agentName);
                        break;
                    }
                    unregisterBackgroundTask(bgTask.taskId);
                    autoGenerateTitleForTask(bgTask.taskId, backgroundTasks);
                }
            };

            let sessionLanded = false;
            try {
                const sessionResp = await api.webui.get(`/api/v1/sessions/${newSessionId}`, { fullResponse: true });
                if (!sessionResp.ok && sessionResp.status !== 404) throw new Error(`HTTP ${sessionResp.status}`);
                const session: Session | null = sessionResp.ok ? ((await sessionResp.json()) as Session) : null;
                setSessionName(session?.name ?? null);
                await detectCollaborativeSession(session, newSessionId);
                switchProjectContext(session);
                currentSessionIdRef.current = newSessionId;
                setSessionId(newSessionId);
                sessionLanded = true;
                setIsResponding(false);
                setCurrentTaskId(null);
                setTaskIdInSidePanel(null);
                closePreview();
                isFinalizing.current = false;
                setLatestStatusText(null);
                sseEventSequenceRef.current = 0;
                setRagData([]);
                deepResearchQueryHistoryRef.current.clear();
                await loadSessionTasks(newSessionId);
                pendingSessionDividerRef.current = true;
                await reconnectBgTasks();
            } catch (error) {
                setError({ title: "Switching Chats Failed", error: getErrorMessage(error, "Failed to switch chat sessions."), errorSource: "api" });
            } finally {
                setIsLoadingSession(false);
            }
            return sessionLanded;
        },
        [
            backgroundTasks,
            closeCurrentEventSource,
            isResponding,
            currentTaskId,
            selectedAgentName,
            isCancelling,
            sessionId,
            setMessages,
            isTaskRunningInBackground,
            detectCollaborativeSession,
            closePreview,
            setRagData,
            loadSessionTasks,
            activeProject,
            projects,
            setActiveProject,
            checkTaskStatus,
            unregisterBackgroundTask,
            autoGenerateTitleForTask,
            setError,
        ]
    );

    const handleSwitchSession = useCallback(
        async (newSessionId: string) => {
            return handleSwitchSessionCore(newSessionId);
        },
        [handleSwitchSessionCore]
    );

    const updateSessionName = useCallback(
        async (sessionIdToUpdate: string, newName: string) => {
            try {
                const response = await api.webui.patch(`/api/v1/sessions/${sessionIdToUpdate}`, { name: newName }, { fullResponse: true });

                if (response.status === 422) {
                    throw new Error("Invalid name");
                }

                if (!response.ok) {
                    const errorData = await response.json().catch(() => ({}));
                    throw new Error(errorData.message || "Failed to update session name");
                }

                if (sessionIdToUpdate === sessionId) {
                    setSessionName(newName);
                }
                if (typeof window !== "undefined") {
                    window.dispatchEvent(new CustomEvent("new-chat-session"));
                }
            } catch (error) {
                setError({ title: "Session Name Update Failed", error: getErrorMessage(error, "Failed to update session name."), errorSource: "api" });
            }
        },
        [sessionId, setError]
    );

    const deleteSession = useCallback(
        async (sessionIdToDelete: string) => {
            const orphanedTaskIds = backgroundTasksRef.current.filter(t => t.sessionId === sessionIdToDelete).map(t => t.taskId);
            for (const taskId of orphanedTaskIds) {
                unregisterBackgroundTask(taskId);
            }

            try {
                await api.webui.delete(`/api/v1/sessions/${sessionIdToDelete}`);
                addNotification("Session deleted.", "success");

                if (sessionIdToDelete === sessionId) {
                    handleNewSession();
                }
                if (typeof window !== "undefined") {
                    window.dispatchEvent(new CustomEvent("new-chat-session"));
                }
            } catch (error) {
                setError({ title: "Chat Deletion Failed", error: getErrorMessage(error, "Failed to delete session."), errorSource: "api" });
            }
        },
        [addNotification, handleNewSession, sessionId, setError, unregisterBackgroundTask]
    );

    const markArtifactAsDisplayed = useCallback(
        (filename: string, displayed: boolean) => {
            queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(sessionId), prev => (prev ?? []).map(artifact => (artifact.filename === filename ? { ...artifact, isDisplayed: displayed } : artifact)));
        },
        [queryClient, sessionId]
    );

    const openSessionDeleteModal = useCallback((session: Session) => {
        setSessionToDelete(session);
    }, []);

    const closeSessionDeleteModal = useCallback(() => {
        setSessionToDelete(null);
    }, []);

    const confirmSessionDelete = useCallback(async () => {
        if (sessionToDelete) {
            await deleteSession(sessionToDelete.id);
            setSessionToDelete(null);
        }
    }, [sessionToDelete, deleteSession]);

    const handleCancel = useCallback(async () => {
        if ((!isResponding && !isCancelling) || !currentTaskId) {
            return;
        }
        if (isCancelling) {
            return;
        }

        setIsCancelling(true);

        try {
            const cancelRequest: CancelTaskRequest = {
                jsonrpc: "2.0",
                id: `req-${uuid()}`,
                method: "tasks/cancel",
                params: { id: currentTaskId },
            };

            const response = await api.webui.post(`/api/v1/tasks/${currentTaskId}:cancel`, cancelRequest, { fullResponse: true });

            if (response.status === 202) {
                if (cancelTimeoutRef.current) clearTimeout(cancelTimeoutRef.current);
                cancelTimeoutRef.current = setTimeout(() => {
                    addNotification("Cancellation timed out. Allowing new input.");
                    setIsCancelling(false);
                    setIsResponding(false);
                    closeCurrentEventSource();
                    setCurrentTaskId(null);
                    cancelTimeoutRef.current = null;
                    setMessages(prev => prev.filter(msg => !msg.isStatusBubble));
                }, 15000);
            } else {
                const errorData = await response.json().catch(() => ({ message: "Unknown cancellation error" }));
                throw new Error(errorData.message || `HTTP error ${response.status}`);
            }
        } catch (error) {
            setError({ title: "Task Cancellation Failed", error: getErrorMessage(error, "An unknown error occurred."), errorSource: "api" });
            setIsCancelling(false);
        }
    }, [isResponding, isCancelling, currentTaskId, addNotification, closeCurrentEventSource, setMessages, setError]);

    const handleFeedbackSubmit = useCallback(
        async (taskId: string, feedbackType: "up" | "down", feedbackText: string) => {
            if (!sessionId) {
                console.error("Cannot submit feedback without a session ID.");
                return;
            }
            try {
                await api.webui.post("/api/v1/feedback", {
                    taskId,
                    sessionId,
                    feedbackType,
                    feedbackText,
                });
                setSubmittedFeedback(prev => ({
                    ...prev,
                    [taskId]: { type: feedbackType, text: feedbackText },
                }));
            } catch (error) {
                if ((error as { status?: number }).status === 409) {
                    const stored = await api.webui.get(`/api/v1/feedback?taskId=${taskId}&pageSize=1`).catch((err: unknown) => {
                        console.warn("Failed to load the recorded feedback after a conflict:", err);
                        return null;
                    });
                    const recorded = buildFeedbackMap(extractFeedbackRows(stored))[taskId];
                    if (recorded) {
                        setSubmittedFeedback(prev => ({ ...prev, [taskId]: recorded }));
                    }
                } else {
                    console.error("Failed to submit feedback:", error);
                }
                throw error;
            }
        },
        [sessionId]
    );

    const cleanupSseMessages = useCallback(() => {
        setMessages(prev => prev.filter(msg => !msg.isStatusBubble).map((m, i, arr) => (i === arr.length - 1 && !m.isUser ? { ...m, isComplete: true } : m)));
    }, [setMessages]);

    const requestManualRetry = useCallback(
        (taskId: string) => {
            closeCurrentEventSource();
            manualRetryTaskIdRef.current = taskId;
            setIsResponding(true);
            setCurrentTaskId(taskId);
        },
        [closeCurrentEventSource]
    );

    const { handleSseOpen, handleSseError, clearReconnectGraceTimer } = useSseReconnectGuard(
        {
            isResponding,
            isFinalizing,
            isCancelling: isCancellingRef,
            currentTaskId,
            getEventSource: () => currentEventSource.current,
        },
        {
            closeCurrentEventSource,
            setError,
            setIsResponding,
            setCurrentTaskId,
            setLatestStatusText,
            cleanupMessages: cleanupSseMessages,
            requestManualRetry,
            takeTaskTrace: () => takeSubmittedTrace(submittedTaskTrace).traceId,
        }
    );

    const cleanupUploadedFiles = useCallback(async (uploadedFiles: Array<{ filename: string; sessionId: string }>) => {
        if (uploadedFiles.length === 0) {
            return;
        }

        for (const { filename, sessionId: fileSessionId } of uploadedFiles) {
            try {
                await api.webui.delete(`/api/v1/artifacts/${fileSessionId}/${encodeURIComponent(filename)}`);
            } catch (error) {
                console.error(`[cleanupUploadedFiles] Exception while cleaning up file ${filename}:`, error);
            }
        }
    }, []);

    const isSubmissionBlocked = useCallback(
        (hasContent: boolean): boolean => {
            if (isSubmissionDropped({ hasContent, isResponding, isCancelling, selectedAgentName })) {
                return true;
            }
            if (selectedAgentName === BUILDER_AGENT_NAME && !builderEnabled) {
                reportAndNotifyError(addNotification, BUILDER_FORBIDDEN_MESSAGE, "client");
                return true;
            }
            return false;
        },
        [isResponding, isCancelling, selectedAgentName, builderEnabled, addNotification]
    );

    const handleSubmit = useCallback(
        async (
            event: FormEvent,
            files?: File[] | null,
            userInputText?: string | null,
            overrideSessionId?: string | null,
            displayHtml?: string | null,
            contextQuote?: string | null,
            contextQuoteSourceId?: string | null,
            artifactReferences?: AttachedArtifactRef[] | null,
            extraMetadata?: Record<string, unknown>
        ) => {
            event.preventDefault();
            const currentInput = userInputText?.trim() || "";
            const currentFiles = files || [];
            const currentArtifactRefs = artifactReferences || [];
            const hasContent = !!currentInput || currentFiles.length > 0 || currentArtifactRefs.length > 0;
            if (isSubmissionBlocked(hasContent)) {
                return;
            }
            closeCurrentEventSource();
            isFinalizing.current = false;
            setIsResponding(true);
            setCurrentTaskId(null);
            setLatestStatusText("Thinking");
            submittedTaskTrace.current = null;

            sseEventSequenceRef.current = 0;

            const userMsg = buildUserMessage({
                currentInput,
                currentFiles,
                currentArtifactRefs,
                displayHtml,
                contextQuote,
                contextQuoteSourceId,
                sessionId: overrideSessionId || sessionId,
                extraMetadata,
            });
            if (messagesRef.current.length > 0) {
                setTurnDividerIndex(messagesRef.current.length);
            }
            setMessages(prev => [...prev, userMsg]);

            try {
                const filesResult = await processSubmissionFiles({
                    currentFiles,
                    currentArtifactRefs,
                    effectiveSessionId: overrideSessionId || sessionId,
                    uploadArtifactFile,
                    cleanupUploadedFiles,
                });
                if (!filesResult.ok) {
                    setError({ title: "File Upload Failed", error: filesResult.errorMessage, errorSource: "api" });
                    setIsResponding(false);
                    setMessages(prev => prev.filter(msg => msg.metadata?.messageId !== userMsg.metadata?.messageId));
                    return;
                }
                const { uploadedFileParts, effectiveSessionId } = filesResult;

                const messageParts: Part[] = [];
                if (currentInput) {
                    messageParts.push({ kind: "text", text: currentInput });
                }

                messageParts.push(...uploadedFileParts);

                if (messageParts.length === 0) {
                    return;
                }

                console.log(`ChatProvider handleSubmit: Using effectiveSessionId for contextId: ${effectiveSessionId}`);

                const messageMetadata = buildSubmitMessageMetadata({
                    selectedAgentName,
                    backgroundTasksDefaultTimeoutMs,
                    activeProjectId: activeProject?.id,
                    extraMetadata,
                });

                const a2aMessage: Message = {
                    role: "user",
                    parts: messageParts,
                    messageId: `msg-${uuid()}`,
                    kind: "message",
                    contextId: effectiveSessionId,
                    metadata: messageMetadata,
                };

                console.log(`[ChatProvider] A2A message metadata:`, a2aMessage.metadata);

                const sendMessageRequest: SendStreamingMessageRequest = {
                    jsonrpc: "2.0",
                    id: `req-${uuid()}`,
                    method: "message/stream",
                    params: {
                        message: a2aMessage,
                    },
                };

                console.log("ChatProvider handleSubmit: Sending POST to /message:stream");
                const result: SendStreamingMessageSuccessResponse = await api.webui.post("/api/v1/message:stream", sendMessageRequest);

                const task = result?.result as Task | undefined;
                const taskId = task?.id;
                const responseSessionId = task?.contextId;
                const responseTraceId = extractResponseTraceId(result?.result);
                submittedTaskTrace.current = buildSubmittedTrace(taskId, responseTraceId);

                console.log(`ChatProvider handleSubmit: Extracted responseSessionId: ${responseSessionId}, current sessionId: ${sessionId}`);
                console.log(`ChatProvider handleSubmit: Full result object:`, result);

                if (!taskId) {
                    console.error("ChatProvider handleSubmit: Backend did not return a valid taskId. Result:", result);
                    throw new Error("Backend did not return a valid taskId.");
                }

                await finalizeSubmittedTask({
                    taskId,
                    responseSessionId,
                    sessionId,
                    userMsg,
                    currentInput,
                    currentFiles,
                    selectedAgentName,
                    autoTitleGenerationEnabled,
                    setSessionId,
                    setMessages,
                    setSessionName,
                    updateSessionName,
                    saveTaskToBackend,
                    registerTaskEarly,
                    registerBackgroundTask,
                    setCurrentTaskId,
                    setTaskIdInSidePanel,
                });
            } catch (error) {
                if (isBudgetRefusal(error)) queryClient.invalidateQueries({ queryKey: budgetStatusKeys.all });
                if (isBudgetSubmitRefusal(error)) {
                    const noticeText = getErrorMessage(error, "A usage budget refused this request.");
                    reportErrorDisplayed(noticeText, "api");
                    setMessages(prev => [...prev.filter(msg => !msg.isStatusBubble), buildErrorMessage(noticeText, true)]);
                } else {
                    setError({
                        title: "Message Failed",
                        error: getErrorMessage(error, "An error occurred. Please try again."),
                        errorSource: "api",
                        traceId: takeSubmittedTrace(submittedTaskTrace).traceId,
                    });
                    setMessages(prev => prev.filter(msg => !msg.isStatusBubble));
                }
                setIsResponding(false);
                setCurrentTaskId(null);
                isFinalizing.current = false;
                setLatestStatusText(null);
            }
        },
        [
            selectedAgentName,
            isSubmissionBlocked,
            closeCurrentEventSource,
            sessionId,
            setMessages,
            activeProject?.id,
            registerTaskEarly,
            uploadArtifactFile,
            cleanupUploadedFiles,
            setError,
            backgroundTasksDefaultTimeoutMs,
            autoTitleGenerationEnabled,
            updateSessionName,
            saveTaskToBackend,
            registerBackgroundTask,
            queryClient,
        ]
    );

    const prevProjectIdRef = useRef<string | null | undefined>("");
    const isSessionSwitchRef = useRef(false);
    const isSessionMoveRef = useRef(false);

    useEffect(() => {
        if (pendingSessionDividerRef.current && messages.length > 1) {
            pendingSessionDividerRef.current = false;
            let lastUserIdx = -1;
            for (let i = messages.length - 1; i >= 0; i--) {
                if (messages[i].isUser) {
                    lastUserIdx = i;
                    break;
                }
            }
            if (lastUserIdx > 0) {
                setTurnDividerIndex(lastUserIdx);
            }
        }
    }, [messages, setTurnDividerIndex]);

    useEffect(() => {
        const handleProjectDeleted = (deletedProjectId: string) => {
            if (activeProject?.id === deletedProjectId) {
                console.log(`Project ${deletedProjectId} was deleted, clearing session context`);
                handleNewSession(false);
            }
        };

        registerProjectDeletedCallback(handleProjectDeleted);
    }, [activeProject, handleNewSession]);

    useEffect(() => {
        const handleTitleUpdated = async (event: Event) => {
            const customEvent = event as CustomEvent;
            const { sessionId: updatedSessionId } = customEvent.detail;

            if (updatedSessionId === sessionId) {
                try {
                    const updatedSession: Session | null = await api.webui.get(`/api/v1/sessions/${updatedSessionId}`);
                    if (updatedSession?.name) {
                        setSessionName(updatedSession.name);
                    }
                } catch (error) {
                    console.error("[ChatProvider] Error fetching updated session name:", error);
                }
            }
        };

        window.addEventListener("session-title-updated", handleTitleUpdated);
        return () => {
            window.removeEventListener("session-title-updated", handleTitleUpdated);
        };
    }, [sessionId]);

    useEffect(() => {
        const handleSwitchToSession = (event: Event) => {
            const detail = (event as CustomEvent).detail;
            if (detail?.sessionId) {
                handleSwitchSession(detail.sessionId);
            }
        };
        window.addEventListener("switch-to-session", handleSwitchToSession);
        return () => {
            window.removeEventListener("switch-to-session", handleSwitchToSession);
        };
    }, [handleSwitchSession]);

    useEffect(() => {
        const handleSessionUpdated = async (event: Event) => {
            const customEvent = event as CustomEvent;
            const { sessionId: updatedSessionId, projectId } = customEvent.detail;

            if (projectId === undefined) return;

            if (updatedSessionId === sessionId) {
                isSessionMoveRef.current = true;

                if (projectId) {
                    const project = projects.find((p: Project) => p.id === projectId);
                    if (project) {
                        setActiveProject(project);
                    }
                } else {
                    setActiveProject(null);
                }
            }
        };

        window.addEventListener("session-updated", handleSessionUpdated);
        return () => {
            window.removeEventListener("session-updated", handleSessionUpdated);
        };
    }, [sessionId, projects, setActiveProject]);

    useEffect(() => {
        const prevId = prevProjectIdRef.current;
        const currentId = activeProject?.id;
        const isActivatingOrSwitching = currentId !== undefined && prevId !== currentId;

        if (isActivatingOrSwitching && !isSessionSwitchRef.current && !isSessionMoveRef.current) {
            console.log("Active project changed explicitly, resetting chat view and preserving project context.");
            handleNewSession(true);
        }
        prevProjectIdRef.current = currentId;
        isSessionSwitchRef.current = false;
        isSessionMoveRef.current = false;
    }, [activeProject, handleNewSession]);

    useEffect(() => {
        if (selectedAgentName || messages.length !== 0 || isLoadingSession) return;

        const cards = isAgentMode ? allAgentCards : yamlAgentCards;
        if (cards.length === 0) return;

        if (!isAgentMode && pendingAgentName && !cards.some(agent => agent.name === pendingAgentName)) return;

        const selectedAgent = selectInitialAgent({
            agents: cards,
            urlAgentName: isAgentMode ? pinnedAgentName : (pendingAgentName ?? new URLSearchParams(globalThis.location.search).get("agent")),
            projectDefaultAgentId: activeProject?.defaultAgentId,
            lastUsedAgentName: getLastUsedAgentName(cacheUserId),
            lastUsedCandidates: allAgentCards,
            mode: mode,
        });
        if (!selectedAgent) return;

        setSelectedAgentName(selectedAgent.name);
        if (pendingAgentName) setPendingAgentName(null);

        if (!seedWelcomeBubble) return;

        const displayedText = resolveWelcomeMessage(selectedAgent.welcome?.welcome_message, configWelcomeMessage, mode);
        setMessages([
            {
                parts: [{ kind: "text", text: displayedText }],
                isUser: false,
                isComplete: true,
                role: "agent",
                metadata: {
                    sessionId: "",
                    lastProcessedEventSequence: 0,
                },
            },
        ]);
    }, [yamlAgentCards, allAgentCards, configWelcomeMessage, messages.length, selectedAgentName, sessionId, isLoadingSession, activeProject, setMessages, isAgentMode, mode, pinnedAgentName, seedWelcomeBubble, cacheUserId, pendingAgentName]);

    const handleSseOpenRef = useRef(handleSseOpen);
    const handleSseErrorRef = useRef(handleSseError);
    const handleActivityEventRef = useRef((event: MessageEvent) => {
        setActivityEventTasks(prev => applyActivityEventFrame(prev, event.data));
    });

    useEffect(() => {
        handleSseMessageRef.current = handleSseMessage;
        handleSseOpenRef.current = handleSseOpen;
        handleSseErrorRef.current = handleSseError;
    }, [handleSseMessage, handleSseOpen, handleSseError]);

    useEffect(() => {
        if (currentTaskId) {
            const bearerToken = getApiBearerToken();

            const bgTask = backgroundTasksRef.current.find(t => t.taskId === currentTaskId);
            const isManualRetry = manualRetryTaskIdRef.current === currentTaskId;
            manualRetryTaskIdRef.current = null;
            const isReconnecting = bgTask !== undefined || isManualRetry;

            const params = new URLSearchParams();
            if (bearerToken) {
                params.append("token", bearerToken);
            }

            if (isReconnecting) {
                params.append("reconnect", "true");
                params.append("lastEventTimestamp", "0");
                console.log(`[ChatProvider] Reconnecting to background task ${currentTaskId} - requesting full event replay`);

                setMessages(prev => {
                    const filtered = prev.filter(msg => {
                        if (msg.isUser) return true;
                        if (msg.taskId !== currentTaskId) return true;
                        return false;
                    });
                    return filtered;
                });
            }

            const baseUrl = api.webui.getFullUrl(`/api/v1/sse/subscribe/${currentTaskId}`);
            const eventSourceUrl = params.toString() ? `${baseUrl}?${params.toString()}` : baseUrl;
            const eventSource = new EventSource(eventSourceUrl, { withCredentials: true });
            currentEventSource.current = eventSource;

            const wrappedHandleSseOpen = () => {
                handleSseOpenRef.current();
            };

            const wrappedHandleSseError = () => {
                handleSseErrorRef.current();
            };

            const wrappedHandleSseMessage = (event: MessageEvent) => {
                if (handleSseMessageRef.current) {
                    handleSseMessageRef.current(event);
                }
            };

            const wrappedHandleActivityEvent = (event: MessageEvent) => {
                handleActivityEventRef.current(event);
            };

            eventSource.onopen = wrappedHandleSseOpen;
            eventSource.onerror = wrappedHandleSseError;
            eventSource.addEventListener("status_update", wrappedHandleSseMessage);
            eventSource.addEventListener("artifact_update", wrappedHandleSseMessage);
            eventSource.addEventListener("final_response", wrappedHandleSseMessage);
            eventSource.addEventListener("error", wrappedHandleSseMessage);
            eventSource.addEventListener("activity_event", wrappedHandleActivityEvent);

            return () => {
                eventSource.removeEventListener("status_update", wrappedHandleSseMessage);
                eventSource.removeEventListener("artifact_update", wrappedHandleSseMessage);
                eventSource.removeEventListener("final_response", wrappedHandleSseMessage);
                eventSource.removeEventListener("error", wrappedHandleSseMessage);
                eventSource.removeEventListener("activity_event", wrappedHandleActivityEvent);
                eventSource.close();
                clearReconnectGraceTimer();
            };
        } else {
            closeCurrentEventSource();
        }
    }, [currentTaskId, closeCurrentEventSource, setMessages, clearReconnectGraceTimer]);

    const handleHeaderClick = useCallback(() => {
        navigate("/chat");
    }, [navigate]);

    const handleViewAgent = useCallback(
        (agent: Agent) => {
            const isRemoteAgent = remoteAgents.some(ra => ra.id === agent.id);
            if (isRemoteAgent) {
                navigate(`/agent-mesh/external/${agent.id}`);
            } else {
                navigate(`/agent-mesh/${agent.id}`);
            }
        },
        [navigate, remoteAgents]
    );

    const handleEditAgent = useCallback(
        (agent: Agent) => {
            setAgentToEdit(agent);
            navigate(`/agents/${agent.id}/edit`);
        },
        [navigate]
    );

    const handleCreateAgent = useCallback(
        (agent: Agent, prompt = "", replaceInHistoryStack = false) => {
            if (!replaceInHistoryStack) report("agent:creation_started");
            setAgentToEdit(agent);
            navigate("/agents/new/edit", { replace: replaceInHistoryStack, state: prompt });
        },
        [navigate]
    );

    const handleViewConnector = useCallback(
        (connector: Connector) => {
            navigate(`/connectors/${connector.id}`);
        },
        [navigate]
    );

    const handleEditConnector = useCallback(
        (connector: Connector) => {
            setConnectorToEdit(connector);
            navigate(`/connectors/${connector.id}/edit`);
        },
        [navigate]
    );

    const handleCreateConnector = useCallback(
        (connector: Connector) => {
            setConnectorToEdit(connector);
            navigate(`/connectors/new/edit?type=${connector.type}&subtype=${connector.subtype}`);
        },
        [navigate]
    );

    const handleViewCustomToolset = useCallback(
        (toolset: Toolset) => {
            navigate(`/toolsets/${toolset.id}`);
        },
        [navigate]
    );

    const handleEditCustomToolset = useCallback(
        (toolset: Toolset) => {
            setCustomToolsetToEdit(toolset);
            navigate(`/toolsets/${toolset.id}/edit`);
        },
        [navigate]
    );

    const handleCreateCustomToolset = useCallback(() => {
        navigate("/toolsets/new/edit");
    }, [navigate]);

    const handleDeleteAgent = useCallback(
        async (agent: Agent): Promise<boolean> => {
            if (!agent.id) return false;
            try {
                const isRemoteAgent = remoteAgents.some(ra => ra.id === agent.id);
                if (isRemoteAgent) {
                    await remoteAgentsApi.delete(agent.id);
                    await remoteAgentsRefetch();
                    addNotification("Remote agent deleted successfully.");
                } else {
                    await agentsApi.delete(agent.id);
                    addNotification("Agent deleted successfully.");
                }
                invalidateAgentRelations(queryClient);
                return true;
            } catch (error) {
                setErrorObject({ title: "Agent Deletion Failed", message: getErrorMessage(error, "An error occurred") });
                return false;
            }
        },
        [remoteAgents, remoteAgentsRefetch, queryClient, addNotification]
    );

    const handleDeleteConnector = useCallback(
        async (connector: Connector) => {
            if (!connector.id) return;
            try {
                await connectorsApi.delete(connector.id);
                await queryClient.invalidateQueries({ queryKey: connectorKeys.lists() });
                addNotification("Connector deleted successfully.");
            } catch (error) {
                setErrorObject({ title: "Connector Deletion Failed", message: error instanceof Error ? error.message : "An error occurred" });
            }
        },
        [queryClient, addNotification]
    );

    const handleDeleteCustomToolset = useCallback(
        async (toolset: Toolset) => {
            if (!toolset.id) return;
            try {
                await toolsetsApi.delete(toolset.id);
                await customToolsetsRefetch().then(() => addNotification("Toolset deleted successfully."));
            } catch (error) {
                setErrorObject({ title: "Toolset Deletion Failed", message: error instanceof Error ? error.message : "An error occurred" });
            }
        },
        [customToolsetsRefetch, addNotification]
    );

    const samConfigArtifact = useMemo(() => artifacts?.find(a => isBuilderConfigMime(a.mimeType ?? "")) ?? null, [artifacts]);

    const { builderMode, builderTabContent: BuilderTabContent } = useBuilderContext();
    const showBuilderTab = (builderMode || !!samConfigArtifact) && BuilderTabContent !== null;
    const builderCustomTabs = useMemo<CustomSidePanelTab[] | undefined>(
        () =>
            showBuilderTab
                ? [
                      {
                          id: "builder",
                          label: "Builder",
                          icon: Hammer,
                          content: (
                              <BuilderTabContent
                                  userAgentsRefetch={async () => {
                                      await userAgentsRefetch();
                                  }}
                              />
                          ),
                      },
                  ]
                : undefined,
        [showBuilderTab, BuilderTabContent, userAgentsRefetch]
    );

    const hasModelConfigWrite = userHasCapability(CAPABILITIES.createModelConfig);

    const contextValue = useMemo<ChatContextValue>(
        () => ({
            ...defaultChatContextValue,
            ragData,
            ragEnabled,
            expandedDocumentFilename,
            setExpandedDocumentFilename,
            configCollectFeedback,
            submittedFeedback,
            handleFeedbackSubmit,
            sessionId,
            setSessionId,
            sessionName,
            setSessionName,
            messages,
            setMessages,
            isResponding,
            isCollaborativeSession,
            hasSharedEditors,
            currentUserEmail,
            sessionOwnerName,
            sessionOwnerEmail,
            currentTaskId,
            isCancelling,
            latestStatusText,
            isLoadingSession,
            agentNameDisplayNameMap,
            handleNewSession,
            handleSwitchSession,
            handleSubmit,
            handleCancel,
            notifications,
            addNotification,
            selectedAgentName,
            setSelectedAgentName,
            uploadArtifactFile,
            isSidePanelCollapsed,
            activeSidePanelTab,
            setIsSidePanelCollapsed,
            setActiveSidePanelTab,
            openSidePanelTab,
            taskIdInSidePanel,
            setTaskIdInSidePanel,
            activityEventTasks,
            isDeleteModalOpen,
            artifactToDelete,
            openDeleteModal,
            closeDeleteModal,
            confirmDelete,
            openSessionDeleteModal,
            closeSessionDeleteModal,
            confirmSessionDelete,
            sessionToDelete,
            isArtifactEditMode,
            setIsArtifactEditMode,
            selectedArtifactFilenames,
            setSelectedArtifactFilenames,
            handleDeleteSelectedArtifacts,
            confirmBatchDeleteArtifacts,
            isBatchDeleteModalOpen,
            setIsBatchDeleteModalOpen,
            previewedArtifactAvailableVersions,
            currentPreviewedVersionNumber,
            previewFileContent,
            openArtifactForPreview: openPreview,
            navigateArtifactVersion: navigateToVersion,
            previewArtifact,
            setPreviewArtifact: setPreviewByArtifact,
            updateSessionName,
            deleteSession,

            markArtifactAsDisplayed,
            downloadAndResolveArtifact,

            displayError: setError,

            pendingPrompt,
            startNewChatWithPrompt,
            clearPendingPrompt,

            backgroundTasks,
            backgroundNotifications,
            isTaskRunningInBackground,

            hasModelConfigWrite,
            turnDividerIndex,

            sessionListExcludeAgentIds: BUILDER_SESSION_LIST_EXCLUDE_AGENT_IDS,
            agentSessionRoutes: BUILDER_AGENT_SESSION_ROUTES,
            customSidePanelTabs: builderCustomTabs,
            platformConfigured,
            agents: allAgentCards,
            connectorSchemas,
            getConnectorSchema,
            connectorSchemasRefetch,
            gatewaySchemas,
            getGatewaySchema,
            gatewaySchemasRefetch,
            agentToEdit,
            setAgentToEdit,
            connectorToEdit,
            setConnectorToEdit,
            gatewayToEdit,
            setGatewayToEdit,
            customToolsetToEdit,
            setCustomToolsetToEdit,
            newlyCreatedToolsetId,
            setNewlyCreatedToolsetId,
            handleHeaderClick,
            handleViewAgent,
            handleEditAgent,
            handleCreateAgent,
            handleDeleteAgent,
            handleViewConnector,
            handleEditConnector,
            handleCreateConnector,
            handleDeleteConnector,
            handleViewCustomToolset,
            handleEditCustomToolset,
            handleCreateCustomToolset,
            handleDeleteCustomToolset,
            navigate,
            useLoaderData,
        }),
        [
            ragData,
            ragEnabled,
            expandedDocumentFilename,
            configCollectFeedback,
            submittedFeedback,
            handleFeedbackSubmit,
            sessionId,
            sessionName,
            messages,
            setMessages,
            isResponding,
            isCollaborativeSession,
            hasSharedEditors,
            currentUserEmail,
            sessionOwnerName,
            sessionOwnerEmail,
            currentTaskId,
            isCancelling,
            latestStatusText,
            isLoadingSession,
            agentNameDisplayNameMap,
            handleNewSession,
            handleSwitchSession,
            handleSubmit,
            handleCancel,
            notifications,
            addNotification,
            selectedAgentName,
            uploadArtifactFile,
            isSidePanelCollapsed,
            activeSidePanelTab,
            openSidePanelTab,
            taskIdInSidePanel,
            activityEventTasks,
            isDeleteModalOpen,
            artifactToDelete,
            openDeleteModal,
            closeDeleteModal,
            confirmDelete,
            openSessionDeleteModal,
            closeSessionDeleteModal,
            confirmSessionDelete,
            sessionToDelete,
            isArtifactEditMode,
            setIsArtifactEditMode,
            selectedArtifactFilenames,
            setSelectedArtifactFilenames,
            handleDeleteSelectedArtifacts,
            confirmBatchDeleteArtifacts,
            isBatchDeleteModalOpen,
            setIsBatchDeleteModalOpen,
            previewedArtifactAvailableVersions,
            currentPreviewedVersionNumber,
            previewFileContent,
            openPreview,
            navigateToVersion,
            previewArtifact,
            setPreviewByArtifact,
            updateSessionName,
            deleteSession,
            markArtifactAsDisplayed,
            downloadAndResolveArtifact,
            setError,
            pendingPrompt,
            startNewChatWithPrompt,
            clearPendingPrompt,
            backgroundTasks,
            backgroundNotifications,
            isTaskRunningInBackground,
            hasModelConfigWrite,
            turnDividerIndex,
            builderCustomTabs,
            platformConfigured,
            allAgentCards,
            connectorSchemas,
            getConnectorSchema,
            connectorSchemasRefetch,
            gatewaySchemas,
            getGatewaySchema,
            gatewaySchemasRefetch,
            agentToEdit,
            connectorToEdit,
            gatewayToEdit,
            customToolsetToEdit,
            newlyCreatedToolsetId,
            handleHeaderClick,
            handleViewAgent,
            handleEditAgent,
            handleCreateAgent,
            handleDeleteAgent,
            handleViewConnector,
            handleEditConnector,
            handleCreateConnector,
            handleDeleteConnector,
            handleViewCustomToolset,
            handleEditCustomToolset,
            handleCreateCustomToolset,
            handleDeleteCustomToolset,
            navigate,
        ]
    );

    return (
        <ChatContext.Provider value={contextValue}>
            <TextSelectionProvider>
                {!disableBuilderBridge && (
                    <>
                        <BuilderSessionRedirector navigate={navigate} />
                        <BuilderTabAutoSwitch hasBuilderArtifact={!!samConfigArtifact} />
                        <BuilderModeEffects />
                    </>
                )}
                <ChatMessageDataProvider>{children}</ChatMessageDataProvider>
            </TextSelectionProvider>
            <GlobalErrorDialog />
            {errorObject && <ErrorDialog title={errorObject.title} open={!!errorObject} error={errorObject.message} errorSource="api" onOpenChange={open => !open && setErrorObject(null)} />}
        </ChatContext.Provider>
    );
};
