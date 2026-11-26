// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any */
import type {
    A2AEventSSEPayload,
    AgentPerformanceMetrics,
    Artifact,
    ArtifactNotificationData,
    DataPart,
    DelegationInfo,
    FilePart,
    JSONRPCError,
    LLMCallData,
    LLMResponseToAgentData,
    Part,
    PerformanceReport,
    TaskFE,
    TaskState,
    TaskStatusUpdateEvent,
    TextPart,
    ToolCallPerformance,
    ToolDecision,
    ToolDecisionData,
    ToolInvocationStartData,
    ToolResultData,
    VisualizedTask,
    VisualizerStep,
    VisualizerStepType,
} from "@/types";

import { isPlaceholderToolResult } from "./flowChart/utils/resultStatus";

const isIntermediateWebContentArtifact = (artifactName: string | undefined): boolean => {
    if (!artifactName) return false;
    return artifactName.startsWith("web_content_");
};

type ArtifactProgressData = { filename: string; status?: string; bytes_transferred?: number };

const artifactProgressStatusText = (data: ArtifactProgressData): string => {
    const name = data.filename;
    switch (data.status) {
        case "completed":
            return `Saved artifact: ${name}`;
        case "failed":
            return `Failed to save artifact: ${name}`;
        case "cancelled":
            return `Cancelled artifact: ${name}`;
        default:
            return `Saving artifact: ${name} (${data.bytes_transferred ?? 0} bytes)`;
    }
};

const getParentTaskIdFromTaskObject = (task: TaskFE | null | undefined): string | null | undefined => {
    if (!task) return undefined;
    if (task.events && task.events.length > 0) {
        const firstEvent = task.events[0];
        if (firstEvent.full_payload?.params?.message?.metadata?.parentTaskId) {
            return firstEvent.full_payload.params.message.metadata.parentTaskId;
        }
    }
    return undefined;
};

const getEventTimestamp = (event: A2AEventSSEPayload): string => {
    return event.full_payload?.result?.status?.timestamp || event.timestamp;
};

const isoJustBefore = (ts: string | undefined): string => {
    const ms = ts ? new Date(ts).getTime() : Number.NaN;
    return new Date((Number.isNaN(ms) ? 0 : ms) - 1).toISOString();
};

const collectAllDescendantEvents = (currentTaskId: string, allMonitoredTasks: Record<string, TaskFE>, taskNestingLevels: Map<string, number>, currentLevel: number, visitedTaskIds: Set<string> = new Set()): A2AEventSSEPayload[] => {
    if (visitedTaskIds.has(currentTaskId)) {
        return [];
    }
    visitedTaskIds.add(currentTaskId);

    const task = allMonitoredTasks[currentTaskId];
    if (!task) {
        console.warn(`[collectAllDescendantEvents] Task not found in allMonitoredTasks: ${currentTaskId}`);
        return [];
    }

    taskNestingLevels.set(currentTaskId, currentLevel);
    let events: A2AEventSSEPayload[] = [...(task.events || [])];

    for (const taskIdInStore in allMonitoredTasks) {
        const potentialChildTask = allMonitoredTasks[taskIdInStore];
        if (potentialChildTask.taskId === currentTaskId) continue;

        const childsParentId = getParentTaskIdFromTaskObject(potentialChildTask);

        if (childsParentId === currentTaskId) {
            events = events.concat(collectAllDescendantEvents(potentialChildTask.taskId, allMonitoredTasks, taskNestingLevels, currentLevel + 1, visitedTaskIds));
        }
    }
    return events;
};

const deforwardEvent = (ev: A2AEventSSEPayload, subTaskId: string): A2AEventSSEPayload => {
    const cloned = structuredClone(ev);
    cloned.task_id = subTaskId;
    const result = cloned.full_payload?.result;
    if (result) {
        result.taskId = subTaskId;
        const meta = result.status?.message?.metadata;
        if (meta) {
            delete meta.forwarded_from_peer;
        }
    }
    return cloned;
};

interface ForwardedSubTaskGroup {
    parentTaskId: string;
    functionCallId?: string;
    events: A2AEventSSEPayload[];
}

const taskHasRequestEvent = (task: TaskFE | undefined): boolean => !!task?.events?.some(e => e.direction === "request");

const groupForwardedEventsBySubTask = (allMonitoredTasks: Record<string, TaskFE>): Map<string, ForwardedSubTaskGroup> => {
    const groups = new Map<string, ForwardedSubTaskGroup>();

    for (const tid in allMonitoredTasks) {
        for (const ev of allMonitoredTasks[tid].events ?? []) {
            const meta = ev.full_payload?.result?.status?.message?.metadata as Record<string, any> | undefined;
            if (!meta?.forwarded_from_peer) continue;

            const chain = Array.isArray(meta.peer_task_chain) ? (meta.peer_task_chain as string[]) : undefined;
            const subTaskId = (chain?.[0] ?? meta.original_peer_event_taskId) as string | undefined;
            if (!subTaskId) {
                console.warn("[synthesizeMonitoredSubTasks] forwarded event missing peer_task_chain/original_peer_event_taskId; cannot reconstruct sub-task", { parentTaskId: ev.task_id ?? tid });
                continue;
            }
            if (taskHasRequestEvent(allMonitoredTasks[subTaskId])) continue;

            const parentTaskId = chain && chain.length > 1 ? chain[1] : (ev.task_id ?? tid);
            const group = groups.get(subTaskId) ?? { parentTaskId, functionCallId: meta.function_call_id as string | undefined, events: [] };
            group.events.push(deforwardEvent(ev, subTaskId));
            groups.set(subTaskId, group);
        }
    }
    return groups;
};

const buildReconstructedSubTask = (subTaskId: string, group: ForwardedSubTaskGroup, existing: TaskFE | undefined): TaskFE => {
    const sortedEvents = group.events.toSorted((a, b) => new Date(getEventTimestamp(a)).getTime() - new Date(getEventTimestamp(b)).getTime());
    const firstTs = existing?.events?.[0]?.timestamp ?? sortedEvents[0]?.timestamp ?? new Date(0).toISOString();
    const requestEvent: A2AEventSSEPayload = {
        task_id: subTaskId,
        direction: "request",
        timestamp: isoJustBefore(firstTs),
        source_entity: "delegation",
        target_entity: subTaskId,
        event_type: "a2a_message",
        solace_topic: `synthetic/forwarded-subtask/${subTaskId}`,
        payload_summary: { method: "message/send" },
        full_payload: {
            method: "message/send",
            params: { message: { metadata: { parentTaskId: group.parentTaskId, function_call_id: group.functionCallId } } },
        },
    };
    if (existing) {
        return { ...existing, events: [requestEvent, ...(existing.events ?? [])] };
    }
    return {
        taskId: subTaskId,
        initialRequestText: "",
        events: [requestEvent, ...sortedEvents],
        firstSeen: new Date(firstTs),
        lastUpdated: new Date(sortedEvents.at(-1)?.timestamp ?? firstTs),
    };
};

const synthesizeMonitoredSubTasksFromForwardedEvents = (allMonitoredTasks: Record<string, TaskFE>): Record<string, TaskFE> => {
    const groups = groupForwardedEventsBySubTask(allMonitoredTasks);
    if (groups.size === 0) return allMonitoredTasks;

    const augmented = { ...allMonitoredTasks };
    for (const [subTaskId, group] of groups) {
        augmented[subTaskId] = buildReconstructedSubTask(subTaskId, group, augmented[subTaskId]);
    }
    return augmented;
};

const summarizeFunctionResponse = (response: unknown): string => {
    let summary: string;
    if (typeof response === "string") {
        summary = response.substring(0, 200);
    } else if (typeof response === "object" && response !== null) {
        summary = JSON.stringify(response).substring(0, 200);
    } else {
        summary = String(response).substring(0, 200);
    }
    return summary.length === 200 ? summary + "..." : summary;
};

const TURN_TIME_STAMP = /^\[Current time: (?:[A-Z][a-z]+day, )?\d{4}-\d{2}-\d{2}T[^\]\s]+(?: \([^)\n]+\))?\]\n/;

const stripTurnTimeStamp = (text: string): string => text.replace(TURN_TIME_STAMP, "");

const buildLlmInvocationPromptText = (llmData: any): string => {
    const contents = llmData?.contents;
    if (!Array.isArray(contents) || contents.length === 0) {
        return "System-initiated LLM call";
    }

    const toolResultSummaries: string[] = [];
    for (const content of contents) {
        if (!Array.isArray(content?.parts)) continue;
        for (const part of content.parts) {
            if (!part.function_response) continue;
            const toolName = part.function_response.name || "unknown";
            toolResultSummaries.push(`- ${toolName}: ${summarizeFunctionResponse(part.function_response.response)}`);
        }
    }

    const userPromptFull = contents
        .filter((c: any) => c.role === "user")
        .flatMap((c: any) => (Array.isArray(c.parts) ? c.parts : []))
        .map((p: any) => stripTurnTimeStamp(p.text || ""))
        .join("\n")
        .trim();

    if (toolResultSummaries.length === 0) {
        return userPromptFull || "System-initiated LLM call";
    }

    let requestLine = "";
    if (userPromptFull) {
        const snippet = userPromptFull.substring(0, 5000);
        const ellipsis = userPromptFull.length > 5000 ? "..." : "";
        requestLine = `\nRequest: ${snippet}${ellipsis}`;
    }
    return `[Following ${toolResultSummaries.length} tool result(s)]${requestLine}\n\nTool Results:\n${toolResultSummaries.join("\n")}`;
};

const toolCallSignalTypes = new Set(["tool_invocation_start", "tool_result"]);

const nodeTypeOf = (metadata: unknown) => (metadata as { node_type?: unknown } | undefined)?.node_type;

const isWorkflowToolNodeTelemetryEvent = (event: A2AEventSSEPayload): boolean => {
    const result = event.direction === "status-update" ? (event.full_payload?.result as TaskStatusUpdateEvent | undefined) : undefined;
    const message = result?.status?.message;
    if (!message?.parts?.length || (nodeTypeOf(result?.metadata) !== "tool" && nodeTypeOf(message.metadata) !== "tool")) {
        return false;
    }
    return message.parts.every(part => part.kind === "data" && toolCallSignalTypes.has((part.data as { type?: string } | undefined)?.type ?? ""));
};

const withoutWorkflowToolNodeTelemetry = (events: A2AEventSSEPayload[]): A2AEventSSEPayload[] => events.filter(event => !isWorkflowToolNodeTelemetryEvent(event));

interface WorkflowToolCall {
    toolArguments?: Record<string, any>;
    resultData?: unknown;
    hasResult: boolean;
}

const collectWorkflowToolCalls = (events: A2AEventSSEPayload[]): Map<string, WorkflowToolCall> => {
    const calls = new Map<string, WorkflowToolCall>();
    for (const event of events) {
        if (!isWorkflowToolNodeTelemetryEvent(event)) continue;
        const parts = (event.full_payload?.result as TaskStatusUpdateEvent | undefined)?.status?.message?.parts ?? [];
        for (const part of parts) {
            const data = (part as { data?: { type?: string; function_call_id?: string; tool_args?: Record<string, any>; result_data?: unknown } }).data;
            if (!data?.function_call_id) continue;
            const call = calls.get(data.function_call_id) ?? { hasResult: false };
            if (data.type === "tool_invocation_start") {
                call.toolArguments = data.tool_args;
            } else if (data.type === "tool_result") {
                call.resultData = data.result_data;
                call.hasResult = true;
            }
            calls.set(data.function_call_id, call);
        }
    }
    return calls;
};

export const attachWorkflowToolCalls = (steps: VisualizerStep[], calls: Map<string, WorkflowToolCall>): void => {
    if (calls.size === 0) return;
    const callIdByNode = new Map<string, string>();
    for (const step of steps) {
        attachToolArguments(step, calls, callIdByNode);
        attachToolResult(step, calls, callIdByNode);
    }
};

const attachToolArguments = (step: VisualizerStep, calls: Map<string, WorkflowToolCall>, callIdByNode: Map<string, string>): void => {
    const start = step.data.workflowNodeExecutionStart;
    if (step.type !== "WORKFLOW_NODE_EXECUTION_START" || start?.nodeType !== "tool" || !start.subTaskId) return;
    const call = calls.get(start.subTaskId);
    if (call?.toolArguments !== undefined) start.toolArguments = call.toolArguments;
    callIdByNode.set(`${step.owningTaskId}:${start.nodeId}`, start.subTaskId);
};

const attachToolResult = (step: VisualizerStep, calls: Map<string, WorkflowToolCall>, callIdByNode: Map<string, string>): void => {
    const result = step.data.workflowNodeExecutionResult;
    if (step.type !== "WORKFLOW_NODE_EXECUTION_RESULT" || !result) return;
    const callId = callIdByNode.get(`${step.owningTaskId}:${result.nodeId}`);
    const call = callId ? calls.get(callId) : undefined;
    if (call?.hasResult) result.toolResultData = call.resultData;
};

const buildSyntheticRequestEvent = (parentTaskObject: TaskFE): A2AEventSSEPayload | null => {
    if (!parentTaskObject.events || parentTaskObject.events.length === 0) {
        return null;
    }

    const firstEventTimestamp = parentTaskObject.events[0]?.timestamp || parentTaskObject.firstSeen.toISOString();
    const syntheticTimestamp = isoJustBefore(firstEventTimestamp);

    let targetAgent = "Orchestrator";
    const firstEvent = parentTaskObject.events[0];
    if (firstEvent?.source_entity) {
        targetAgent = firstEvent.source_entity;
    } else if (firstEvent?.full_payload?.result?.metadata?.agent_name) {
        targetAgent = firstEvent.full_payload.result.metadata.agent_name;
    }

    return {
        task_id: parentTaskObject.taskId,
        direction: "request",
        timestamp: syntheticTimestamp,
        source_entity: "User",
        target_entity: targetAgent,
        event_type: "a2a_message",
        solace_topic: `synthetic/request/${parentTaskObject.taskId}`,
        payload_summary: {
            method: "message/send",
        },
        full_payload: {
            method: "message/send",
            params: {
                message: {
                    parts: [
                        {
                            kind: "text",
                            text: parentTaskObject.initialRequestText ?? "",
                        },
                    ],
                },
            },
        },
    };
};

const extractWorkflowAgentRequestInput = (
    params: any
): {
    inputText?: string;
    instruction?: string;
    inputArtifactRef?: { name: string; version?: number; uri?: string; mimeType?: string };
    inputSchema?: Record<string, any>;
    outputSchema?: Record<string, any>;
    suggestedOutputFilename?: string;
} => {
    if (!params?.message?.parts) {
        return {};
    }

    let inputSchema: Record<string, any> | undefined;
    let outputSchema: Record<string, any> | undefined;
    let suggestedOutputFilename: string | undefined;
    const structuredInvocationPart = params.message.parts.find((p: any) => p.kind === "data" && p.data?.type === "structured_invocation_request");
    if (structuredInvocationPart) {
        const invocationData = structuredInvocationPart.data;
        inputSchema = invocationData.input_schema;
        outputSchema = invocationData.output_schema;
        suggestedOutputFilename = invocationData.suggested_output_filename;
    }

    const textParts = params.message.parts.filter((p: any) => p.kind === "text" && p.text && !p.text.includes("REMINDER:"));
    const instruction: string | undefined = textParts.length > 0 ? textParts[0].text : undefined;

    let inputArtifactRef: { name: string; version?: number; uri?: string; mimeType?: string } | undefined;
    const fileParts = params.message.parts.filter((p: any) => p.kind === "file" && p.file);
    if (fileParts.length > 0) {
        const file = fileParts[0].file;
        inputArtifactRef = {
            name: file.name || "input",
            version: file.version,
            uri: file.uri,
            mimeType: file.mimeType,
        };
    }

    let inputText: string | undefined;
    if (!inputArtifactRef && textParts.length > 1) {
        inputText = textParts[1].text;
    } else if (!inputArtifactRef && textParts.length === 1 && !instruction) {
        inputText = textParts[0].text;
    }

    return { inputText, instruction, inputArtifactRef, inputSchema, outputSchema, suggestedOutputFilename };
};

const buildWorkflowAgentRequestStep = (args: {
    event: A2AEventSSEPayload;
    payload: any;
    eventId: string;
    eventTimestamp: string;
    eventAgentName: string;
    workflowName: string;
    nodeId: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    parentTaskId: string | null | undefined;
    stepIndex: number;
}): VisualizerStep => {
    const { event, payload, eventId, eventTimestamp, eventAgentName, workflowName, nodeId, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, stepIndex } = args;
    const input = extractWorkflowAgentRequestInput(payload.params);

    return {
        id: `vstep-wfagentreq-${stepIndex}-${eventId}`,
        type: "WORKFLOW_AGENT_REQUEST" as const,
        timestamp: eventTimestamp,
        title: `Workflow Request to ${event.target_entity || eventAgentName}`,
        source: "Workflow",
        target: event.target_entity || eventAgentName,
        data: {
            workflowAgentRequest: {
                agentName: event.target_entity || eventAgentName || "Unknown",
                nodeId,
                workflowName,
                ...input,
            },
        },
        rawEventIds: [eventId],
        isSubTaskStep: true,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        parentTaskId: parentTaskId ?? undefined,
    };
};

const extractUserRequestText = (params: any): string => {
    if (!params?.message?.parts) {
        return "User request";
    }

    const textParts = params.message.parts.filter((p: any) => p.kind === "text" && p.text);
    const isGatewayTimestamp = (text: string) => text.trim().startsWith("Request received by gateway at:");
    const filteredParts = textParts.filter((p: any) => !isGatewayTimestamp(p.text));

    if (filteredParts.length > 0) {
        return filteredParts.map((p: any) => p.text).join("\n");
    }
    if (textParts.length > 0) {
        const lastPart = textParts[textParts.length - 1].text;
        const nonGatewayLines = lastPart.split("\n").filter((line: string) => !isGatewayTimestamp(line));
        return nonGatewayLines.length > 0 ? nonGatewayLines.join("\n") : lastPart;
    }
    return "User request";
};

interface AggregationState {
    text: string;
    sourceAgent?: string;
    timestamp?: string;
    rawEventIds: string[];
    isForwardedContext?: boolean;
    lastFlushedText: string | null;
}

interface InProgressToolCall {
    timestamp: string;
    toolName: string;
    isPeer: boolean;
    invokingAgentInstanceId: string;
    subTaskId?: string;
    parallelBlockId?: string;
}

interface PendingArtifact {
    filename: string;
    version?: number;
    description?: string;
    mimeType?: string;
    timestamp: string;
    agentName: string;
    taskId: string;
    nestingLevel: number;
    eventId: string;
}

interface ProcessingContext {
    allMonitoredTasks: Record<string, TaskFE>;
    parentTaskObject: TaskFE;
    taskNestingLevels: Map<string, number>;
    visualizerSteps: VisualizerStep[];
    report: PerformanceReport;
    inProgressLlmCalls: Map<string, { timestamp: string; modelName: string }>;
    inProgressToolCalls: Map<string, InProgressToolCall>;
    pendingArtifacts: Map<string, PendingArtifact>;
    subTaskToFunctionCallIdMap: Map<string, string>;
    functionCallIdToDelegationInfoMap: Map<string, DelegationInfo>;
    processedWorkflowEvents: Set<string>;
    aggregation: AggregationState;
    statusText: { current?: string };
}

const ensureAgentMetrics = (report: PerformanceReport, instanceId: string, agentName: string): AgentPerformanceMetrics => {
    if (!report.agents[instanceId]) {
        report.agents[instanceId] = {
            agentName,
            instanceId,
            displayName: agentName,
            llmCalls: [],
            toolCalls: [],
            totalLlmTimeMs: 0,
            totalToolTimeMs: 0,
        };
    }
    return report.agents[instanceId];
};

const resolveFlushOwningTaskId = (args: { aggregation: AggregationState; currentEventOwningTaskId: string | undefined; rootTaskId: string }): string => {
    const { aggregation, currentEventOwningTaskId, rootTaskId } = args;
    if (aggregation.rawEventIds.length > 0) {
        const parts = aggregation.rawEventIds[0].split("-");
        if (parts.length > 2 && parts[1] !== "global") {
            return parts.slice(1, -1).join("-");
        }
        return rootTaskId;
    }
    return currentEventOwningTaskId ?? rootTaskId;
};

const flushAggregatedTextStep = (ctx: ProcessingContext, currentEventOwningTaskId?: string): void => {
    const aggregation = ctx.aggregation;
    if (aggregation.text.trim() && aggregation.sourceAgent && aggregation.timestamp) {
        const textToFlush = aggregation.text.trim();
        const owningTaskIdForFlush = resolveFlushOwningTaskId({ aggregation, currentEventOwningTaskId, rootTaskId: ctx.parentTaskObject.taskId });
        const nestingLevelForFlush = ctx.taskNestingLevels.get(owningTaskIdForFlush) ?? 0;
        const functionCallIdForStep = ctx.subTaskToFunctionCallIdMap.get(owningTaskIdForFlush);
        const parentTaskIdForFlush = getParentTaskIdFromTaskObject(ctx.allMonitoredTasks[owningTaskIdForFlush]);

        ctx.visualizerSteps.push({
            id: `vstep-agenttext-${ctx.visualizerSteps.length}-${aggregation.rawEventIds[0] || "unknown"}`,
            type: "AGENT_RESPONSE_TEXT",
            timestamp: aggregation.timestamp,
            title: `${aggregation.sourceAgent}: Response`,
            source: aggregation.sourceAgent,
            target: "User",
            data: { text: textToFlush },
            rawEventIds: [...aggregation.rawEventIds],
            isSubTaskStep: nestingLevelForFlush > 0,
            nestingLevel: nestingLevelForFlush,
            owningTaskId: owningTaskIdForFlush,
            parentTaskId: parentTaskIdForFlush ?? undefined,
            functionCallId: functionCallIdForStep,
        });
        aggregation.lastFlushedText = textToFlush;
    }
    aggregation.text = "";
    aggregation.sourceAgent = undefined;
    aggregation.timestamp = undefined;
    aggregation.rawEventIds = [];
    aggregation.isForwardedContext = undefined;
};

const resolveEventAgentName = (event: A2AEventSSEPayload, payload: any): string => {
    if (payload?.params?.message?.metadata?.agent_name) return payload.params.message.metadata.agent_name;
    if (payload?.result?.metadata?.agent_name) return payload.result.metadata.agent_name;
    if (payload?.result?.status?.message?.metadata?.agent_name) return payload.result.status.message.metadata.agent_name;
    if (payload?.result?.artifact?.metadata?.agent_name) return payload.result.artifact.metadata.agent_name;
    return event.source_entity || "UnknownAgent";
};

const handleSubTaskRequestEvent = (args: {
    event: A2AEventSSEPayload;
    payload: any;
    eventId: string;
    eventTimestamp: string;
    eventAgentName: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    parentTaskId: string | null | undefined;
    ctx: ProcessingContext;
}): boolean => {
    const { event, payload, eventId, eventTimestamp, eventAgentName, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, ctx } = args;
    if (event.direction !== "request" || currentEventNestingLevel === 0) {
        return false;
    }

    const paramsMetadata = payload.params?.metadata;
    const messageMetadata = payload.params?.message?.metadata;
    const functionCallId = paramsMetadata?.function_call_id || messageMetadata?.function_call_id;
    const subTaskId = event.task_id;

    if (subTaskId && functionCallId) {
        ctx.subTaskToFunctionCallIdMap.set(subTaskId, functionCallId);
        return true;
    }

    const workflowName = messageMetadata?.workflow_name;
    const nodeId = messageMetadata?.node_id;
    if (workflowName && nodeId) {
        ctx.visualizerSteps.push(
            buildWorkflowAgentRequestStep({
                event,
                payload,
                eventId,
                eventTimestamp,
                eventAgentName,
                workflowName,
                nodeId,
                currentEventNestingLevel,
                currentEventOwningTaskId,
                parentTaskId,
                stepIndex: ctx.visualizerSteps.length,
            })
        );
        return true;
    }

    return false;
};

const handleUserRequestEvent = (args: { event: A2AEventSSEPayload; payload: any; eventId: string; eventTimestamp: string; eventAgentName: string; currentEventOwningTaskId: string; ctx: ProcessingContext }): void => {
    const { event, payload, eventId, eventTimestamp, eventAgentName, currentEventOwningTaskId, ctx } = args;
    flushAggregatedTextStep(ctx, currentEventOwningTaskId);
    ctx.aggregation.lastFlushedText = null;
    const userText = extractUserRequestText(payload.params);
    ctx.visualizerSteps.push({
        id: `vstep-userreq-${ctx.visualizerSteps.length}-${eventId}`,
        type: "USER_REQUEST",
        timestamp: eventTimestamp,
        title: "User Input",
        source: "User",
        target: event.target_entity || eventAgentName,
        data: { text: userText },
        rawEventIds: [eventId],
        isSubTaskStep: false,
        nestingLevel: 0,
        owningTaskId: currentEventOwningTaskId,
    });
};

const resolveStatusUpdateAgentName = (args: { result: TaskStatusUpdateEvent; messageMetadata: any; isForwardedMessage: boolean; fallbackSourceEntity: string }): string => {
    const { result, messageMetadata, isForwardedMessage, fallbackSourceEntity } = args;
    if (isForwardedMessage) {
        return messageMetadata?.forwarded_from_peer || result.metadata?.forwarded_from_peer;
    }
    if (result.metadata?.agent_name) {
        return result.metadata.agent_name as string;
    }
    if (messageMetadata?.agent_name) {
        return messageMetadata.agent_name as string;
    }
    return fallbackSourceEntity;
};

const updateLastStatusTextForPart = (signalData: any, statusText: { current?: string }): void => {
    if (signalData?.type === "agent_progress_update") {
        statusText.current = signalData.status_text;
    } else if (signalData?.type === "artifact_creation_progress") {
        statusText.current = artifactProgressStatusText(signalData);
    }
};

const appendAggregatedText = (args: { part: TextPart; eventId: string; statusUpdateAgentName: string; isForwardedMessage: boolean; eventTimestamp: string; currentEventOwningTaskId: string; ctx: ProcessingContext }): void => {
    const { part, eventId, statusUpdateAgentName, isForwardedMessage, eventTimestamp, currentEventOwningTaskId, ctx } = args;
    const aggregation = ctx.aggregation;
    if (aggregation.sourceAgent && aggregation.sourceAgent !== statusUpdateAgentName) {
        flushAggregatedTextStep(ctx, currentEventOwningTaskId);
        aggregation.lastFlushedText = null;
    }
    if (!aggregation.sourceAgent) {
        aggregation.sourceAgent = statusUpdateAgentName;
        aggregation.timestamp = eventTimestamp;
        aggregation.isForwardedContext = isForwardedMessage;
    }
    aggregation.text += part.text;
    aggregation.rawEventIds.push(eventId);
};

const peerAgentNameFromToolName = (toolName: string): string => {
    if (toolName.startsWith("peer_")) return toolName.substring(5);
    if (toolName.startsWith("workflow_")) return toolName.substring(9);
    return toolName;
};

const peerAgentNamePrefixOnly = (toolName: string): string | undefined => {
    if (toolName.startsWith("peer_")) return toolName.substring(5);
    if (toolName.startsWith("workflow_")) return toolName.substring(9);
    return undefined;
};

const isSubTaskCreatedAfterDecision = (subTaskCreationRequest: A2AEventSSEPayload, eventTimestamp: string): boolean => {
    const subTaskTimestamp = getEventTimestamp(subTaskCreationRequest);
    const eventTimestampMs = new Date(eventTimestamp).getTime();
    const subTaskTimestampMs = subTaskTimestamp ? new Date(subTaskTimestamp).getTime() : Number.NaN;
    return Number.isNaN(subTaskTimestampMs) || subTaskTimestampMs >= eventTimestampMs;
};

const findSubTaskDelegation = (args: { decision: ToolDecision; currentEventOwningTaskId: string; eventTimestamp: string; claimedSubTaskIds: Set<string>; ctx: ProcessingContext }): DelegationInfo | undefined => {
    const { decision, currentEventOwningTaskId, eventTimestamp, claimedSubTaskIds, ctx } = args;
    const peerAgentActualName = peerAgentNameFromToolName(decision.toolName);

    for (const stId in ctx.allMonitoredTasks) {
        const candSubTask = ctx.allMonitoredTasks[stId];
        if (claimedSubTaskIds.has(candSubTask.taskId)) continue;

        const candSubTaskParentId = getParentTaskIdFromTaskObject(candSubTask);
        if (candSubTaskParentId !== currentEventOwningTaskId || !candSubTask.events?.length) continue;

        const subTaskCreationRequest = candSubTask.events.find(e => {
            const fcId = e.full_payload?.params?.message?.metadata?.function_call_id;
            return e.direction === "request" && fcId === decision.functionCallId;
        });
        if (!subTaskCreationRequest || !isSubTaskCreatedAfterDecision(subTaskCreationRequest, eventTimestamp)) continue;

        return { functionCallId: decision.functionCallId, peerAgentName: peerAgentActualName, subTaskId: candSubTask.taskId };
    }

    return undefined;
};

const matchDelegationsForDecisions = (args: { decisions: ToolDecision[]; currentEventOwningTaskId: string; eventTimestamp: string; ctx: ProcessingContext }): DelegationInfo[] => {
    const { decisions, currentEventOwningTaskId, eventTimestamp, ctx } = args;
    const delegationInfos: DelegationInfo[] = [];
    const claimedSubTaskIds = new Set<string>();

    decisions.forEach(decision => {
        const delegationInfo = findSubTaskDelegation({ decision, currentEventOwningTaskId, eventTimestamp, claimedSubTaskIds, ctx });
        if (!delegationInfo) return;

        decision.isPeerDelegation = true;
        delegationInfos.push(delegationInfo);
        ctx.functionCallIdToDelegationInfoMap.set(decision.functionCallId, delegationInfo);
        if (delegationInfo.subTaskId) {
            ctx.subTaskToFunctionCallIdMap.set(delegationInfo.subTaskId, decision.functionCallId);
            claimedSubTaskIds.add(delegationInfo.subTaskId);
        }
    });

    return delegationInfos;
};

const startToolCallTimers = (args: { decisions: ToolDecision[]; invokingAgentInstanceId: string; eventTimestamp: string; parallelBlockId: string | undefined; ctx: ProcessingContext }): void => {
    const { decisions, invokingAgentInstanceId, eventTimestamp, parallelBlockId, ctx } = args;
    decisions.forEach(decision => {
        if (ctx.inProgressToolCalls.has(decision.functionCallId)) return;
        const subTaskId = decision.isPeerDelegation ? ctx.functionCallIdToDelegationInfoMap.get(decision.functionCallId)?.subTaskId : undefined;
        ctx.inProgressToolCalls.set(decision.functionCallId, {
            timestamp: eventTimestamp,
            toolName: decision.toolName,
            isPeer: decision.isPeerDelegation,
            invokingAgentInstanceId,
            subTaskId,
            parallelBlockId,
        });
    });
};

const recordLlmResponseDuration = (args: { agentInstanceId: string; eventTimestamp: string; statusUpdateAgentName: string; ctx: ProcessingContext }): void => {
    const { agentInstanceId, eventTimestamp, statusUpdateAgentName, ctx } = args;
    const openCallForPerf = ctx.inProgressLlmCalls.get(agentInstanceId);
    if (!openCallForPerf) return;

    const duration = new Date(eventTimestamp).getTime() - new Date(openCallForPerf.timestamp).getTime();
    const agentMetrics = ensureAgentMetrics(ctx.report, agentInstanceId, statusUpdateAgentName);
    agentMetrics.llmCalls.push({ modelName: openCallForPerf.modelName, durationMs: duration, timestamp: openCallForPerf.timestamp });
    ctx.inProgressLlmCalls.delete(agentInstanceId);
};

const pushLlmResponseToAgentStep = (args: {
    contentParts: any[] | undefined;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    llmResponseData: any;
    ctx: ProcessingContext;
}): void => {
    const { contentParts, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, llmResponseData, ctx } = args;
    const llmResponseText =
        contentParts
            ?.filter(p => p.text)
            .map(p => p.text)
            .join("\n") || "";
    const llmResponseToAgentData: LLMResponseToAgentData = {
        responsePreview: llmResponseText.substring(0, 200) + (llmResponseText.length > 200 ? "..." : ""),
        response: llmResponseText,
        isFinalResponse: llmResponseData?.partial === false,
    };
    ctx.visualizerSteps.push({
        id: `vstep-llmrespagent-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_LLM_RESPONSE_TO_AGENT",
        timestamp: eventTimestamp,
        title: `${statusUpdateAgentName}: LLM Response`,
        source: "LLM",
        target: statusUpdateAgentName,
        data: { llmResponseToAgent: llmResponseToAgentData },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const handleLlmToolDecision = (args: {
    functionCallParts: any[];
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    agentInstanceId: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { functionCallParts, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;

    flushAggregatedTextStep(ctx, currentEventOwningTaskId);
    ctx.aggregation.lastFlushedText = null;

    const decisions: ToolDecision[] = functionCallParts.map(p => ({
        functionCallId: p.function_call.id,
        toolName: p.function_call.name,
        toolArguments: p.function_call.args ?? {},
        isPeerDelegation: p.function_call.name?.startsWith("peer_") || p.function_call.name?.startsWith("workflow_"),
    }));
    const toolDecisionData: ToolDecisionData = { decisions, isParallel: decisions.length > 1 };

    const delegationInfos = matchDelegationsForDecisions({ decisions, currentEventOwningTaskId, eventTimestamp, ctx });

    const toolDecisionStep: VisualizerStep = {
        id: `vstep-tooldecision-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_LLM_RESPONSE_TOOL_DECISION",
        timestamp: eventTimestamp,
        title: `LLM: Tool Decision${toolDecisionData.isParallel ? " (Parallel)" : ""}`,
        source: "LLM",
        target: statusUpdateAgentName,
        data: { toolDecision: toolDecisionData },
        rawEventIds: [eventId],
        delegationInfo: delegationInfos.length > 0 ? delegationInfos : undefined,
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    };
    ctx.visualizerSteps.push(toolDecisionStep);

    const parallelBlockId = toolDecisionData.isParallel ? toolDecisionStep.id : undefined;
    ensureAgentMetrics(ctx.report, agentInstanceId, statusUpdateAgentName);
    startToolCallTimers({ decisions, invokingAgentInstanceId: agentInstanceId, eventTimestamp, parallelBlockId, ctx });
};

const handleLlmResponseSignal = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    agentInstanceId: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;

    recordLlmResponseDuration({ agentInstanceId, eventTimestamp, statusUpdateAgentName, ctx });

    const llmResponseData = signalData.data;
    const contentParts = llmResponseData?.content?.parts as any[] | undefined;
    const functionCallParts = contentParts?.filter(p => p.function_call);

    if (functionCallParts && functionCallParts.length > 0) {
        handleLlmToolDecision({ functionCallParts, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        return;
    }

    pushLlmResponseToAgentStep({ contentParts, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, llmResponseData, ctx });
};

const handleWorkflowExecutionStart = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    parentTaskId: string | null | undefined;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, functionCallIdForStep, ctx } = args;
    const dedupKey = `start:${signalData.execution_id}`;
    if (ctx.processedWorkflowEvents.has(dedupKey)) return;
    ctx.processedWorkflowEvents.add(dedupKey);

    ctx.visualizerSteps.push({
        id: `vstep-wfstart-${ctx.visualizerSteps.length}-${eventId}`,
        type: "WORKFLOW_EXECUTION_START",
        timestamp: eventTimestamp,
        title: `Workflow Started: ${signalData.workflow_name}`,
        source: "System",
        target: "Workflow",
        data: {
            workflowExecutionStart: {
                workflowName: signalData.workflow_name,
                executionId: signalData.execution_id,
                inputArtifactRef: signalData.input_artifact_ref,
                workflowInput: signalData.workflow_input,
            },
        },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        parentTaskId: parentTaskId ?? undefined,
        functionCallId: functionCallIdForStep,
    });
};

const handleWorkflowNodeExecutionStart = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    const dedupKey = `node_start:${currentEventOwningTaskId}:${signalData.sub_task_id || signalData.node_id}:${signalData.iteration_index ?? 0}`;
    if (ctx.processedWorkflowEvents.has(dedupKey)) return;
    ctx.processedWorkflowEvents.add(dedupKey);

    ctx.visualizerSteps.push({
        id: `vstep-wfnode-start-${ctx.visualizerSteps.length}-${eventId}`,
        type: "WORKFLOW_NODE_EXECUTION_START",
        timestamp: eventTimestamp,
        title: `Node Started: ${signalData.node_id} (${signalData.node_type})`,
        source: "Workflow",
        target: signalData.agent_name || signalData.node_id,
        data: {
            workflowNodeExecutionStart: {
                nodeId: signalData.node_id,
                nodeType: signalData.node_type,
                agentName: signalData.agent_name,
                inputArtifactRef: signalData.input_artifact_ref,
                iterationIndex: signalData.iteration_index,
                condition: signalData.condition,
                trueBranch: signalData.true_branch,
                falseBranch: signalData.false_branch,
                trueBranchLabel: signalData.true_branch_label,
                falseBranchLabel: signalData.false_branch_label,
                cases: signalData.cases,
                defaultBranch: signalData.default_branch,
                waitFor: signalData.wait_for,
                joinStrategy: signalData.join_strategy,
                joinN: signalData.join_n,
                maxIterations: signalData.max_iterations,
                loopDelay: signalData.loop_delay,
                subTaskId: signalData.sub_task_id,
                parentNodeId: signalData.parent_node_id,
                parallelGroupId: signalData.parallel_group_id,
                dependsOn: signalData.depends_on,
                toolName: signalData.tool_name,
            },
        },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const handleWorkflowNodeExecutionResult = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    const dedupKey = `node_result:${currentEventOwningTaskId}:${signalData.node_id}:${signalData.status}`;
    if (ctx.processedWorkflowEvents.has(dedupKey)) return;
    ctx.processedWorkflowEvents.add(dedupKey);

    ctx.visualizerSteps.push({
        id: `vstep-wfnode-result-${ctx.visualizerSteps.length}-${eventId}`,
        type: "WORKFLOW_NODE_EXECUTION_RESULT",
        timestamp: eventTimestamp,
        title: `Node Completed: ${signalData.node_id} (${signalData.status})`,
        source: signalData.node_id,
        target: "Workflow",
        data: {
            workflowNodeExecutionResult: {
                nodeId: signalData.node_id,
                status: signalData.status,
                outputArtifactRef: signalData.output_artifact_ref,
                errorMessage: signalData.error_message,
                metadata: signalData.metadata,
                conditionResult: signalData.metadata?.condition_result,
            },
        },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const handleWorkflowMapProgress = (args: { signalData: any; eventId: string; eventTimestamp: string; currentEventNestingLevel: number; currentEventOwningTaskId: string; functionCallIdForStep: string | undefined; ctx: ProcessingContext }): void => {
    const { signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    ctx.visualizerSteps.push({
        id: `vstep-wfmap-${ctx.visualizerSteps.length}-${eventId}`,
        type: "WORKFLOW_MAP_PROGRESS",
        timestamp: eventTimestamp,
        title: `Map Progress: ${signalData.node_id} (${signalData.completed_items}/${signalData.total_items})`,
        source: signalData.node_id,
        target: "Workflow",
        data: {
            workflowMapProgress: {
                nodeId: signalData.node_id,
                totalItems: signalData.total_items,
                completedItems: signalData.completed_items,
                status: signalData.status,
            },
        },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const handleWorkflowExecutionResult = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    const dedupKey = `result:${currentEventOwningTaskId}:${signalData.status}`;
    if (ctx.processedWorkflowEvents.has(dedupKey)) return;
    ctx.processedWorkflowEvents.add(dedupKey);

    ctx.visualizerSteps.push({
        id: `vstep-wfresult-${ctx.visualizerSteps.length}-${eventId}`,
        type: "WORKFLOW_EXECUTION_RESULT",
        timestamp: eventTimestamp,
        title: `Workflow Finished: ${signalData.status}`,
        source: "Workflow",
        target: "User",
        data: {
            workflowExecutionResult: {
                status: signalData.status,
                outputArtifactRef: signalData.output_artifact_ref,
                errorMessage: signalData.error_message,
                workflowOutput: signalData.workflow_output,
            },
        },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const handleAgentProgressUpdateSignal = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    ctx.visualizerSteps.push({
        id: `vstep-progress-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_RESPONSE_TEXT",
        timestamp: eventTimestamp,
        title: `${statusUpdateAgentName}: Progress Update`,
        source: statusUpdateAgentName,
        target: "User",
        data: { text: signalData.status_text },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const handleLlmInvocationSignal = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    agentInstanceId: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    const llmData = signalData.request;
    const llmCallData: LLMCallData = {
        modelName: llmData?.model || "Unknown Model",
        promptPreview: buildLlmInvocationPromptText(llmData) || "No text in user prompt.",
    };
    ensureAgentMetrics(ctx.report, agentInstanceId, statusUpdateAgentName);
    ctx.inProgressLlmCalls.set(agentInstanceId, { timestamp: eventTimestamp, modelName: llmCallData.modelName });
    ctx.visualizerSteps.push({
        id: `vstep-llmcall-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_LLM_CALL",
        timestamp: eventTimestamp,
        title: `${statusUpdateAgentName}: LLM Call`,
        source: statusUpdateAgentName,
        target: "LLM",
        data: { llmCall: llmCallData },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const handleToolInvocationStartSignal = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    const delegationInfo = ctx.functionCallIdToDelegationInfoMap.get(signalData.function_call_id);

    const invocationData: ToolInvocationStartData = {
        functionCallId: signalData.function_call_id,
        toolName: signalData.tool_name,
        toolArguments: signalData.tool_args,
        isPeerInvocation: signalData.tool_name?.startsWith("peer_") || signalData.tool_name?.startsWith("workflow_") || !!delegationInfo,
        parallelGroupId: signalData.parallel_group_id,
    };

    ctx.visualizerSteps.push({
        id: `vstep-toolinvokestart-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_TOOL_INVOCATION_START",
        timestamp: eventTimestamp,
        title: `${statusUpdateAgentName}: Executing tool ${invocationData.toolName}`,
        source: statusUpdateAgentName,
        target: invocationData.toolName,
        data: { toolInvocationStart: invocationData },
        rawEventIds: [eventId],
        delegationInfo: delegationInfo ? [delegationInfo] : undefined,
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
};

const recordToolCallDuration = (args: { functionCallId: string; eventTimestamp: string; ctx: ProcessingContext }): void => {
    const { functionCallId, eventTimestamp, ctx } = args;
    const openToolCallForPerf = ctx.inProgressToolCalls.get(functionCallId);
    ctx.inProgressToolCalls.delete(functionCallId);
    if (!openToolCallForPerf) return;

    const invokingAgentMetrics = ctx.report.agents[openToolCallForPerf.invokingAgentInstanceId];
    if (!invokingAgentMetrics) return;

    const duration = new Date(eventTimestamp).getTime() - new Date(openToolCallForPerf.timestamp).getTime();
    const peerAgentName = openToolCallForPerf.isPeer ? peerAgentNamePrefixOnly(openToolCallForPerf.toolName) : undefined;
    const toolCallPerf: ToolCallPerformance = {
        toolName: openToolCallForPerf.toolName,
        durationMs: duration,
        isPeer: openToolCallForPerf.isPeer,
        timestamp: openToolCallForPerf.timestamp,
        peerAgentName,
        subTaskId: openToolCallForPerf.subTaskId,
        parallelBlockId: openToolCallForPerf.parallelBlockId,
    };
    invokingAgentMetrics.toolCalls.push(toolCallPerf);
};

const flushPendingArtifactNotification = (args: { functionCallId: string; ctx: ProcessingContext }): void => {
    const { functionCallId, ctx } = args;
    const pendingArtifact = ctx.pendingArtifacts.get(functionCallId);
    ctx.pendingArtifacts.delete(functionCallId);
    if (!pendingArtifact || isIntermediateWebContentArtifact(pendingArtifact.filename)) return;

    const artifactNotification: ArtifactNotificationData = {
        artifactName: pendingArtifact.filename,
        version: pendingArtifact.version,
        description: pendingArtifact.description,
        mimeType: pendingArtifact.mimeType,
    };
    ctx.visualizerSteps.push({
        id: `vstep-artifactcreated-${ctx.visualizerSteps.length}-${pendingArtifact.eventId}`,
        type: "AGENT_ARTIFACT_NOTIFICATION",
        timestamp: pendingArtifact.timestamp,
        title: `${pendingArtifact.agentName}: Created Artifact - ${artifactNotification.artifactName}`,
        source: pendingArtifact.agentName,
        target: "User/System",
        data: { artifactNotification },
        rawEventIds: [pendingArtifact.eventId],
        isSubTaskStep: pendingArtifact.nestingLevel > 0,
        nestingLevel: pendingArtifact.nestingLevel,
        owningTaskId: pendingArtifact.taskId,
        functionCallId,
    });
};

const handleToolResultSignal = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    const functionCallId = signalData.function_call_id;

    if (!isPlaceholderToolResult(signalData.result_data)) {
        recordToolCallDuration({ functionCallId, eventTimestamp, ctx });
    }

    const toolResultData: ToolResultData = {
        toolName: signalData.tool_name,
        functionCallId,
        resultData: signalData.result_data,
        isPeerResponse: signalData.tool_name?.startsWith("peer_") || signalData.tool_name?.startsWith("workflow_"),
    };
    ctx.visualizerSteps.push({
        id: `vstep-toolresult-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_TOOL_EXECUTION_RESULT",
        timestamp: eventTimestamp,
        title: `${statusUpdateAgentName}: Tool Result - ${toolResultData.toolName}`,
        source: toolResultData.toolName,
        target: statusUpdateAgentName,
        data: { toolResult: toolResultData },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });

    if (signalData.tool_name === "_notify_artifact_save" && functionCallId) {
        flushPendingArtifactNotification({ functionCallId, ctx });
    }
};

const handleArtifactSavedSignal = (args: {
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    flushAggregatedTextStep(ctx, currentEventOwningTaskId);

    const artifactFilename = signalData.filename || "Unnamed Artifact";
    if (isIntermediateWebContentArtifact(artifactFilename)) return;

    if (signalData.function_call_id?.startsWith("host-notify-")) {
        ctx.pendingArtifacts.set(signalData.function_call_id, {
            filename: artifactFilename,
            version: signalData.version,
            description: signalData.description,
            mimeType: signalData.mimeType,
            timestamp: eventTimestamp,
            agentName: statusUpdateAgentName,
            taskId: currentEventOwningTaskId,
            nestingLevel: currentEventNestingLevel,
            eventId,
        });
        return;
    }

    const artifactNotification: ArtifactNotificationData = {
        artifactName: artifactFilename,
        version: signalData.version,
        description: signalData.description,
        mimeType: signalData.mimeType,
    };
    ctx.visualizerSteps.push({
        id: `vstep-artifactsaved-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_ARTIFACT_NOTIFICATION",
        timestamp: eventTimestamp,
        title: `${statusUpdateAgentName}: Created Artifact - ${artifactNotification.artifactName}`,
        source: statusUpdateAgentName,
        target: "User/System",
        data: { artifactNotification },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: signalData.function_call_id || functionCallIdForStep,
    });
};

const dispatchStatusSignal = (args: {
    signalType: string | undefined;
    signalData: any;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    agentInstanceId: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    parentTaskId: string | null | undefined;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { signalType, signalData, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, functionCallIdForStep, ctx } = args;
    switch (signalType) {
        case "workflow_execution_start":
            return handleWorkflowExecutionStart({ signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, functionCallIdForStep, ctx });
        case "workflow_node_execution_start":
            return handleWorkflowNodeExecutionStart({ signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "workflow_node_execution_result":
            return handleWorkflowNodeExecutionResult({ signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "workflow_map_progress":
            return handleWorkflowMapProgress({ signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "workflow_execution_result":
            return handleWorkflowExecutionResult({ signalData, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "agent_progress_update":
            return handleAgentProgressUpdateSignal({ signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "llm_invocation":
            return handleLlmInvocationSignal({ signalData, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "llm_response":
            return handleLlmResponseSignal({ signalData, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "tool_invocation_start":
            return handleToolInvocationStartSignal({ signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "tool_result":
            return handleToolResultSignal({ signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        case "artifact_saved":
            return handleArtifactSavedSignal({ signalData, eventId, eventTimestamp, statusUpdateAgentName, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx });
        default:
            return;
    }
};

const processStatusMessagePart = (args: {
    part: Part;
    eventId: string;
    eventTimestamp: string;
    statusUpdateAgentName: string;
    agentInstanceId: string;
    isForwardedMessage: boolean;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    parentTaskId: string | null | undefined;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): void => {
    const { part, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, isForwardedMessage, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, functionCallIdForStep, ctx } = args;

    if (part.kind === "data") {
        const signalData = (part as DataPart).data as any;
        updateLastStatusTextForPart(signalData, ctx.statusText);
        flushAggregatedTextStep(ctx, currentEventOwningTaskId);
        dispatchStatusSignal({
            signalType: signalData?.type as string,
            signalData,
            eventId,
            eventTimestamp,
            statusUpdateAgentName,
            agentInstanceId,
            currentEventNestingLevel,
            currentEventOwningTaskId,
            parentTaskId,
            functionCallIdForStep,
            ctx,
        });
        return;
    }

    if (part.kind === "text" && part.text) {
        appendAggregatedText({ part: part as TextPart, eventId, statusUpdateAgentName, isForwardedMessage, eventTimestamp, currentEventOwningTaskId, ctx });
    }
};

const handleStatusUpdateEvent = (args: {
    event: A2AEventSSEPayload;
    payload: any;
    eventId: string;
    eventTimestamp: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    parentTaskId: string | null | undefined;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): boolean => {
    const { event, payload, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, functionCallIdForStep, ctx } = args;
    if (event.direction !== "status-update" || !payload?.result) {
        return false;
    }

    const result = payload.result as TaskStatusUpdateEvent;
    const statusMessage = result.status?.message;
    const messageMetadata = statusMessage?.metadata as any;
    const isForwardedMessage = !!messageMetadata?.forwarded_from_peer || !!result.metadata?.forwarded_from_peer;
    const statusUpdateAgentName = resolveStatusUpdateAgentName({ result, messageMetadata, isForwardedMessage, fallbackSourceEntity: event.source_entity || "Agent" });
    const agentInstanceId = `${statusUpdateAgentName}:${currentEventOwningTaskId}`;

    if (!isForwardedMessage && statusMessage?.parts) {
        for (const part of statusMessage.parts) {
            processStatusMessagePart({ part, eventId, eventTimestamp, statusUpdateAgentName, agentInstanceId, isForwardedMessage, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, functionCallIdForStep, ctx });
        }
    }

    return true;
};

const extractArtifactMimeType = (artifactData: Artifact): string | undefined => {
    const firstPart = artifactData.parts?.[0];
    if (!firstPart) return undefined;
    if (firstPart.kind === "file") {
        return (firstPart as FilePart).file.mimeType ?? undefined;
    }
    return (firstPart.metadata?.mimeType as string | undefined) ?? undefined;
};

const handleArtifactUpdateEvent = (args: {
    event: A2AEventSSEPayload;
    payload: any;
    eventId: string;
    eventTimestamp: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): boolean => {
    const { event, payload, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    if (event.direction !== "artifact_update" || !payload?.result?.artifact) {
        return false;
    }

    flushAggregatedTextStep(ctx, currentEventOwningTaskId);
    const artifactData = payload.result.artifact as Artifact;
    const artifactName = artifactData.name || "Unnamed Artifact";
    if (isIntermediateWebContentArtifact(artifactName)) {
        return true;
    }

    const artifactAgentName = (artifactData.metadata?.agent_name as string) || event.source_entity || "Agent";
    const artifactNotification: ArtifactNotificationData = {
        artifactName,
        version: typeof artifactData.metadata?.version === "number" ? artifactData.metadata.version : undefined,
        description: artifactData.description ?? undefined,
        mimeType: extractArtifactMimeType(artifactData),
    };
    ctx.visualizerSteps.push({
        id: `vstep-artifactnotify-${ctx.visualizerSteps.length}-${eventId}`,
        type: "AGENT_ARTIFACT_NOTIFICATION",
        timestamp: eventTimestamp,
        title: `${artifactAgentName}: Artifact Update - ${artifactNotification.artifactName}`,
        source: artifactAgentName,
        target: "User/System",
        data: { artifactNotification },
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
    return true;
};

const buildTaskFailedErrorDetails = (args: { finalState: string; includeFinalMessage: boolean; finalMessageTextFromEvent: string; payload: any }): { message: string; code?: number | string; details?: any } => {
    const { finalState, includeFinalMessage, finalMessageTextFromEvent, payload } = args;
    const errorDetails: { message: string; code?: number | string; details?: any } = {
        message: includeFinalMessage ? finalMessageTextFromEvent : `Task ${finalState}.`,
    };
    const rpcError = payload.error as JSONRPCError | undefined;
    if (rpcError) {
        errorDetails.message = rpcError.message || errorDetails.message;
        errorDetails.code = rpcError.code;
        if (rpcError.data) errorDetails.details = rpcError.data;
    }
    const result = payload.result;
    if (result?.error) {
        errorDetails.message = result.error.message || errorDetails.message;
        errorDetails.code = result.error.code || errorDetails.code;
        errorDetails.details = result.error.data || errorDetails.details;
    }
    return errorDetails;
};

const tryHandleTaskCompletionEvent = (args: {
    event: A2AEventSSEPayload;
    payload: any;
    eventId: string;
    eventTimestamp: string;
    currentEventNestingLevel: number;
    currentEventOwningTaskId: string;
    functionCallIdForStep: string | undefined;
    ctx: ProcessingContext;
}): boolean => {
    const { event, payload, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx } = args;
    const isErrorResponse = event.direction === "error_response" && !!payload?.error;
    if (!isErrorResponse && (!["response", "task"].includes(event.direction) || !payload?.result?.status?.state)) {
        return false;
    }

    if (ctx.aggregation.text.trim()) {
        flushAggregatedTextStep(ctx, currentEventOwningTaskId);
    }

    const result = payload.result;
    const finalState = isErrorResponse ? "failed" : (result.status.state as string);
    if (!["completed", "failed", "canceled"].includes(finalState)) {
        return false;
    }

    const responseAgentName = result?.metadata?.agent_name || result?.status?.message?.metadata?.agent_name || event.source_entity || "Agent";
    const stepType: VisualizerStepType = finalState === "completed" ? "TASK_COMPLETED" : "TASK_FAILED";
    const title = `${responseAgentName}: Task ${finalState.charAt(0).toUpperCase() + finalState.slice(1)}`;

    const textPart = result?.status?.message?.parts?.find((p: any) => p.kind === "text") as TextPart | undefined;
    const finalMessageTextFromEvent = textPart?.text ? textPart.text.trim() : "";

    const isRootTaskFinalEvent = currentEventOwningTaskId === ctx.parentTaskObject.taskId;
    const includeFinalMessage = !!finalMessageTextFromEvent && (!isRootTaskFinalEvent || finalMessageTextFromEvent !== ctx.aggregation.lastFlushedText);

    const dataPayload =
        stepType === "TASK_COMPLETED" ? { finalMessage: includeFinalMessage ? finalMessageTextFromEvent : undefined } : { errorDetails: buildTaskFailedErrorDetails({ finalState, includeFinalMessage, finalMessageTextFromEvent, payload }) };

    ctx.visualizerSteps.push({
        id: `vstep-${finalState}-${ctx.visualizerSteps.length}-${eventId}`,
        type: stepType,
        timestamp: eventTimestamp,
        title,
        source: responseAgentName,
        target: "User",
        data: dataPayload,
        rawEventIds: [eventId],
        isSubTaskStep: currentEventNestingLevel > 0,
        nestingLevel: currentEventNestingLevel,
        owningTaskId: currentEventOwningTaskId,
        functionCallId: functionCallIdForStep,
    });
    if (isRootTaskFinalEvent) ctx.aggregation.lastFlushedText = null;
    return true;
};

const resolveFallbackSourceAgentName = (event: A2AEventSSEPayload, payload: any): string | undefined => {
    const statusMetadata = payload?.result?.status?.message?.metadata;
    const resultMetadata = payload?.result?.metadata;
    if (statusMetadata?.forwarded_from_peer) return statusMetadata.forwarded_from_peer;
    if (resultMetadata?.agent_name) return resultMetadata.agent_name;
    if (statusMetadata?.agent_name) return statusMetadata.agent_name;
    return event.source_entity;
};

const flushAggregatedTextIfStaleEvent = (args: { event: A2AEventSSEPayload; payload: any; currentEventOwningTaskId: string; ctx: ProcessingContext }): void => {
    const { event, payload, currentEventOwningTaskId, ctx } = args;
    if (!ctx.aggregation.text.trim() || !ctx.aggregation.sourceAgent) {
        return;
    }

    const isStreamingTextEvent = event.direction === "status-update" && !!payload?.result?.status?.message?.parts?.some((p: any) => p.kind === "text");
    const currentEventSourceAgentName = resolveFallbackSourceAgentName(event, payload);
    if (isStreamingTextEvent && currentEventSourceAgentName === ctx.aggregation.sourceAgent) {
        return;
    }

    flushAggregatedTextStep(ctx, currentEventOwningTaskId);
    const ownerForFlushedText = ctx.aggregation.rawEventIds[0]?.split("-")[1] || ctx.parentTaskObject.taskId;
    if (ctx.taskNestingLevels.get(ownerForFlushedText) === 0 && !ctx.aggregation.isForwardedContext) {
        ctx.aggregation.lastFlushedText = null;
    }
};

const processSingleEvent = (event: A2AEventSSEPayload, index: number, ctx: ProcessingContext): void => {
    const eventTimestamp = getEventTimestamp(event);
    const eventId = `raw-${event.task_id || "global"}-${index}`;
    const payload = event.full_payload;
    const currentEventOwningTaskId = event.task_id || ctx.parentTaskObject.taskId;
    const currentEventNestingLevel = ctx.taskNestingLevels.get(currentEventOwningTaskId) ?? 0;

    const currentTask = ctx.allMonitoredTasks[currentEventOwningTaskId];
    const parentTaskId = getParentTaskIdFromTaskObject(currentTask);
    const eventAgentName = resolveEventAgentName(event, payload);

    const functionCallIdForStep = currentEventNestingLevel > 0 ? ctx.subTaskToFunctionCallIdMap.get(currentEventOwningTaskId) : undefined;

    if (handleSubTaskRequestEvent({ event, payload, eventId, eventTimestamp, eventAgentName, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, ctx })) {
        return;
    }

    if (event.payload_summary?.method === "tasks/cancel") {
        return;
    }

    if (event.direction === "request" && currentEventNestingLevel === 0 && event.task_id === ctx.parentTaskObject.taskId) {
        handleUserRequestEvent({ event, payload, eventId, eventTimestamp, eventAgentName, currentEventOwningTaskId, ctx });
        return;
    }

    if (handleStatusUpdateEvent({ event, payload, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, parentTaskId, functionCallIdForStep, ctx })) {
        return;
    }

    if (handleArtifactUpdateEvent({ event, payload, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx })) {
        return;
    }

    if (tryHandleTaskCompletionEvent({ event, payload, eventId, eventTimestamp, currentEventNestingLevel, currentEventOwningTaskId, functionCallIdForStep, ctx })) {
        return;
    }

    flushAggregatedTextIfStaleEvent({ event, payload, currentEventOwningTaskId, ctx });
};

const resolveTaskStatusAndEndTime = (visualizerSteps: VisualizerStep[], rootTaskId: string): { status: TaskState; endTime: string | undefined } => {
    const lastRootTaskStep = visualizerSteps.findLast(step => step.owningTaskId === rootTaskId);
    if (lastRootTaskStep?.type === "TASK_COMPLETED") {
        return { status: "completed", endTime: lastRootTaskStep.timestamp };
    }
    if (lastRootTaskStep?.type === "TASK_FAILED") {
        return { status: "failed", endTime: lastRootTaskStep.timestamp };
    }
    return { status: "working", endTime: undefined };
};

const collectPrecedingAgentResponseText = (steps: VisualizerStep[], completedIndex: number, source: string | undefined): string => {
    let text = "";
    for (let i = completedIndex - 1; i >= 0; i--) {
        const prevStep = steps[i];
        if (prevStep.type !== "AGENT_RESPONSE_TEXT" || prevStep.source !== source) {
            break;
        }
        text = prevStep.data.text + text;
    }
    return text;
};

const backfillFinalTurnText = (steps: VisualizerStep[]): void => {
    steps.forEach((step, index) => {
        if (step.type !== "TASK_COMPLETED") return;
        let finalTurnText = collectPrecedingAgentResponseText(steps, index, step.source);
        if (step.data.finalMessage) {
            finalTurnText += step.data.finalMessage;
        }
        step.data.finalMessage = finalTurnText.trim() || undefined;
    });
};

const computeToolTimeMs = (toolCalls: ToolCallPerformance[]): number => {
    const sequentialCalls = toolCalls.filter(call => !call.parallelBlockId);
    const parallelBlocks = new Map<string, ToolCallPerformance[]>();
    toolCalls.forEach(call => {
        if (!call.parallelBlockId) return;
        if (!parallelBlocks.has(call.parallelBlockId)) {
            parallelBlocks.set(call.parallelBlockId, []);
        }
        parallelBlocks.get(call.parallelBlockId)!.push(call);
    });

    const sequentialTime = sequentialCalls.reduce((sum, call) => sum + call.durationMs, 0);
    let parallelTime = 0;
    parallelBlocks.forEach(blockCalls => {
        if (blockCalls.length === 0) return;
        const startTime = Math.min(...blockCalls.map(call => new Date(call.timestamp).getTime()));
        const endTime = Math.max(...blockCalls.map(call => new Date(call.timestamp).getTime() + call.durationMs));
        parallelTime += endTime - startTime;
    });

    return sequentialTime + parallelTime;
};

const computeAgentTimings = (report: PerformanceReport): void => {
    Object.values(report.agents).forEach(agentMetrics => {
        agentMetrics.totalLlmTimeMs = agentMetrics.llmCalls.reduce((sum, call) => sum + call.durationMs, 0);
        agentMetrics.totalToolTimeMs = computeToolTimeMs(agentMetrics.toolCalls);
    });
};

const getFirstAgentActivityTimestamp = (metrics: AgentPerformanceMetrics): number => {
    const allTimestamps = [...metrics.llmCalls.map(c => new Date(c.timestamp).getTime()), ...metrics.toolCalls.map(c => new Date(c.timestamp).getTime())];
    return allTimestamps.length > 0 ? Math.min(...allTimestamps) : Infinity;
};

const assignAgentDisplayNames = (report: PerformanceReport, agentNameMap: Record<string, string> | undefined): void => {
    const friendlyName = (slug: string): string => agentNameMap?.[slug] ?? slug;
    const agentInstancesByName = new Map<string, AgentPerformanceMetrics[]>();
    Object.values(report.agents).forEach(agentMetrics => {
        if (!agentInstancesByName.has(agentMetrics.agentName)) {
            agentInstancesByName.set(agentMetrics.agentName, []);
        }
        agentInstancesByName.get(agentMetrics.agentName)!.push(agentMetrics);
    });

    agentInstancesByName.forEach(instances => {
        if (instances.length > 1) {
            instances.sort((a, b) => getFirstAgentActivityTimestamp(a) - getFirstAgentActivityTimestamp(b));
            instances.forEach((instance, index) => {
                instance.displayName = `${friendlyName(instance.agentName)} (${index + 1})`;
            });
        } else {
            instances[0].displayName = friendlyName(instances[0].agentName);
        }
    });
};

const linkPeerDelegationDisplayNames = (report: PerformanceReport): void => {
    const subTaskToDisplayNameMap = new Map<string, string>();
    Object.values(report.agents).forEach(agentMetrics => {
        const owningTaskId = agentMetrics.instanceId.split(":").slice(1).join(":");
        if (owningTaskId) {
            subTaskToDisplayNameMap.set(owningTaskId, agentMetrics.displayName);
        }
    });

    Object.values(report.agents).forEach(agentMetrics => {
        agentMetrics.toolCalls.forEach(call => {
            if (!call.isPeer || !call.subTaskId) return;
            const peerDisplayName = subTaskToDisplayNameMap.get(call.subTaskId);
            if (peerDisplayName) {
                call.peerAgentName = peerDisplayName;
            }
        });
    });
};

export const processTaskForVisualization = (_rawEventsForParentTask: A2AEventSSEPayload[], allMonitoredTasks: Record<string, TaskFE>, parentTaskObject: TaskFE | null, agentNameMap?: Record<string, string>): VisualizedTask | null => {
    if (!parentTaskObject) {
        return null;
    }

    allMonitoredTasks = synthesizeMonitoredSubTasksFromForwardedEvents(allMonitoredTasks);

    const hasRequestEvent = parentTaskObject.events?.some(event => event.direction === "request" && event.task_id === parentTaskObject.taskId);
    const syntheticRequestEvent: A2AEventSSEPayload | null = hasRequestEvent ? null : buildSyntheticRequestEvent(parentTaskObject);

    const taskNestingLevels = new Map<string, number>();
    let combinedEvents = collectAllDescendantEvents(parentTaskObject.taskId, allMonitoredTasks, taskNestingLevels, 0, new Set());

    if (syntheticRequestEvent) {
        combinedEvents = [syntheticRequestEvent, ...combinedEvents];
    }

    if (combinedEvents.length === 0) {
        return {
            taskId: parentTaskObject.taskId,
            initialRequestText: parentTaskObject.initialRequestText,
            status: "working",
            startTime: parentTaskObject.firstSeen.toISOString(),
            steps: [],
        } as unknown as VisualizedTask;
    }

    const sortedEvents = withoutWorkflowToolNodeTelemetry(combinedEvents).toSorted((a, b) => new Date(getEventTimestamp(a)).getTime() - new Date(getEventTimestamp(b)).getTime());

    const ctx: ProcessingContext = {
        allMonitoredTasks,
        parentTaskObject,
        taskNestingLevels,
        visualizerSteps: [],
        report: { overall: { totalTaskDurationMs: 0 }, agents: {} },
        inProgressLlmCalls: new Map(),
        inProgressToolCalls: new Map(),
        pendingArtifacts: new Map(),
        subTaskToFunctionCallIdMap: new Map(),
        functionCallIdToDelegationInfoMap: new Map(),
        processedWorkflowEvents: new Set(),
        aggregation: { text: "", rawEventIds: [], lastFlushedText: null },
        statusText: {},
    };

    sortedEvents.forEach((event, index) => processSingleEvent(event, index, ctx));

    flushAggregatedTextStep(ctx, sortedEvents.at(-1)?.task_id || parentTaskObject.taskId);
    attachWorkflowToolCalls(ctx.visualizerSteps, collectWorkflowToolCalls(combinedEvents));

    const startTime = sortedEvents[0] ? getEventTimestamp(sortedEvents[0]) : parentTaskObject.firstSeen.toISOString();
    const { status: taskStatus, endTime } = resolveTaskStatusAndEndTime(ctx.visualizerSteps, parentTaskObject.taskId);
    const totalDurationMs = startTime && endTime ? new Date(endTime).getTime() - new Date(startTime).getTime() : undefined;

    const currentStatusText = ["completed", "failed", "canceled", "rejected"].includes(taskStatus) ? undefined : ctx.statusText.current;

    const visualizedTask: VisualizedTask = {
        taskId: parentTaskObject.taskId,
        initialRequestText: parentTaskObject.initialRequestText,
        status: taskStatus,
        currentStatusText,
        startTime,
        endTime,
        durationMs: totalDurationMs,
        steps: ctx.visualizerSteps,
    };

    backfillFinalTurnText(visualizedTask.steps);

    computeAgentTimings(ctx.report);
    assignAgentDisplayNames(ctx.report, agentNameMap);
    linkPeerDelegationDisplayNames(ctx.report);
    if (totalDurationMs !== undefined) {
        ctx.report.overall.totalTaskDurationMs = totalDurationMs;
    }

    visualizedTask.performanceReport = ctx.report;

    return visualizedTask;
};
