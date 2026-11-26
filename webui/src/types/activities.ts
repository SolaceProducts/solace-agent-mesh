// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any */
import type { TaskState } from "./a2a";

export interface LlmCallPerformance {
    modelName: string;
    durationMs: number;
    timestamp: string;
}

export interface ToolCallPerformance {
    toolName: string;
    durationMs: number;
    isPeer: boolean;
    peerAgentName?: string;
    subTaskId?: string;
    timestamp: string;
    parallelBlockId?: string;
}

export interface AgentPerformanceMetrics {
    agentName: string;
    instanceId: string;
    displayName: string;
    llmCalls: LlmCallPerformance[];
    toolCalls: ToolCallPerformance[];
    totalLlmTimeMs: number;
    totalToolTimeMs: number;
}

export interface PerformanceReport {
    overall: {
        totalTaskDurationMs: number;
    };
    agents: Record<string, AgentPerformanceMetrics>;
}

export type VisualizerStepType =
    | "USER_REQUEST"
    | "WORKFLOW_AGENT_REQUEST"
    | "AGENT_LLM_CALL"
    | "AGENT_LLM_RESPONSE_TOOL_DECISION"
    | "AGENT_LLM_RESPONSE_TO_AGENT"
    | "AGENT_TOOL_INVOCATION_START"
    | "AGENT_TOOL_EXECUTION_RESULT"
    | "AGENT_ARTIFACT_NOTIFICATION"
    | "AGENT_RESPONSE_TEXT"
    | "AGENT_STATUS_UPDATE"
    | "TASK_COMPLETED"
    | "TASK_FAILED"
    | "WORKFLOW_EXECUTION_START"
    | "WORKFLOW_NODE_EXECUTION_START"
    | "WORKFLOW_NODE_EXECUTION_RESULT"
    | "WORKFLOW_MAP_PROGRESS"
    | "WORKFLOW_EXECUTION_RESULT";

export interface LLMCallData {
    modelName: string;
    promptPreview: string;
}

export interface LLMResponseToAgentData {
    modelName?: string;
    responsePreview: string;
    response: string;
    isFinalResponse?: boolean;
}

export interface ToolDecision {
    functionCallId: string;
    toolName: string;
    toolArguments: Record<string, any>;
    isPeerDelegation: boolean;
}

export interface ToolDecisionData {
    decisions: ToolDecision[];
    isParallel: boolean;
    isPeerDelegation?: boolean;
}

export interface ToolInvocationStartData {
    functionCallId: string;
    toolName: string;
    toolArguments: Record<string, any>;
    isPeerInvocation?: boolean;
    parallelGroupId?: string;
}

export interface ToolResultData {
    functionCallId?: string;
    toolName: string;
    resultData: any;
    isPeerResponse?: boolean;
}

export interface ArtifactNotificationData {
    artifactName: string;
    version?: number;
    description?: string;
    mimeType?: string;
}

export interface ErrorDetailsData {
    message: string;
    code?: number | string;
    details?: any;
}

export interface DelegationInfo {
    functionCallId: string;
    peerAgentName: string;
    subTaskId?: string;
}

export interface ArtifactRef {
    name: string;
    version?: number;
}

export interface WorkflowExecutionStartData {
    workflowName: string;
    executionId: string;
    inputArtifactRef?: ArtifactRef;
    workflowInput?: Record<string, any>;
}

export interface WorkflowAgentRequestData {
    agentName: string;
    nodeId?: string;
    workflowName?: string;
    inputText?: string;
    inputArtifactRef?: {
        name: string;
        version?: number;
        uri?: string;
        mimeType?: string;
    };
    instruction?: string;
    inputSchema?: Record<string, any>;
    outputSchema?: Record<string, any>;
    suggestedOutputFilename?: string;
}

export interface SwitchCaseInfo {
    condition: string;
    node: string;
}

export interface WorkflowNodeExecutionStartData {
    nodeId: string;
    nodeType: string;
    agentName?: string;
    inputArtifactRef?: ArtifactRef;
    iterationIndex?: number;
    condition?: string;
    trueBranch?: string;
    falseBranch?: string;
    trueBranchLabel?: string;
    falseBranchLabel?: string;
    subTaskId?: string;
    parentNodeId?: string;
    cases?: SwitchCaseInfo[];
    defaultBranch?: string;
    waitFor?: string[];
    joinStrategy?: string;
    joinN?: number;
    maxIterations?: number;
    loopDelay?: string;
    parallelGroupId?: string;
    dependsOn?: string[];
    toolName?: string;
    toolArguments?: Record<string, any>;
}

export interface WorkflowNodeExecutionResultData {
    nodeId: string;
    status: "success" | "error" | "failure" | "skipped";
    outputArtifactRef?: ArtifactRef;
    toolResultData?: unknown;
    errorMessage?: string;
    metadata?: Record<string, any>;
    conditionResult?: boolean;
    selectedBranch?: string;
    selectedCaseIndex?: number;
}

export interface WorkflowMapProgressData {
    nodeId: string;
    totalItems: number;
    completedItems: number;
    status: "in-progress" | "completed" | "failed";
}

export interface WorkflowExecutionResultData {
    status: "success" | "failure" | "error";
    outputArtifactRef?: ArtifactRef;
    errorMessage?: string;
    workflowOutput?: Record<string, any>;
}

export interface VisualizerStep {
    id: string;
    type: VisualizerStepType;
    timestamp: string;
    durationMs?: number;
    title: string;
    source?: string;
    target?: string;
    data: {
        text?: string;
        llmCall?: LLMCallData;
        llmResponseToAgent?: LLMResponseToAgentData;
        toolDecision?: ToolDecisionData;
        toolInvocationStart?: ToolInvocationStartData;
        toolResult?: ToolResultData;
        artifactNotification?: ArtifactNotificationData;
        errorDetails?: ErrorDetailsData;
        statusText?: string;
        finalMessage?: string;
        workflowExecutionStart?: WorkflowExecutionStartData;
        workflowAgentRequest?: WorkflowAgentRequestData;
        workflowNodeExecutionStart?: WorkflowNodeExecutionStartData;
        workflowNodeExecutionResult?: WorkflowNodeExecutionResultData;
        workflowMapProgress?: WorkflowMapProgressData;
        workflowExecutionResult?: WorkflowExecutionResultData;
    };
    rawEventIds: string[];
    functionCallId?: string;
    delegationInfo?: DelegationInfo[];
    isSubTaskStep?: boolean;
    nestingLevel: number;
    owningTaskId: string;
    parentTaskId?: string;
}

export interface VisualizedTask {
    taskId: string;
    initialRequestText: string;
    status: TaskState;
    currentStatusText?: string;
    startTime: string;
    endTime?: string;
    durationMs?: number;
    steps: VisualizerStep[];
    performanceReport?: PerformanceReport | null;
}
