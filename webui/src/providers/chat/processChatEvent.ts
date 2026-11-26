// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { checkHasVisibleContent, filterRenderableDataParts, isCompactionNotificationBubble } from "@/utils/messageProcessing";
import { uuid } from "@/utils/uuid";

import { isHiddenWorkingPart, partArtifactName } from "@/features/chat/artifactTags";
import { markPlanResponded } from "@/features/chat/research/respondedPlansStore";

import type {
    A2UISurface,
    ArtifactInfo,
    ArtifactPart,
    DataPart,
    FileAttachment,
    JSONRPCErrorResponse,
    MessageFE,
    Part,
    PartFE,
    ProgressUpdate,
    RAGSearchResult,
    RAGSearchType,
    SendStreamingMessageSuccessResponse,
    TaskStatusUpdateEvent,
    TextPart,
} from "@/types";
import { isRAGSearchType } from "@/types";

import { removeArtifactMarkers } from "./artifactMarkers";

const TASK_TIMEOUT_ERROR_CODE = -32001;

const BUDGET_EXHAUSTED_ERROR_CATEGORY = "budget_exhausted";

const BUDGET_STOP_FALLBACK_TEXT = "This workflow stopped because a usage budget was reached.";

function isBudgetStop(details: unknown): boolean {
    return (details as { llm_error_category?: unknown } | undefined)?.llm_error_category === BUDGET_EXHAUSTED_ERROR_CATEGORY;
}

function isNoticeError(error: { code: number; data?: unknown }): boolean {
    return error.code === TASK_TIMEOUT_ERROR_CODE || isBudgetStop(error.data);
}

function errorMessageText(error: { message: string }, isNotice: boolean): string {
    return isNotice ? error.message : `Error: ${error.message}`;
}

function isBudgetStoppedTask(result: { kind: string; metadata?: unknown }, isTaskFailed: boolean): boolean {
    return isTaskFailed && result.kind === "task" && isBudgetStop(result.metadata);
}

export function buildErrorMessage(text: string, isWarning: boolean, taskId?: string): MessageFE {
    return {
        role: "agent",
        parts: [{ kind: "text", text }],
        taskId,
        isUser: false,
        isError: true,
        isWarning,
        isComplete: true,
        metadata: { messageId: `msg-${uuid()}` },
    };
}

interface ThinkingContentPayload {
    type: "thinking_content";
    content: string;
    is_complete: boolean;
}

interface AgentProgressUpdatePayload {
    type: "agent_progress_update";
    status_text?: string;
}

interface ArtifactCreationProgressPayload {
    type: "artifact_creation_progress";
    filename: string;
    status: "in-progress" | "completed" | "failed" | "cancelled";
    bytes_transferred: number;
    mime_type?: string;
    description?: string;
    artifact_chunk?: string;
    version?: number;
    rolled_back_text?: string;
    tags?: string[];
}

interface AuthenticationRequiredPayload {
    type: "authentication_required";
    auth_uri?: string;
    target_agent?: string;
    gateway_task_id?: string;
    tool_display_name?: string;
    credential_key?: string;
}

interface AuthenticationResultPayload {
    type: "authentication_result";
    success?: boolean;
    target_agent?: string;
    target_agent_task_id?: string;
    credential_key?: string;
    auth_request_function_call_id?: string;
    tool_display_name?: string;
}

interface RagInfoUpdatePayload {
    type: "rag_info_update";
    title: string;
    query: string;
    search_type?: string;
    search_turn?: number;
    sources: Array<{
        url: string;
        fileId?: string;
        filename?: string;
        title: string;
        citationId?: string;
        sourceType?: string;
        sourceUrl?: string;
        contentPreview?: string;
        relevanceScore?: number;
        retrievedAt?: string;
        favicon?: string;
        source_type?: string;
        metadata?: Record<string, unknown>;
    }>;
    is_complete: boolean;
    timestamp: string;
}

interface DeepResearchProgressPayload {
    type: "deep_research_progress";
    phase: string;
    current_query: string;
    fetching_urls: Array<{ url: string; title: string; favicon: string; source_type?: string }>;
}

interface CompactionNotificationPayload {
    type: "compaction_notification";
}

interface ToolResultPayload {
    type: "tool_result";
    result_data?: {
        rag_metadata?: {
            query: string;
            title?: string;
            searchType: RAGSearchResult["searchType"];
            timestamp: string;
            sources: RAGSearchResult["sources"];
            metadata?: RAGSearchResult["metadata"];
        };
        [key: string]: unknown;
    };
    [key: string]: unknown;
}

interface UserInputRequestPayload {
    type: "user_input_request";
    request_id?: string;
    expires_at?: string;
    source?: string;
    surface?: unknown;
}

interface UserInputResponsePayload {
    type: "user_input_response";
    request_id?: string;
    status?: string;
}

interface ToolInvocationStartPayload {
    type: "tool_invocation_start";
}

interface DeepResearchPlanStalePayload {
    type: "deep_research_plan_stale";
    plan_id: string;
    reason: "timed_out";
}

interface GatewayTimeoutWarningPayload {
    type: "gateway_timeout_warning";
    message?: string;
}

interface ArtifactSavedPayload {
    type: "artifact_saved";
    filename: string;
    version?: number;
    mime_type?: string;
    size_bytes?: number;
    description?: string;
    function_call_id?: string;
    tags?: string[];
}

type ChatDataPartPayload =
    | ThinkingContentPayload
    | AgentProgressUpdatePayload
    | ArtifactCreationProgressPayload
    | ArtifactSavedPayload
    | AuthenticationRequiredPayload
    | AuthenticationResultPayload
    | RagInfoUpdatePayload
    | DeepResearchProgressPayload
    | CompactionNotificationPayload
    | ToolResultPayload
    | ToolInvocationStartPayload
    | UserInputRequestPayload
    | UserInputResponsePayload
    | DeepResearchPlanStalePayload
    | GatewayTimeoutWarningPayload;

function asTypedPayload(data: unknown): ChatDataPartPayload | null {
    if (typeof data !== "object" || data === null || !("type" in data)) return null;
    return typeof (data as Record<string, unknown>).type === "string" ? (data as ChatDataPartPayload) : null;
}

function isPendingAuthMatch(msg: MessageFE, taskId: string | undefined, credentialKey: string | undefined): boolean {
    const link = msg.authenticationLink;
    if (!link || msg.taskId !== taskId || link.outcome || link.rejected) return false;
    if (credentialKey && link.credentialKey) return link.credentialKey === credentialKey;
    return true;
}

function handleAuthenticationRequired(messages: MessageFE[], data: AuthenticationRequiredPayload, taskId: string | undefined): MessageFE[] {
    const authUri = data?.auth_uri;
    if (typeof authUri !== "string" || !authUri.startsWith("http")) return messages;

    const credentialKey = typeof data?.credential_key === "string" ? data.credential_key : undefined;
    const superseded = messages.map(msg => (isPendingAuthMatch(msg, taskId, credentialKey) ? { ...msg, authenticationLink: { ...msg.authenticationLink!, outcome: "failed" as const } } : msg));
    const authMessage: MessageFE = {
        role: "agent",
        parts: [{ kind: "text", text: "" }],
        taskId,
        authenticationLink: {
            url: authUri,
            text: "Authenticate",
            targetAgent: typeof data?.target_agent === "string" ? data.target_agent : "Agent",
            serviceName: typeof data?.tool_display_name === "string" ? data.tool_display_name : undefined,
            gatewayTaskId: typeof data?.gateway_task_id === "string" ? data.gateway_task_id : undefined,
            credentialKey,
        },
        isUser: false,
        isComplete: true,
        metadata: { messageId: `auth-${uuid()}` },
    };
    return [...superseded, authMessage];
}

function handleAuthenticationResult(messages: MessageFE[], data: AuthenticationResultPayload, taskId: string | undefined): MessageFE[] {
    const outcome = data?.success === true ? "authenticated" : "failed";
    const resultCredentialKey = typeof data?.credential_key === "string" ? data.credential_key : undefined;
    return messages.map(msg => (isPendingAuthMatch(msg, taskId, resultCredentialKey) ? { ...msg, authenticationLink: { ...msg.authenticationLink!, outcome } } : msg));
}

function appendTimeoutWarning(messages: MessageFE[], taskId: string | undefined, message: string | undefined): MessageFE[] {
    const warningId = `timeout-warning-${taskId ?? "task"}`;
    if (messages.some(m => m.metadata?.messageId === warningId)) return messages;
    return [
        ...messages,
        {
            role: "agent",
            taskId,
            parts: [{ kind: "text", text: message || "The agent is taking longer than expected. Still working…" }],
            isUser: false,
            isWarning: true,
            isComplete: true,
            metadata: { messageId: warningId },
        },
    ];
}

export interface QueryHistoryEntry {
    query: string;
    timestamp: string;
    urls: Array<{ url: string; title: string; favicon: string; source_type?: string }>;
}

export interface ChatEventInput {
    eventData: string;
    messages: MessageFE[];
    ragData: RAGSearchResult[];
    artifacts: ArtifactInfo[];
    flags: {
        showThinkingContent: boolean;
        ragEnabled: boolean;
        isReplaying: boolean;
    };
    sessionId: string;
    selectedAgentName: string;
    deepResearchQueryHistory: Map<string, QueryHistoryEntry[]>;
    eventSequence: number;
    isTaskRunningInBackground: (taskId: string) => boolean;
}

export interface ChatEventOutput {
    messages?: MessageFE[];
    ragData?: RAGSearchResult[];
    artifacts?: ArtifactInfo[];
    latestStatusText?: string | null;
    deepResearchQueryHistory?: Map<string, QueryHistoryEntry[]>;
    effects: ChatEffect[];
}

export type ChatEffect =
    | { type: "close-connection" }
    | { type: "stop-responding" }
    | { type: "clear-task-id" }
    | { type: "cancel-complete" }
    | { type: "refetch-artifacts" }
    | { type: "finalize-artifacts" }
    | {
          type: "save-task";
          payload: {
              taskId: string;
              messages: MessageFE[];
              sessionId: string;
              selectedAgentName: string;
              ragData: RAGSearchResult[];
          };
      }
    | { type: "unregister-background-task"; payload: { taskId: string } }
    | { type: "update-task-timestamp"; payload: { taskId: string; timestamp: number } }
    | { type: "auto-download-artifact"; payload: { filename: string } }
    | { type: "dispatch-new-chat-session" }
    | { type: "finalize"; payload: { failed: boolean; failureText: string; taskId: string | undefined; replayed: boolean } }
    | { type: "rpc-error"; payload: { message: string; replayed: boolean } };

function failureTextOf(result: unknown): string {
    const status = (result as { status?: { message?: { parts?: { kind?: string; text?: string }[] } } })?.status;
    return (status?.message?.parts ?? [])
        .filter(part => part.kind === "text" && typeof part.text === "string")
        .map(part => part.text)
        .join(" ");
}

export function processChatEvent(input: ChatEventInput): ChatEventOutput {
    const { eventData, flags, sessionId, eventSequence, isTaskRunningInBackground } = input;
    const deepResearchQueryHistory = new Map([...input.deepResearchQueryHistory].map(([k, v]) => [k, [...v]]));
    let messages = [...input.messages];
    let ragData: RAGSearchResult[] | undefined;
    let artifacts: ArtifactInfo[] | undefined;
    let latestStatusText: string | null | undefined;
    const effects: ChatEffect[] = [];

    const buildOutput = (): ChatEventOutput => {
        const output: ChatEventOutput = { messages, effects };
        if (ragData !== undefined) output.ragData = ragData;
        if (artifacts !== undefined) output.artifacts = artifacts;
        if (latestStatusText !== undefined) output.latestStatusText = latestStatusText;
        output.deepResearchQueryHistory = deepResearchQueryHistory;
        return output;
    };

    let rpcResponse: SendStreamingMessageSuccessResponse | JSONRPCErrorResponse;
    try {
        rpcResponse = JSON.parse(eventData) as SendStreamingMessageSuccessResponse | JSONRPCErrorResponse;
    } catch {
        console.error("Failed to parse SSE message");
        return { effects: [] };
    }

    if ("result" in rpcResponse && rpcResponse.result) {
        const result = rpcResponse.result;
        let taskIdFromResult: string | undefined;
        if (result.kind === "task") taskIdFromResult = result.id;
        else if (result.kind === "status-update") taskIdFromResult = result.taskId;
        if (taskIdFromResult && isTaskRunningInBackground(taskIdFromResult)) {
            effects.push({ type: "update-task-timestamp", payload: { taskId: taskIdFromResult, timestamp: Date.now() } });
        }
    }

    if ("error" in rpcResponse && rpcResponse.error) {
        const errorContent = rpcResponse.error;
        const isNotice = isNoticeError(errorContent);
        const messageContent = errorMessageText(errorContent, isNotice);

        const newMessages = messages.filter(msg => !msg.isStatusBubble);
        const lastStreamingIdx = newMessages.findLastIndex(msg => !msg.isUser && !msg.isComplete);
        if (lastStreamingIdx !== -1) {
            newMessages[lastStreamingIdx] = { ...newMessages[lastStreamingIdx], isComplete: true };
        }

        const erroredTaskId = lastStreamingIdx !== -1 ? newMessages[lastStreamingIdx].taskId : undefined;
        const erroredTaskIdForAuthCheck = erroredTaskId ?? newMessages.findLast(msg => !!msg.taskId)?.taskId;
        const rpcErrorAuthDenied = erroredTaskIdForAuthCheck !== undefined && newMessages.some(m => m.taskId === erroredTaskIdForAuthCheck && m.authenticationLink?.rejected);
        for (let i = 0; i < newMessages.length; i++) {
            const link = newMessages[i].authenticationLink;
            if (link && newMessages[i].taskId === erroredTaskId && !link.outcome && !link.rejected) {
                newMessages[i] = { ...newMessages[i], authenticationLink: { ...link, outcome: "failed" } };
            }
        }
        if (!rpcErrorAuthDenied) {
            const resolvedTaskId = erroredTaskId ?? newMessages.findLast(msg => !!msg.taskId)?.taskId;
            newMessages.push({ ...buildErrorMessage(messageContent, isNotice, resolvedTaskId), metadata: { messageId: `msg-${uuid()}`, lastProcessedEventSequence: eventSequence } });
        }

        effects.push({ type: "stop-responding" }, { type: "close-connection" }, { type: "clear-task-id" }, { type: "rpc-error", payload: { message: errorContent.message, replayed: flags.isReplaying } });
        return { messages: newMessages, effects };
    }

    if (!("result" in rpcResponse) || !rpcResponse.result) {
        console.warn("Received SSE message without a result or error field.", rpcResponse);
        return { effects: [] };
    }

    const result = rpcResponse.result;
    let isFinalEvent: boolean;
    let messageToProcess: { parts: Part[]; metadata?: Record<string, unknown> } | undefined;
    let currentTaskIdFromResult: string | undefined;

    switch (result.kind) {
        case "task":
            isFinalEvent = true;
            if (result.status?.message) {
                if (result.status.state === "failed") {
                    messageToProcess = result.status.message;
                } else {
                    const finalParts = (result.status.message.parts ?? []).filter(p => p.kind === "file");
                    if (finalParts.length > 0) {
                        messageToProcess = { parts: finalParts };
                    }
                }
            }
            currentTaskIdFromResult = result.id;
            break;
        case "status-update":
            isFinalEvent = result.final;
            messageToProcess = result.status?.message;
            currentTaskIdFromResult = result.taskId;
            break;
        case "artifact-update":
            effects.push({ type: "refetch-artifacts" });
            return { effects };
        default:
            console.warn("Received unknown result kind in SSE message:", result);
            return { effects };
    }

    if (messageToProcess?.parts) {
        const dataParts = messageToProcess.parts.filter(p => p.kind === "data") as DataPart[];
        if (dataParts.length > 0) {
            for (const part of dataParts) {
                const data = asTypedPayload(part.data);
                if (data) {
                    switch (data.type) {
                        case "thinking_content": {
                            if (!flags.showThinkingContent) {
                                const otherPartsThinking = messageToProcess.parts.filter(p => p.kind !== "data");
                                if (otherPartsThinking.length === 0) {
                                    return { effects };
                                }
                                break;
                            }

                            const { content: thinkingText, is_complete: isThinkingComplete } = data;

                            const thinkingUpdate: ProgressUpdate = {
                                type: "thinking" as const,
                                text: "Thinking",
                                timestamp: Date.now(),
                                expandableContent: thinkingText,
                                isExpandableComplete: isThinkingComplete,
                            };

                            messages = appendProgressUpdate(messages, currentTaskIdFromResult, eventSequence, thinkingUpdate, undefined, {
                                progressUpdater: existing => {
                                    const updates = [...existing];
                                    const lastThinkingIdx = updates.findLastIndex(p => p.type === "thinking");
                                    const lastThinkingEntry = lastThinkingIdx >= 0 ? updates[lastThinkingIdx] : null;

                                    if (lastThinkingEntry && !lastThinkingEntry.isExpandableComplete) {
                                        updates[lastThinkingIdx] = {
                                            ...lastThinkingEntry,
                                            expandableContent: (lastThinkingEntry.expandableContent || "") + thinkingText,
                                            isExpandableComplete: isThinkingComplete,
                                        };
                                    } else {
                                        updates.push(thinkingUpdate);
                                    }
                                    return updates;
                                },
                                messageUpdater: msg => ({
                                    thinkingContent: (msg.thinkingContent || "") + thinkingText,
                                    isThinkingComplete: isThinkingComplete,
                                }),
                            });

                            const otherPartsThinking = messageToProcess.parts.filter(p => p.kind !== "data");
                            if (otherPartsThinking.length === 0) {
                                break;
                            }
                            break;
                        }
                        case "agent_progress_update": {
                            const statusText = String(data?.status_text ?? "Processing...");
                            latestStatusText = statusText;

                            const progressUpdate: ProgressUpdate = {
                                type: "status",
                                text: statusText,
                                timestamp: Date.now(),
                            };

                            messages = appendProgressUpdate(
                                messages,
                                currentTaskIdFromResult,
                                eventSequence,
                                progressUpdate,
                                { isStatusBubble: false },
                                {
                                    progressUpdater: existing => {
                                        const updates = [...existing];
                                        const lastThinkingIdx = updates.findLastIndex((p: ProgressUpdate) => p.type === "thinking");
                                        if (lastThinkingIdx >= 0 && !updates[lastThinkingIdx].isExpandableComplete) {
                                            updates[lastThinkingIdx] = {
                                                ...updates[lastThinkingIdx],
                                                isExpandableComplete: true,
                                            };
                                        }
                                        updates.push(progressUpdate);
                                        return updates;
                                    },
                                    messageUpdater: msg => {
                                        const updates = msg.progressUpdates || [];
                                        const lastThinkingIdx = updates.findLastIndex((p: ProgressUpdate) => p.type === "thinking");
                                        const needsClose = lastThinkingIdx >= 0 && !updates[lastThinkingIdx].isExpandableComplete;
                                        return needsClose ? { isThinkingComplete: true } : {};
                                    },
                                }
                            );

                            const otherParts = messageToProcess.parts.filter(p => p.kind !== "data");
                            if (otherParts.length === 0) {
                                return buildOutput();
                            }
                            break;
                        }
                        case "artifact_creation_progress": {
                            const fromPeer = !!messageToProcess.metadata?.forwarded_from_peer;
                            const result = processArtifactCreationProgress(data, messages, artifacts ?? input.artifacts, currentTaskIdFromResult, eventSequence, sessionId, fromPeer);
                            messages = result.messages;
                            artifacts = result.artifacts;
                            effects.push(...result.effects);
                            return buildOutput();
                        }
                        case "artifact_saved": {
                            const fromPeer = !!messageToProcess.metadata?.forwarded_from_peer;
                            if (!fromPeer) {
                                const before = messages;
                                messages = finalizeArtifactCardFromSaved(messages, data, currentTaskIdFromResult, sessionId);
                                if (messages !== before) {
                                    effects.push({ type: "refetch-artifacts" });
                                }
                            }
                            const otherPartsAS = messageToProcess.parts.filter(p => p.kind !== "data");
                            if (otherPartsAS.length === 0) {
                                return buildOutput();
                            }
                            break;
                        }
                        case "tool_invocation_start":
                            break;
                        case "authentication_required": {
                            messages = handleAuthenticationRequired(messages, data, currentTaskIdFromResult);
                            break;
                        }
                        case "authentication_result": {
                            messages = handleAuthenticationResult(messages, data, currentTaskIdFromResult);
                            break;
                        }
                        case "user_input_request": {
                            const requestId = (data as UserInputRequestPayload).request_id;
                            const surface = (data as UserInputRequestPayload).surface;
                            const alreadyPresent = requestId && messages.some(m => m.userInputRequest?.requestId === String(requestId));
                            if (requestId && surface && !alreadyPresent) {
                                const hilMessage: MessageFE = {
                                    role: "agent",
                                    taskId: currentTaskIdFromResult,
                                    parts: [{ kind: "text", text: "" }],
                                    userInputRequest: {
                                        requestId: String(requestId),
                                        expiresAt: typeof (data as UserInputRequestPayload).expires_at === "string" ? (data as UserInputRequestPayload).expires_at! : "",
                                        source: (data as UserInputRequestPayload).source === "tool_approval" ? "tool_approval" : "ask_user_question",
                                        surface: surface as A2UISurface,
                                        taskId: currentTaskIdFromResult ?? "",
                                        agentName: input.selectedAgentName ?? "",
                                    },
                                    isUser: false,
                                    isComplete: false,
                                    metadata: { messageId: `hil-${requestId}` },
                                };
                                messages = messages.map(m =>
                                    m.userInputRequest && !m.userInputRequest.responded && !m.userInputRequest.timedOut && !m.userInputRequest.cancelled && m.userInputRequest.requestId !== String(requestId)
                                        ? { ...m, userInputRequest: { ...m.userInputRequest, cancelled: true } }
                                        : m
                                );
                                messages = [...messages, hilMessage];
                            }
                            break;
                        }
                        case "user_input_response": {
                            const requestId = (data as { request_id?: string; status?: string }).request_id;
                            const status = (data as { request_id?: string; status?: string }).status;
                            if (requestId) {
                                messages = messages.map(m =>
                                    m.userInputRequest?.requestId === String(requestId)
                                        ? {
                                              ...m,
                                              userInputRequest: {
                                                  ...m.userInputRequest,
                                                  responded: true,
                                                  timedOut: status === "timeout" ? true : m.userInputRequest.timedOut,
                                              },
                                          }
                                        : m
                                );
                            }
                            break;
                        }
                        case "rag_info_update": {
                            if (flags.ragEnabled) {
                                ragData = processRagInfoUpdate(data, ragData ?? input.ragData, currentTaskIdFromResult);
                            }
                            break;
                        }
                        case "deep_research_progress": {
                            latestStatusText = null;
                            const queryHistory = processDeepResearchProgress(data, deepResearchQueryHistory, currentTaskIdFromResult);
                            if (queryHistory) {
                                (data as DeepResearchProgressPayload & Record<string, unknown>).query_history = queryHistory;
                            }
                            break;
                        }
                        case "compaction_notification": {
                            latestStatusText = null;
                            break;
                        }
                        case "deep_research_plan_stale": {
                            const stalePlanId = data.plan_id;
                            markPlanResponded(stalePlanId, "stale");
                            for (let i = messages.length - 1; i >= 0; i--) {
                                const msg = messages[i];
                                if (!msg.parts) continue;
                                let touched = false;
                                const nextParts = msg.parts.map(p => {
                                    if (p.kind !== "data") return p;
                                    const d = (p as DataPart).data as { type?: string; plan_id?: string; responded?: string } | undefined;
                                    if (d?.type !== "deep_research_plan" || d.plan_id !== stalePlanId) return p;
                                    touched = true;
                                    return { ...(p as DataPart), data: { ...d, responded: "stale" } } as DataPart;
                                });
                                if (touched) {
                                    messages[i] = { ...msg, parts: nextParts };
                                    break;
                                }
                            }
                            break;
                        }
                        case "gateway_timeout_warning": {
                            messages = appendTimeoutWarning(messages, currentTaskIdFromResult, data.message);
                            break;
                        }
                        case "tool_result": {
                            if (flags.ragEnabled) {
                                const updatedRag = processToolResultRag(data, ragData ?? input.ragData, currentTaskIdFromResult);
                                if (updatedRag !== undefined) {
                                    ragData = updatedRag;
                                }
                            }
                            const resultData = (data as ToolResultPayload).result_data;
                            const resultDataType = resultData && (resultData as Record<string, unknown>).type;
                            if ((resultDataType === "redirect" || resultDataType === "request_build_activate") && messageToProcess) {
                                messageToProcess.parts.push({ kind: "data", data: resultData } as DataPart);
                            }
                            break;
                        }
                        default:
                            console.debug("Received unhandled data part type:", (data as { type: string }).type);
                    }
                }
            }
        }
    }

    const hasDeepResearchProgress = messageToProcess?.parts?.some(p => {
        if (p.kind === "data") {
            const dataPart = p as DataPart;
            return dataPart.data && dataPart.data.type === "deep_research_progress";
        }
        return false;
    });

    const newContentParts = filterRenderableDataParts(messageToProcess?.parts || [], !!hasDeepResearchProgress);
    const hasNewFiles = newContentParts.some(p => p.kind === "file");
    const authWasDeniedByUser = messages.some(m => m.taskId === currentTaskIdFromResult && m.authenticationLink?.rejected);
    const isTaskFailed = !authWasDeniedByUser && result.kind === "task" && result.status?.state === "failed";

    const responseMessageId = rpcResponse.id?.toString() ?? `msg-${uuid()}`;
    const responseContextId = "result" in rpcResponse && rpcResponse.result && "contextId" in rpcResponse.result ? (rpcResponse.result as unknown as { contextId?: string }).contextId : undefined;

    messages = applyContentToMessages(messages, newContentParts, {
        hasNewFiles,
        isFinalEvent,
        isTaskFailed,
        isBudgetStop: isBudgetStoppedTask(result, isTaskFailed),
        taskId: currentTaskIdFromResult,
        eventSequence,
        messageId: responseMessageId,
        contextId: responseContextId,
        fallbackSessionId: sessionId,
    });

    if (isFinalEvent) {
        latestStatusText = null;

        const terminalState = result.kind === "task" || result.kind === "status-update" ? result.status?.state : undefined;
        const authNotCompleted = isTaskFailed || terminalState === "canceled" || terminalState === "rejected";
        for (let i = 0; i < messages.length; i++) {
            const link = messages[i].authenticationLink;
            if (link && messages[i].taskId === currentTaskIdFromResult && !link.outcome && !link.rejected) {
                messages[i] = { ...messages[i], authenticationLink: { ...link, outcome: authNotCompleted ? "failed" : "authenticated" } };
            }
        }

        for (let i = 0; i < messages.length; i++) {
            const uir = messages[i].userInputRequest;
            if (uir && messages[i].taskId === currentTaskIdFromResult && !uir.responded && !uir.timedOut && !uir.cancelled) {
                messages[i] = { ...messages[i], userInputRequest: { ...uir, cancelled: true } };
            }
        }

        for (let i = messages.length - 1; i >= 0; i--) {
            const msg = messages[i];
            if (msg.taskId === currentTaskIdFromResult && msg.parts.some(p => p.kind === "artifact" && (p as ArtifactPart).status === "in-progress")) {
                const finalParts: PartFE[] = msg.parts.map(p => {
                    if (p.kind === "artifact" && (p as ArtifactPart).status === "in-progress") {
                        return { ...p, status: "failed", error: `Artifact creation for "${(p as ArtifactPart).name}" did not complete.` } as ArtifactPart;
                    }
                    return p;
                });
                messages[i] = { ...msg, parts: finalParts, isError: true, isComplete: true };
            }
        }

        const taskMessageIndex = messages.findLastIndex(msg => !msg.isUser && msg.taskId === currentTaskIdFromResult);
        if (taskMessageIndex !== -1) {
            messages[taskMessageIndex] = {
                ...messages[taskMessageIndex],
                isComplete: true,
                metadata: { ...messages[taskMessageIndex].metadata, lastProcessedEventSequence: eventSequence },
            };
            for (let i = taskMessageIndex - 1; i >= 0; i--) {
                const msg = messages[i];
                if (!msg.isUser && msg.taskId === currentTaskIdFromResult && !msg.isComplete) {
                    messages[i] = { ...msg, isComplete: true };
                }
            }
        }
        if (result.kind === "task" && result.status?.state !== "failed" && result.status?.message?.parts) {
            const partIsVisible = (p: PartFE): boolean => (p.kind === "text" && !!(p as { text?: string }).text?.trim()) || ((p.kind === "file" || p.kind === "artifact") && !isHiddenWorkingPart(p));
            const taskMessages = messages.filter(msg => !msg.isUser && msg.taskId === currentTaskIdFromResult);
            const taskHasVisibleContent = taskMessages.some(msg => msg.parts.some(partIsVisible));
            if (!taskHasVisibleContent) {
                const representedNames = new Set(taskMessages.flatMap(msg => msg.parts.map(partArtifactName).filter((name): name is string => !!name)));
                const adoptedTextParts: PartFE[] = (result.status.message.parts as PartFE[])
                    .filter((p): p is TextPart => p.kind === "text")
                    .map(p => ({ ...p, text: removeArtifactMarkers(p.text ?? "", name => representedNames.has(name)) }))
                    .filter(p => !!p.text.trim());
                if (adoptedTextParts.length > 0) {
                    if (taskMessageIndex === -1) {
                        messages.push({
                            role: "agent",
                            parts: adoptedTextParts,
                            taskId: currentTaskIdFromResult,
                            isUser: false,
                            isComplete: true,
                            metadata: {
                                messageId: rpcResponse.id?.toString() || `msg-${uuid()}`,
                                sessionId: (result as unknown as { contextId?: string }).contextId || sessionId,
                                lastProcessedEventSequence: eventSequence,
                            },
                        });
                    } else {
                        messages[taskMessageIndex] = {
                            ...messages[taskMessageIndex],
                            parts: [...messages[taskMessageIndex].parts, ...adoptedTextParts],
                            isComplete: true,
                        };
                    }
                }
            }
        }

        if (currentTaskIdFromResult && !flags.isReplaying) {
            const taskMessagesToSave = messages.filter(msg => msg.taskId === currentTaskIdFromResult && !msg.isStatusBubble);
            if (taskMessagesToSave.length > 0) {
                const taskSessionId = (result as TaskStatusUpdateEvent).contextId || sessionId;
                const currentRagData = ragData ?? input.ragData;
                const taskRagSnapshot = currentRagData.filter(r => r.taskId === currentTaskIdFromResult);
                effects.push({
                    type: "save-task",
                    payload: {
                        taskId: currentTaskIdFromResult,
                        messages: taskMessagesToSave,
                        sessionId: taskSessionId,
                        selectedAgentName: input.selectedAgentName,
                        ragData: taskRagSnapshot,
                    },
                });
            } else if (isTaskRunningInBackground(currentTaskIdFromResult)) {
                effects.push({ type: "unregister-background-task", payload: { taskId: currentTaskIdFromResult } }, { type: "dispatch-new-chat-session" });
            }
        }

        messages = messages.map(msg => {
            if (msg.isUser) return msg;
            const hasInProgressArtifacts = msg.parts.some(p => p.kind === "artifact" && (p as ArtifactPart).status === "in-progress");
            if (!hasInProgressArtifacts) return msg;

            return {
                ...msg,
                parts: msg.parts.map(part => {
                    if (part.kind === "artifact" && (part as ArtifactPart).status === "in-progress") {
                        const artifactPart = part as ArtifactPart;
                        const fileAttachment: FileAttachment = {
                            name: artifactPart.name,
                            mimeType: artifactPart.file?.mimeType,
                            uri: `artifact://${sessionId}/${artifactPart.name}`,
                        };
                        const sweptPart: ArtifactPart = { ...artifactPart, status: "completed", file: fileAttachment };
                        delete sweptPart.bytesTransferred;
                        return sweptPart;
                    }
                    return part;
                }),
            };
        });

        effects.push(
            { type: "cancel-complete" },
            { type: "stop-responding" },
            { type: "close-connection" },
            { type: "clear-task-id" },
            { type: "finalize-artifacts" },
            { type: "refetch-artifacts" },
            { type: "finalize", payload: { failed: isTaskFailed, failureText: failureTextOf(result), taskId: currentTaskIdFromResult, replayed: flags.isReplaying } }
        );

        if (currentTaskIdFromResult) {
            deepResearchQueryHistory.delete(currentTaskIdFromResult);
        }
    }

    return buildOutput();
}

function appendProgressUpdate(
    messages: MessageFE[],
    taskId: string | undefined,
    eventSequence: number,
    update: ProgressUpdate,
    extraFields?: Partial<MessageFE>,
    options?: {
        progressUpdater?: (existing: ProgressUpdate[]) => ProgressUpdate[];
        messageUpdater?: (msg: MessageFE) => Partial<MessageFE>;
    }
): MessageFE[] {
    const result = [...messages];
    const lastMsg = result[result.length - 1];
    if (lastMsg && !lastMsg.isUser && !lastMsg.isWarning && !lastMsg.userInputRequest && !lastMsg.authenticationLink && lastMsg.taskId === taskId) {
        const updatedProgressUpdates = options?.progressUpdater ? options.progressUpdater(lastMsg.progressUpdates || []) : [...(lastMsg.progressUpdates || []), update];
        const msgExtra = options?.messageUpdater ? options.messageUpdater(lastMsg) : {};
        result[result.length - 1] = {
            ...lastMsg,
            progressUpdates: updatedProgressUpdates,
            ...msgExtra,
            ...extraFields,
        };
    } else {
        result.push({
            role: "agent",
            parts: [],
            taskId,
            isUser: false,
            isComplete: false,
            progressUpdates: [update],
            metadata: {
                messageId: `msg-${uuid()}`,
                lastProcessedEventSequence: eventSequence,
            },
            ...extraFields,
        });
    }
    return result;
}

function mergeArtifactProgress(existing: ArtifactInfo, data: ArtifactCreationProgressPayload, sessionId: string): ArtifactInfo {
    const { filename, status, bytes_transferred, mime_type: mimeType, description, artifact_chunk, tags } = data;
    const isDisplayed = existing.isDisplayed ?? false;
    const isInProgressChunk = status === "in-progress" && !!artifact_chunk;

    let accumulatedContent: string | undefined;
    if (isInProgressChunk) {
        accumulatedContent = (existing.accumulatedContent ?? "") + artifact_chunk;
    } else if (status === "completed" && !isDisplayed) {
        accumulatedContent = undefined;
    } else {
        accumulatedContent = existing.accumulatedContent;
    }

    return {
        ...existing,
        description: description ?? existing.description,
        size: bytes_transferred || existing.size,
        lastModified: existing.creationStatus === "in-progress" ? existing.lastModified : new Date().toISOString(),
        uri: existing.uri ?? `artifact://${sessionId}/${filename}`,
        accumulatedContent,
        isAccumulatedContentPlainText: isInProgressChunk ? true : existing.isAccumulatedContentPlainText,
        mimeType: status === "completed" && mimeType ? mimeType : existing.mimeType,
        needsEmbedResolution: status === "completed" ? true : existing.needsEmbedResolution,
        creationStatus: artifactCreationStatus(status),
        tags: tags ?? existing.tags,
    };
}

function artifactCreationStatus(status: ArtifactCreationProgressPayload["status"]): ArtifactInfo["creationStatus"] {
    if (status === "completed") return "completed";
    if (status === "failed") return "failed";
    return "in-progress";
}

function newArtifactFromProgress(data: ArtifactCreationProgressPayload, sessionId: string): ArtifactInfo {
    const { filename, status, bytes_transferred, mime_type: mimeType, description, artifact_chunk, tags } = data;
    const isInProgressChunk = status === "in-progress" && !!artifact_chunk;
    return {
        filename,
        description: description ?? null,
        mimeType: mimeType ?? "application/octet-stream",
        size: bytes_transferred ?? 0,
        lastModified: new Date().toISOString(),
        uri: `artifact://${sessionId}/${filename}`,
        accumulatedContent: isInProgressChunk ? artifact_chunk : undefined,
        isAccumulatedContentPlainText: isInProgressChunk,
        needsEmbedResolution: status === "completed",
        creationStatus: artifactCreationStatus(status),
        tags,
    };
}

function processArtifactCreationProgress(
    data: ArtifactCreationProgressPayload,
    inputMessages: MessageFE[],
    inputArtifacts: ArtifactInfo[],
    currentTaskIdFromResult: string | undefined,
    eventSequence: number,
    sessionId: string,
    forwardedFromPeer: boolean
): { messages: MessageFE[]; artifacts: ArtifactInfo[]; effects: ChatEffect[] } {
    const { filename, status, bytes_transferred, mime_type: mimeType, description, version, rolled_back_text, tags } = data;

    const effects: ChatEffect[] = [];
    let artifactsList = inputArtifacts;
    if (status === "cancelled") {
        artifactsList = inputArtifacts.filter(a => a.filename !== filename);
    } else {
        const existingIndex = inputArtifacts.findIndex(a => a.filename === filename);
        const existing = existingIndex >= 0 ? inputArtifacts[existingIndex] : undefined;
        if (existingIndex >= 0) {
            artifactsList = [...inputArtifacts];
            artifactsList[existingIndex] = mergeArtifactProgress(inputArtifacts[existingIndex], data, sessionId);
        } else if (description !== undefined || status === "in-progress") {
            artifactsList = [...inputArtifacts, newArtifactFromProgress(data, sessionId)];
        }
        if (status === "completed" && existing?.isDisplayed) {
            effects.push({ type: "auto-download-artifact", payload: { filename } });
        }
        if (status === "completed") {
            effects.push({ type: "refetch-artifacts" });
        }
    }

    if (forwardedFromPeer) {
        return { messages: inputMessages, artifacts: artifactsList, effects };
    }

    let messages = inputMessages;
    let agentMessageIndex = messages.findLastIndex(m => !m.isUser && !m.userInputRequest && !m.authenticationLink && m.taskId === currentTaskIdFromResult);
    const existingArtifactIdx = agentMessageIndex >= 0 ? messages[agentMessageIndex].parts.findIndex(p => p.kind === "artifact" && (p as ArtifactPart).name === filename) : -1;

    if (status === "cancelled") {
        if (existingArtifactIdx >= 0) {
            const agentMsg = messages[agentMessageIndex];
            let parts: PartFE[] = agentMsg.parts.filter(p => !(p.kind === "artifact" && (p as ArtifactPart).name === filename));
            if (rolled_back_text) {
                parts = [...parts, { kind: "text", text: rolled_back_text }];
            }
            messages = [...messages];
            messages[agentMessageIndex] = { ...agentMsg, parts };
        }
        return { messages, artifacts: artifactsList, effects };
    }

    if (status !== "in-progress" && existingArtifactIdx === -1) {
        return { messages, artifacts: artifactsList, effects };
    }

    if (status === "in-progress" && agentMessageIndex === -1) {
        const newAgentMessage: MessageFE = {
            role: "agent",
            parts: [],
            taskId: currentTaskIdFromResult,
            isUser: false,
            isComplete: false,
            isStatusBubble: false,
            metadata: {
                messageId: uuid(),
                lastProcessedEventSequence: eventSequence,
            },
        };
        messages = [...messages, newAgentMessage];
        agentMessageIndex = messages.length - 1;
    }

    const agentMsg = messages[agentMessageIndex];
    const updatedParts: PartFE[] = [...agentMsg.parts];

    if (status === "in-progress") {
        if (existingArtifactIdx >= 0) {
            const existingPart = updatedParts[existingArtifactIdx] as ArtifactPart;
            updatedParts[existingArtifactIdx] = { ...existingPart, status: "in-progress", bytesTransferred: bytes_transferred, tags: tags ?? existingPart.tags };
        } else {
            const newPart: ArtifactPart = {
                kind: "artifact",
                status: "in-progress",
                name: filename,
                bytesTransferred: bytes_transferred,
            };
            if (description !== undefined) {
                newPart.description = description;
            }
            if (tags !== undefined) {
                newPart.tags = tags;
            }
            updatedParts.push(newPart);
        }
    } else if (status === "completed") {
        const fileAttachment: FileAttachment = {
            name: filename,
            mimeType,
            uri: version !== undefined ? `artifact://${sessionId}/${filename}?version=${version}` : `artifact://${sessionId}/${filename}`,
        };
        const existingPart = updatedParts[existingArtifactIdx] as ArtifactPart;
        const updatedPart: ArtifactPart = { ...existingPart, status: "completed", file: fileAttachment };
        delete updatedPart.bytesTransferred;
        applyTerminalTags(updatedPart, tags);
        updatedParts[existingArtifactIdx] = updatedPart;
    } else if (status === "failed") {
        const errorMsg = `Failed to create artifact: ${filename}`;
        const existingPart = updatedParts[existingArtifactIdx] as ArtifactPart;
        const updatedPart: ArtifactPart = { ...existingPart, status: "failed", error: errorMsg };
        delete updatedPart.bytesTransferred;
        applyTerminalTags(updatedPart, tags);
        updatedParts[existingArtifactIdx] = updatedPart;
    }

    messages = [...messages];
    messages[agentMessageIndex] = { ...agentMsg, parts: updatedParts, isStatusBubble: false };

    return { messages, artifacts: artifactsList, effects };
}

function applyTerminalTags(part: ArtifactPart, tags: string[] | undefined): void {
    if (tags) {
        part.tags = tags;
    } else {
        delete part.tags;
    }
}

function finalizeArtifactCardFromSaved(inputMessages: MessageFE[], data: ArtifactSavedPayload, currentTaskIdFromResult: string | undefined, sessionId: string): MessageFE[] {
    const { filename, version, mime_type: mimeType, tags } = data;
    const agentMessageIndex = inputMessages.findLastIndex(m => !m.isUser && m.taskId === currentTaskIdFromResult);
    if (agentMessageIndex === -1) {
        return inputMessages;
    }
    const agentMsg = inputMessages[agentMessageIndex];
    const existingArtifactIdx = agentMsg.parts.findIndex(p => p.kind === "artifact" && (p as ArtifactPart).name === filename && (p as ArtifactPart).status === "in-progress");
    if (existingArtifactIdx === -1) {
        return inputMessages;
    }

    const fileAttachment: FileAttachment = {
        name: filename,
        mimeType,
        uri: version !== undefined ? `artifact://${sessionId}/${filename}?version=${version}` : `artifact://${sessionId}/${filename}`,
    };
    const existingPart = agentMsg.parts[existingArtifactIdx] as ArtifactPart;
    const updatedPart: ArtifactPart = { ...existingPart, status: "completed", file: fileAttachment };
    delete updatedPart.bytesTransferred;
    applyTerminalTags(updatedPart, tags);

    const updatedParts: PartFE[] = [...agentMsg.parts];
    updatedParts[existingArtifactIdx] = updatedPart;
    const messages = [...inputMessages];
    messages[agentMessageIndex] = { ...agentMsg, parts: updatedParts };
    return messages;
}

const RAG_SEARCH_TYPE_WHEN_ABSENT: RAGSearchType = "deep_research";

function toRAGSearchType(wireValue: string | undefined): RAGSearchType {
    if (isRAGSearchType(wireValue)) return wireValue;
    if (wireValue !== undefined) {
        console.warn(`[processChatEvent] unrecognised rag_info_update search_type "${wireValue}"; rendering as ${RAG_SEARCH_TYPE_WHEN_ABSENT}`);
    }
    return RAG_SEARCH_TYPE_WHEN_ABSENT;
}

function processRagInfoUpdate(data: RagInfoUpdatePayload, prevRagData: RAGSearchResult[], currentTaskIdFromResult: string | undefined): RAGSearchResult[] {
    const ragData = [...prevRagData];
    const searchType = toRAGSearchType(data.search_type);
    const wireSearchTurn = data.search_turn;
    const searchTurn = wireSearchTurn ?? 0;

    const formattedSources = (data.sources || []).map((source, idx) => {
        const sourceMetadata = source.metadata ?? {};
        const url = source.url || source.sourceUrl || "";
        const sourceType = (sourceMetadata.source_type as string) || source.sourceType || source.source_type || "web";
        const favicon = (sourceMetadata.favicon as string) || source.favicon || (url ? `https://www.google.com/s2/favicons?domain=${url}&sz=32` : "");
        return {
            citationId: source.citationId || `s${searchTurn}r${idx}`,
            fileId: source.fileId,
            filename: source.filename,
            title: source.title || url,
            sourceUrl: source.sourceUrl || url,
            url,
            contentPreview: source.contentPreview || `Source: ${source.title || url}`,
            relevanceScore: source.relevanceScore ?? 1,
            retrievedAt: source.retrievedAt || data.timestamp,
            sourceType,
            metadata: {
                ...sourceMetadata,
                favicon,
                type: (sourceMetadata.type as string) || "web_search",
                source_type: sourceType,
            },
        };
    });

    const existingIndex = ragData.findIndex(r => r.searchType === searchType && r.taskId === currentTaskIdFromResult && r.query === data.query && r.searchTurn === wireSearchTurn);

    if (existingIndex !== -1) {
        const existing = ragData[existingIndex];
        ragData[existingIndex] = {
            ...existing,
            title: data.title || existing.title,
            sources: formattedSources,
            isComplete: existing.isComplete || data.is_complete,
        };
    } else {
        ragData.push({
            query: data.query,
            title: data.title,
            searchType,
            timestamp: data.timestamp,
            sources: formattedSources,
            taskId: currentTaskIdFromResult,
            isComplete: data.is_complete,
            searchTurn: wireSearchTurn,
        });
    }

    return ragData;
}

function processDeepResearchProgress(data: DeepResearchProgressPayload, deepResearchQueryHistory: Map<string, QueryHistoryEntry[]>, currentTaskIdFromResult: string | undefined): QueryHistoryEntry[] | null {
    if (!currentTaskIdFromResult) return null;

    const taskHistory = deepResearchQueryHistory.get(currentTaskIdFromResult) || [];

    if (data.current_query) {
        const existingQueryIndex = taskHistory.findIndex(q => q.query === data.current_query);
        if (existingQueryIndex === -1) {
            taskHistory.push({
                query: data.current_query,
                timestamp: new Date().toISOString(),
                urls: data.fetching_urls || [],
            });
        } else if (data.fetching_urls && data.fetching_urls.length > 0) {
            const existingEntry = taskHistory[existingQueryIndex];
            const existingUrls = new Set(existingEntry.urls.map(u => u.url));
            const newUrls = data.fetching_urls.filter(u => !existingUrls.has(u.url));
            if (newUrls.length > 0) {
                taskHistory[existingQueryIndex] = {
                    ...existingEntry,
                    urls: [...existingEntry.urls, ...newUrls],
                };
            }
        }
    } else if (data.fetching_urls && data.fetching_urls.length > 0 && taskHistory.length > 0) {
        const lastQueryIndex = taskHistory.length - 1;
        const lastEntry = taskHistory[lastQueryIndex];
        const existingUrls = new Set(lastEntry.urls.map(u => u.url));
        const newUrls = data.fetching_urls.filter(u => !existingUrls.has(u.url));
        if (newUrls.length > 0) {
            taskHistory[lastQueryIndex] = {
                ...lastEntry,
                urls: [...lastEntry.urls, ...newUrls],
            };
        }
    }

    deepResearchQueryHistory.set(currentTaskIdFromResult, taskHistory);

    return taskHistory;
}

function processToolResultRag(data: ToolResultPayload, prevRagData: RAGSearchResult[], currentTaskIdFromResult: string | undefined): RAGSearchResult[] | undefined {
    const resultData = data.result_data;
    if (!resultData || !resultData.rag_metadata) {
        return undefined;
    }

    const ragMetadata = resultData.rag_metadata;
    if (Array.isArray(ragMetadata) || typeof ragMetadata.searchType !== "string" || !Array.isArray(ragMetadata.sources)) {
        return undefined;
    }

    const ragSearchResult: RAGSearchResult = {
        query: ragMetadata.query,
        title: ragMetadata.title,
        searchType: ragMetadata.searchType,
        timestamp: ragMetadata.timestamp,
        sources: ragMetadata.sources,
        taskId: currentTaskIdFromResult,
        metadata: ragMetadata.metadata,
    };

    if (ragMetadata.searchType === "deep_research") {
        const filtered = prevRagData.filter(r => !(r.searchType === "deep_research" && r.taskId === currentTaskIdFromResult));
        return [...filtered, ragSearchResult];
    } else {
        return [...prevRagData, ragSearchResult];
    }
}

function appendBudgetStopNotice(messages: MessageFE[], parts: Part[], taskId: string | undefined, metadata: MessageFE["metadata"]): void {
    const lastMessage = messages.at(-1);
    if (lastMessage && !lastMessage.isUser && lastMessage.taskId === taskId && !lastMessage.isComplete) {
        messages[messages.length - 1] = { ...lastMessage, isComplete: true };
    }
    const noticeParts: PartFE[] = checkHasVisibleContent(parts) ? (parts as PartFE[]) : [{ kind: "text", text: BUDGET_STOP_FALLBACK_TEXT }];
    messages.push({ role: "agent", parts: noticeParts, taskId, isUser: false, isComplete: true, isError: true, isWarning: true, metadata: { ...metadata, messageId: `msg-${uuid()}` } });
}

function applyContentToMessages(
    inputMessages: MessageFE[],
    newContentParts: Part[],
    options: {
        hasNewFiles: boolean;
        isFinalEvent: boolean;
        isTaskFailed: boolean;
        isBudgetStop: boolean;
        taskId: string | undefined;
        eventSequence: number;
        messageId: string;
        contextId: string | undefined;
        fallbackSessionId: string;
    }
): MessageFE[] {
    const { hasNewFiles, isFinalEvent, isTaskFailed, isBudgetStop, taskId, eventSequence, messageId, contextId, fallbackSessionId } = options;
    const messages = [...inputMessages];
    let lastMessage = messages[messages.length - 1];

    if (lastMessage?.isStatusBubble) {
        messages.pop();
        lastMessage = messages[messages.length - 1];
    }

    const isProgressUpdate = newContentParts.length === 1 && newContentParts[0].kind === "data" && (newContentParts[0] as DataPart).data && (newContentParts[0] as DataPart).data?.type === "deep_research_progress";

    if (isProgressUpdate && lastMessage && !lastMessage.isUser && !lastMessage.isWarning && !lastMessage.userInputRequest && !lastMessage.authenticationLink && lastMessage.taskId === taskId) {
        messages[messages.length - 1] = {
            ...lastMessage,
            parts: newContentParts as PartFE[],
            isComplete: isFinalEvent || hasNewFiles,
            metadata: {
                ...lastMessage.metadata,
                lastProcessedEventSequence: eventSequence,
            },
        };
    } else if (isCompactionNotificationBubble(lastMessage, taskId ?? "", newContentParts)) {
        messages.push({
            role: "agent",
            parts: newContentParts as PartFE[],
            taskId,
            isUser: false,
            isComplete: isFinalEvent,
            metadata: {
                messageId,
                sessionId: contextId ?? fallbackSessionId,
                lastProcessedEventSequence: eventSequence,
            },
        });
    } else if (isBudgetStop) {
        appendBudgetStopNotice(messages, newContentParts, taskId, {
            messageId,
            sessionId: contextId ?? fallbackSessionId,
            lastProcessedEventSequence: eventSequence,
        });
    } else if (lastMessage && !lastMessage.isUser && !lastMessage.isWarning && !lastMessage.userInputRequest && !lastMessage.authenticationLink && lastMessage.taskId === taskId && newContentParts.length > 0) {
        messages[messages.length - 1] = {
            ...lastMessage,
            parts: [...lastMessage.parts, ...(newContentParts as PartFE[])],
            isComplete: isFinalEvent || hasNewFiles,
            isError: isTaskFailed || lastMessage.isError,
            metadata: {
                ...lastMessage.metadata,
                lastProcessedEventSequence: eventSequence,
            },
        };
    } else if (isTaskFailed || checkHasVisibleContent(newContentParts)) {
        messages.push({
            role: "agent",
            parts: newContentParts as PartFE[],
            taskId,
            isUser: false,
            isComplete: isFinalEvent || hasNewFiles,
            isError: isTaskFailed,
            metadata: {
                messageId,
                sessionId: contextId ?? fallbackSessionId,
                lastProcessedEventSequence: eventSequence,
            },
        });
    }

    return messages;
}
