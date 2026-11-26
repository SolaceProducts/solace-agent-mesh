// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { VisualizerStep } from "@/types";

import { decisionResultOf, decisionRoute } from "../../decisionResult";

import { isFailedResultStatus, isPlaceholderToolResult, toolResultStatus } from "./resultStatus";
import type { BuildContext, Edge, EscalationRouting, LayoutNode, LayoutResult, WorkflowDefinitions } from "./types";

const NODE_WIDTHS = {
    AGENT: 220,
    TOOL: 180,
    LLM: 180,
    USER: 140,
    SWITCH: 120,
    LOOP: 120,
    MAP: 120,
    MIN_AGENT_CONTENT: 200,
};

const NODE_HEIGHTS = {
    AGENT_HEADER: 50,
    TOOL: 50,
    LLM: 50,
    USER: 50,
    SWITCH: 80,
    LOOP: 80,
    MAP: 80,
};

const SPACING = {
    VERTICAL: 16,
    HORIZONTAL: 20,
    AGENT_VERTICAL: 60,
    PADDING: 20,
};

function resolveAgentName(context: BuildContext, agentName: string): string {
    const displayName = context.agentNameMap[agentName] || agentName;

    if (!context.agentNameMap[agentName] && context.onUnknownAgent) {
        context.onUnknownAgent(agentName);
    }

    return displayName;
}

export function processSteps(steps: VisualizerStep[], agentNameMap: Record<string, string> = {}, onUnknownAgent?: (agentName: string) => void, workflowDefinitions?: WorkflowDefinitions): LayoutResult {
    const context: BuildContext = {
        steps,
        stepIndex: 0,
        nodeCounter: 0,
        workflowDefinitions,
        taskToNodeMap: new Map(),
        functionCallToNodeMap: new Map(),
        currentAgentNode: null,
        rootNodes: [],
        agentNameMap,
        onUnknownAgent,
        parallelContainerMap: new Map(),
        currentBranchMap: new Map(),
        hasTopUserNode: false,
        hasBottomUserNode: false,
        parallelPeerGroupMap: new Map(),
        parallelBlockMap: new Map(),
        subWorkflowParentMap: new Map(),
        workflowFunctionCallIdMap: new Map(),
        workflowParentTaskFunctionCallIds: new Map(),
    };

    for (let i = 0; i < steps.length; i++) {
        context.stepIndex = i;
        const step = steps[i];
        processStep(step, context);
    }

    const nodes = calculateLayout(context.rootNodes);

    const edges = calculateEdges(nodes);

    const { totalWidth, totalHeight } = calculateCanvasSize(nodes);

    return {
        nodes,
        edges,
        totalWidth,
        totalHeight,
    };
}

function processStep(step: VisualizerStep, context: BuildContext): void {
    switch (step.type) {
        case "USER_REQUEST":
            handleUserRequest(step, context);
            break;
        case "AGENT_LLM_CALL":
            handleLLMCall(step, context);
            break;
        case "AGENT_TOOL_INVOCATION_START":
            handleToolInvocation(step, context);
            break;
        case "AGENT_TOOL_EXECUTION_RESULT":
            handleToolResult(step, context);
            break;
        case "AGENT_LLM_RESPONSE_TO_AGENT":
        case "AGENT_LLM_RESPONSE_TOOL_DECISION":
            handleLLMResponse(step, context);
            break;
        case "AGENT_RESPONSE_TEXT":
            handleAgentResponse(step, context);
            break;
        case "WORKFLOW_EXECUTION_START":
            handleWorkflowStart(step, context);
            break;
        case "WORKFLOW_NODE_EXECUTION_START":
            if (context.eagerlyProcessedStepIds?.has(step.id)) break;
            handleWorkflowNodeStart(step, context);
            break;
        case "WORKFLOW_EXECUTION_RESULT":
            handleWorkflowExecutionResult(step, context);
            break;
        case "WORKFLOW_NODE_EXECUTION_RESULT":
            handleWorkflowNodeResult(step, context);
            break;
        case "AGENT_ARTIFACT_NOTIFICATION":
            handleArtifactNotification(step, context);
            break;
    }
}

function handleUserRequest(step: VisualizerStep, context: BuildContext): void {
    if (!context.hasTopUserNode && step.nestingLevel === 0) {
        const userNode = createNode(
            context,
            "user",
            {
                label: "User",
                visualizerStepId: step.id,
                isTopNode: true,
            },
            step.owningTaskId
        );
        context.rootNodes.unshift(userNode);
        context.hasTopUserNode = true;
    }

    if (step.owningTaskId) {
        const existingAgent = context.taskToNodeMap.get(step.owningTaskId);
        if (existingAgent) {
            context.currentAgentNode = existingAgent;
            return;
        }
    }

    const agentName = step.target || "Agent";
    const displayName = resolveAgentName(context, agentName);

    const agentNode = createNode(
        context,
        "agent",
        {
            label: displayName,
            visualizerStepId: step.id,
        },
        step.owningTaskId
    );

    context.rootNodes.push(agentNode);

    context.currentAgentNode = agentNode;

    if (step.owningTaskId) {
        context.taskToNodeMap.set(step.owningTaskId, agentNode);
    }
}

function eagerlyRegisterSubAgentIfPending(step: VisualizerStep, context: BuildContext): void {
    if (!step.owningTaskId || context.taskToNodeMap.has(step.owningTaskId)) return;

    for (let i = context.stepIndex + 1; i < context.steps.length; i++) {
        const futureStep = context.steps[i];
        if (futureStep.type === "WORKFLOW_NODE_EXECUTION_START" && futureStep.data.workflowNodeExecutionStart?.subTaskId === step.owningTaskId) {
            context.eagerlyProcessedStepIds ??= new Set();
            if (!context.eagerlyProcessedStepIds.has(futureStep.id)) {
                context.eagerlyProcessedStepIds.add(futureStep.id);
                handleWorkflowNodeStart(futureStep, context);
            }
            return;
        }
    }
}

function handleLLMCall(step: VisualizerStep, context: BuildContext): void {
    eagerlyRegisterSubAgentIfPending(step, context);

    let agentNode = findAgentForStep(step, context);

    if (!agentNode && step.owningTaskId && step.source) {
        const displayName = resolveAgentName(context, step.source);
        agentNode = createNode(
            context,
            "agent",
            {
                label: displayName,
                visualizerStepId: step.id,
            },
            step.owningTaskId
        );
        context.rootNodes.push(agentNode);
        context.currentAgentNode = agentNode;
        context.taskToNodeMap.set(step.owningTaskId, agentNode);
    }

    if (!agentNode) return;

    const llmNode = createNode(
        context,
        "llm",
        {
            label: "LLM",
            visualizerStepId: step.id,
            status: "in-progress",
        },
        step.owningTaskId
    );

    agentNode.children.push(llmNode);

    if (step.functionCallId) {
        context.functionCallToNodeMap.set(step.functionCallId, llmNode);
    }
}

function handleLLMResponse(step: VisualizerStep, context: BuildContext): void {
    const agentNode = findAgentForStep(step, context);
    if (!agentNode) {
        return;
    }

    for (let i = agentNode.children.length - 1; i >= 0; i--) {
        const child = agentNode.children[i];
        if (child.type === "llm" && child.data.status === "in-progress") {
            child.data.status = "completed";
            break;
        }
    }

    if (step.type === "AGENT_LLM_RESPONSE_TOOL_DECISION") {
        const toolDecision = step.data.toolDecision;
        if (toolDecision?.isParallel && toolDecision.decisions) {
            const peerDecisions = toolDecision.decisions.filter(d => d.isPeerDelegation);

            const workflowDecisions = toolDecision.decisions.filter(d => !d.isPeerDelegation && d.toolName.includes("workflow_"));

            if (peerDecisions.length > 1) {
                const groupKey = `${step.owningTaskId}:parallel-peer:${step.id}`;
                const functionCallIds = new Set(peerDecisions.map(d => d.functionCallId));
                context.parallelPeerGroupMap.set(groupKey, functionCallIds);
            }

            if (workflowDecisions.length > 1) {
                const groupKey = `${step.owningTaskId}:parallel-workflow:${step.id}`;
                const functionCallIds = new Set(workflowDecisions.map(d => d.functionCallId));
                context.parallelPeerGroupMap.set(groupKey, functionCallIds);
            }
        }
    }
}

function handleToolInvocation(step: VisualizerStep, context: BuildContext): void {
    const isPeer = step.data.toolInvocationStart?.isPeerInvocation || step.target?.startsWith("peer_");
    const target = step.target || "";
    const toolName = step.data.toolInvocationStart?.toolName || target;
    const parallelGroupId = step.data.toolInvocationStart?.parallelGroupId;

    if (target.includes("workflow_") || toolName.includes("workflow_")) {
        const functionCallId = step.data.toolInvocationStart?.functionCallId || step.functionCallId;
        const subTaskId = step.delegationInfo?.[0]?.subTaskId;
        const parentTaskId = step.owningTaskId;

        if (subTaskId && functionCallId) {
            context.workflowFunctionCallIdMap.set(subTaskId, functionCallId);
        }

        if (parentTaskId && functionCallId) {
            if (!context.workflowParentTaskFunctionCallIds.has(parentTaskId)) {
                context.workflowParentTaskFunctionCallIds.set(parentTaskId, new Set());
            }
            context.workflowParentTaskFunctionCallIds.get(parentTaskId)!.add(functionCallId);
        }
        return;
    }

    let agentNode = findAgentForStep(step, context);

    if (!agentNode && isPeer && step.owningTaskId && step.source) {
        const displayName = resolveAgentName(context, step.source);
        agentNode = createNode(
            context,
            "agent",
            {
                label: displayName,
                visualizerStepId: step.id,
            },
            step.owningTaskId
        );
        context.rootNodes.push(agentNode);
        context.currentAgentNode = agentNode;
        context.taskToNodeMap.set(step.owningTaskId, agentNode);
    }

    if (!agentNode) {
        return;
    }

    if (isPeer) {
        const peerName = target.startsWith("peer_") ? target.substring(5) : target;
        const displayName = resolveAgentName(context, peerName);

        const subAgentNode = createNode(
            context,
            "agent",
            {
                label: displayName,
                visualizerStepId: step.id,
            },
            step.delegationInfo?.[0]?.subTaskId || step.owningTaskId
        );

        const functionCallId = step.data.toolInvocationStart?.functionCallId || step.functionCallId;
        let addedToParallelBlock = false;

        if (parallelGroupId) {
            let parallelBlock = context.parallelBlockMap.get(parallelGroupId);
            if (!parallelBlock) {
                parallelBlock = createNode(
                    context,
                    "parallelBlock",
                    {
                        label: "Parallel",
                        visualizerStepId: step.id,
                    },
                    step.owningTaskId
                );
                context.parallelBlockMap.set(parallelGroupId, parallelBlock);
                agentNode.children.push(parallelBlock);
            }
            parallelBlock.children.push(subAgentNode);
            addedToParallelBlock = true;
        } else if (functionCallId) {
            for (const [groupKey, functionCallIds] of context.parallelPeerGroupMap.entries()) {
                if (functionCallIds.has(functionCallId)) {
                    let parallelBlock = context.parallelBlockMap.get(groupKey);
                    if (!parallelBlock) {
                        parallelBlock = createNode(
                            context,
                            "parallelBlock",
                            {
                                label: "Parallel",
                                visualizerStepId: step.id,
                            },
                            step.owningTaskId
                        );
                        context.parallelBlockMap.set(groupKey, parallelBlock);
                        agentNode.children.push(parallelBlock);
                    }
                    parallelBlock.children.push(subAgentNode);
                    addedToParallelBlock = true;
                    break;
                }
            }
        }

        if (!addedToParallelBlock) {
            agentNode.children.push(subAgentNode);
        }

        const subTaskId = step.delegationInfo?.[0]?.subTaskId;
        if (subTaskId) {
            context.taskToNodeMap.set(subTaskId, subAgentNode);
        }

        if (functionCallId) {
            context.functionCallToNodeMap.set(functionCallId, subAgentNode);
        }
    } else {
        const toolNode = createNode(
            context,
            "tool",
            {
                label: toolName,
                visualizerStepId: step.id,
                status: "in-progress",
            },
            step.owningTaskId
        );

        if (parallelGroupId) {
            let parallelBlock = context.parallelBlockMap.get(parallelGroupId);
            if (!parallelBlock) {
                parallelBlock = createNode(
                    context,
                    "parallelBlock",
                    {
                        label: "Parallel Tools",
                        visualizerStepId: step.id,
                    },
                    step.owningTaskId
                );
                context.parallelBlockMap.set(parallelGroupId, parallelBlock);
                agentNode.children.push(parallelBlock);
            }
            parallelBlock.children.push(toolNode);
        } else {
            agentNode.children.push(toolNode);
        }

        const functionCallId = step.data.toolInvocationStart?.functionCallId || step.functionCallId;
        if (functionCallId) {
            context.functionCallToNodeMap.set(functionCallId, toolNode);
        }
    }
}

function handleToolResult(step: VisualizerStep, context: BuildContext): void {
    const functionCallId = step.data.toolResult?.functionCallId || step.functionCallId;
    if (!functionCallId) return;

    const node = context.functionCallToNodeMap.get(functionCallId);
    const resultData = step.data.toolResult?.resultData;
    if (node && !isPlaceholderToolResult(resultData)) {
        node.data.status = isFailedResultStatus(toolResultStatus(resultData)) ? "error" : "completed";
    }
}

function handleArtifactNotification(step: VisualizerStep, context: BuildContext): void {
    const functionCallId = step.functionCallId;
    if (!functionCallId) return;

    const node = context.functionCallToNodeMap.get(functionCallId);
    if (node && step.data.artifactNotification) {
        if (!node.data.createdArtifacts) {
            node.data.createdArtifacts = [];
        }
        node.data.createdArtifacts.push({
            filename: step.data.artifactNotification.artifactName,
            version: step.data.artifactNotification.version,
            mimeType: step.data.artifactNotification.mimeType,
            description: step.data.artifactNotification.description,
        });
    }
}

function handleAgentResponse(step: VisualizerStep, context: BuildContext): void {
    if (step.nestingLevel && step.nestingLevel > 0) return;

    const remainingSteps = context.steps.slice(context.stepIndex + 1);
    const hasMoreTopLevelResponses = remainingSteps.some(s => s.type === "AGENT_RESPONSE_TEXT" && s.nestingLevel === 0);

    if (!hasMoreTopLevelResponses && !context.hasBottomUserNode) {
        const userNode = createNode(
            context,
            "user",
            {
                label: "User",
                visualizerStepId: step.id,
                isBottomNode: true,
            },
            step.owningTaskId
        );

        context.rootNodes.push(userNode);
        context.hasBottomUserNode = true;
    }
}

function handleWorkflowStart(step: VisualizerStep, context: BuildContext): void {
    const workflowName = step.data.workflowExecutionStart?.workflowName || "Workflow";
    const displayName = resolveAgentName(context, workflowName);
    const executionId = step.data.workflowExecutionStart?.executionId;

    const callingAgent = resolveWorkflowCallingAgent(step, context);

    const subWorkflowMeta = step.owningTaskId ? context.subWorkflowNodeMeta?.get(step.owningTaskId) : undefined;
    const groupNode = createNode(
        context,
        "group",
        {
            label: displayName,
            visualizerStepId: step.id,
            nodeId: subWorkflowMeta?.nodeId,
            dependsOn: subWorkflowMeta?.dependsOn,
            workflowName,
        },
        executionId || step.owningTaskId
    );

    const startNode = createNode(
        context,
        "agent",
        {
            label: "Start",
            variant: "pill",
            visualizerStepId: step.id,
        },
        step.owningTaskId
    );

    groupNode.children.push(startNode);

    placeWorkflowGroup(step, context, groupNode, callingAgent, executionId, subWorkflowMeta);

    if (executionId) {
        context.taskToNodeMap.set(executionId, groupNode);
    }

    if (step.owningTaskId && step.owningTaskId !== executionId) {
        context.taskToNodeMap.set(step.owningTaskId, groupNode);
    }
}

function resolveWorkflowCallingAgent(step: VisualizerStep, context: BuildContext): LayoutNode | null {
    const parentFromWorkflowNode = step.owningTaskId ? context.subWorkflowParentMap.get(step.owningTaskId) : null;
    if (parentFromWorkflowNode) return parentFromWorkflowNode;

    if (step.parentTaskId) {
        const fromParentTask = context.taskToNodeMap.get(step.parentTaskId);
        if (fromParentTask) return fromParentTask;
    }

    return context.currentAgentNode;
}

function resolveWorkflowFunctionCallId(step: VisualizerStep, context: BuildContext, executionId: string | undefined): string | undefined {
    const direct = context.workflowFunctionCallIdMap.get(step.owningTaskId || "") || context.workflowFunctionCallIdMap.get(executionId || "") || step.functionCallId;
    if (direct || !step.parentTaskId) return direct;

    const parentFunctionCallIds = context.workflowParentTaskFunctionCallIds.get(step.parentTaskId);
    if (!parentFunctionCallIds) return undefined;

    for (const fcId of parentFunctionCallIds) {
        if (findParallelPeerGroupKey(context, fcId)) {
            parentFunctionCallIds.delete(fcId);
            return fcId;
        }
    }
    return undefined;
}

function findParallelPeerGroupKey(context: BuildContext, functionCallId: string): string | undefined {
    for (const [groupKey, functionCallIds] of context.parallelPeerGroupMap.entries()) {
        if (functionCallIds.has(functionCallId)) {
            return groupKey;
        }
    }
    return undefined;
}

function placeWorkflowGroup(step: VisualizerStep, context: BuildContext, groupNode: LayoutNode, callingAgent: LayoutNode | null, executionId: string | undefined, subWorkflowMeta: { nodeId: string; dependsOn?: string[] } | undefined): void {
    const functionCallId = resolveWorkflowFunctionCallId(step, context, executionId);
    const groupKey = functionCallId ? findParallelPeerGroupKey(context, functionCallId) : undefined;

    if (groupKey) {
        let parallelBlock = context.parallelBlockMap.get(groupKey);
        if (!parallelBlock && callingAgent) {
            parallelBlock = createNode(
                context,
                "parallelBlock",
                {
                    label: "Parallel",
                    visualizerStepId: step.id,
                },
                step.owningTaskId
            );
            context.parallelBlockMap.set(groupKey, parallelBlock);
            callingAgent.children.push(parallelBlock);
        }
        if (parallelBlock) {
            parallelBlock.children.push(groupNode);
            return;
        }
    }

    if (callingAgent && subWorkflowMeta?.nodeId && subWorkflowMeta.dependsOn !== undefined) {
        placeDagNodeByLevel(callingAgent, groupNode, subWorkflowMeta.nodeId, subWorkflowMeta.dependsOn, context);
        return;
    }

    if (callingAgent) {
        callingAgent.children.push(groupNode);
    } else {
        context.rootNodes.push(groupNode);
    }
}

function noteTopLevelRouteCandidate(step: VisualizerStep, context: BuildContext, nodeId: string): void {
    const workflowNodeData = step.data.workflowNodeExecutionStart;
    if (workflowNodeData?.dependsOn === undefined || workflowNodeData.parentNodeId) {
        return;
    }
    const owner = findAgentForStep(step, context);
    if (owner) noteEscalationRouteCandidate(context, owner, nodeId, workflowNodeData.dependsOn);
}

function handleWorkflowNodeStart(step: VisualizerStep, context: BuildContext): void {
    const workflowNodeData = step.data.workflowNodeExecutionStart;
    const nodeType = workflowNodeData?.nodeType;
    const nodeId = workflowNodeData?.nodeId || "unknown";
    const parallelGroupId = workflowNodeData?.parallelGroupId;
    const taskId = step.owningTaskId;

    noteTopLevelRouteCandidate(step, context, nodeId);

    if (nodeType === "workflow") {
        handleWorkflowNodeType(step, context);
        return;
    }

    const { type, label } = workflowNodeTypeAndLabel(nodeType, workflowNodeData?.agentName, nodeId);
    const workflowNode = createNode(
        context,
        type,
        {
            label,
            variant: "default",
            visualizerStepId: step.id,
            condition: workflowNodeData?.condition,
            trueBranch: workflowNodeData?.trueBranch,
            falseBranch: workflowNodeData?.falseBranch,
            cases: workflowNodeData?.cases,
            defaultBranch: workflowNodeData?.defaultBranch,
            maxIterations: workflowNodeData?.maxIterations,
            loopDelay: workflowNodeData?.loopDelay,
            nodeId,
            dependsOn: workflowNodeData?.dependsOn,
            toolName: workflowNodeData?.toolName,
        },
        step.owningTaskId
    );

    if (nodeType === "agent" && workflowNodeData?.subTaskId) {
        context.taskToNodeMap.set(workflowNodeData.subTaskId, workflowNode);
    }

    if (nodeType === "map") {
        placeContainerNode(step, context, workflowNode, parallelGroupId || `${taskId}:${nodeId}`);
        return;
    }
    if (nodeType === "loop") {
        placeContainerNode(step, context, workflowNode, `${taskId}:${nodeId}`);
        return;
    }

    const parentNodeId = workflowNodeData?.parentNodeId;
    const isMapOrLoopChild = parentNodeId !== undefined && parentNodeId !== null;
    const parallelContainer = isMapOrLoopChild ? context.parallelContainerMap.get(parallelGroupId || `${taskId}:${parentNodeId}`) : null;
    if (parallelContainer) {
        placeMapLoopChild(step, parallelContainer, workflowNode);
        return;
    }

    const groupNode = findAgentForStep(step, context);
    if (!groupNode) return;

    if ((nodeType === "agent" || nodeType === "tool") && !isMapOrLoopChild && workflowNodeData?.dependsOn !== undefined) {
        placeDagNodeByLevel(groupNode, workflowNode, nodeId, workflowNodeData.dependsOn, context);
        return;
    }

    if ((nodeType === "agent" || nodeType === "tool") && parallelGroupId) {
        placeImplicitParallelNode(step, context, groupNode, workflowNode, parallelGroupId);
        return;
    }

    if (workflowNodeData?.dependsOn !== undefined) {
        recordDagLevel(context, groupNode.id, nodeId, workflowNodeData.dependsOn);
    }
    groupNode.children.push(workflowNode);
}

function workflowNodeTypeAndLabel(nodeType: string | undefined, agentName: string | undefined, nodeId: string): { type: LayoutNode["type"]; label: string } {
    switch (nodeType) {
        case "switch":
            return { type: "switch", label: "Switch" };
        case "decision":
            return { type: "decision", label: nodeId };
        case "loop":
            return { type: "loop", label: "Loop" };
        case "map":
            return { type: "map", label: "Map" };
        case "tool":
            return { type: "tool", label: nodeId };
        default:
            return { type: "agent", label: agentName || nodeId };
    }
}

function placeContainerNode(step: VisualizerStep, context: BuildContext, workflowNode: LayoutNode, containerKey: string): void {
    const groupNode = findAgentForStep(step, context);
    if (!groupNode) return;

    const dependsOn = step.data.workflowNodeExecutionStart?.dependsOn;
    if (dependsOn !== undefined) {
        recordDagLevel(context, groupNode.id, workflowNode.data.nodeId!, dependsOn);
    }

    context.parallelContainerMap.set(containerKey, workflowNode);
    groupNode.children.push(workflowNode);
}

function placeMapLoopChild(step: VisualizerStep, parallelContainer: LayoutNode, workflowNode: LayoutNode): void {
    if (parallelContainer.type !== "loop") {
        workflowNode.data.iterationIndex = step.data.workflowNodeExecutionStart?.iterationIndex ?? 0;
    }
    parallelContainer.children.push(workflowNode);
}

function placeImplicitParallelNode(step: VisualizerStep, context: BuildContext, groupNode: LayoutNode, workflowNode: LayoutNode, parallelGroupId: string): void {
    let implicitParallelBlock = context.parallelBlockMap.get(parallelGroupId);
    if (!implicitParallelBlock) {
        implicitParallelBlock = createNode(
            context,
            "parallelBlock",
            {
                label: "Parallel",
                visualizerStepId: step.id,
            },
            step.owningTaskId
        );
        context.parallelBlockMap.set(parallelGroupId, implicitParallelBlock);
        groupNode.children.push(implicitParallelBlock);
    }

    workflowNode.data.iterationIndex = step.data.workflowNodeExecutionStart?.iterationIndex ?? 0;
    implicitParallelBlock.children.push(workflowNode);
}

function recordDagLevel(context: BuildContext, groupLayoutId: string, nodeId: string, dependsOn: string[]): number {
    const levels = dagLevelsOf(context, groupLayoutId);
    let level = 0;
    for (const dep of dependsOn) {
        const depLevel = levels.get(dep);
        if (depLevel !== undefined && depLevel + 1 > level) {
            level = depLevel + 1;
        }
    }
    levels.set(nodeId, level);
    return level;
}

function dagLevelsOf(context: BuildContext, groupLayoutId: string): Map<string, number> {
    context.dagLevelsByGroup ??= new Map();
    let levels = context.dagLevelsByGroup.get(groupLayoutId);
    if (!levels) {
        levels = new Map();
        context.dagLevelsByGroup.set(groupLayoutId, levels);
    }
    return levels;
}

function placeDagNodeByLevel(groupNode: LayoutNode, workflowNode: LayoutNode, nodeId: string, dependsOn: string[], context: BuildContext): void {
    placeInLevelRow(groupNode, workflowNode, recordDagLevel(context, groupNode.id, nodeId, dependsOn), context);
}

function placeInLevelRow(groupNode: LayoutNode, workflowNode: LayoutNode, level: number, context: BuildContext): void {
    context.dagLevelBlocks ??= new Map();
    const blockKey = `${groupNode.id}:level:${level}`;
    let block = context.dagLevelBlocks.get(blockKey);
    if (!block) {
        block = createNode(
            context,
            "parallelBlock",
            {
                label: "Parallel",
                visualizerStepId: workflowNode.data.visualizerStepId,
            },
            workflowNode.owningTaskId
        );
        context.dagLevelBlocks.set(blockKey, block);
        groupNode.children.push(block);
    }

    workflowNode.data.iterationIndex = block.children.length;
    block.children.push(workflowNode);
}

function handleWorkflowNodeType(step: VisualizerStep, context: BuildContext): void {
    const workflowNodeData = step.data.workflowNodeExecutionStart;
    const subTaskId = workflowNodeData?.subTaskId;

    if (!subTaskId) {
        return;
    }

    const parentGroup = findAgentForStep(step, context);
    if (!parentGroup) {
        return;
    }

    context.subWorkflowParentMap.set(subTaskId, parentGroup);

    const nodeId = workflowNodeData?.nodeId;
    if (!nodeId) return;
    context.subWorkflowNodeMeta ??= new Map();
    context.subWorkflowNodeMeta.set(subTaskId, { nodeId, dependsOn: workflowNodeData?.dependsOn });
    if (workflowNodeData?.dependsOn !== undefined) {
        recordDagLevel(context, parentGroup.id, nodeId, workflowNodeData.dependsOn);
    }
}

function handleWorkflowExecutionResult(step: VisualizerStep, context: BuildContext): void {
    const groupNode = findAgentForStep(step, context);
    if (!groupNode) return;

    const resultData = step.data.workflowExecutionResult;
    const isError = isFailedResultStatus(resultData?.status);
    const nodeStatus = isError ? "error" : "completed";

    const finishNode = createNode(
        context,
        "agent",
        {
            label: "Finish",
            variant: "pill",
            visualizerStepId: step.id,
            status: nodeStatus,
        },
        step.owningTaskId
    );

    groupNode.children.push(finishNode);
}

function applySwitchSelection(targetNode: { data: { selectedBranch?: string; selectedCaseIndex?: number } }, metadata: Record<string, unknown> | undefined): void {
    const selectedBranch = metadata?.selected_branch;
    if (typeof selectedBranch === "string") {
        targetNode.data.selectedBranch = selectedBranch;
    }
    const selectedCaseIndex = metadata?.selected_case_index;
    if (typeof selectedCaseIndex === "number") {
        targetNode.data.selectedCaseIndex = selectedCaseIndex;
    }
}

function handleWorkflowNodeResult(step: VisualizerStep, context: BuildContext): void {
    const resultData = step.data.workflowNodeExecutionResult;
    const nodeId = resultData?.nodeId;
    const taskId = step.owningTaskId;

    if (!nodeId) return;

    const containerKey = `${taskId}:${nodeId}`;
    const parallelContainer = context.parallelContainerMap.get(containerKey);

    applyNodeResultToTree(step, context, nodeId, resultData);

    if (parallelContainer) {
        context.parallelContainerMap.delete(containerKey);
    }

    const nodeType = resultData?.metadata?.node_type;
    if (nodeType === "agent" || !parallelContainer) {
        completeInProgressLLMChildren(context, nodeId);
    }
}

function nodeStatusFromResult(status: string | undefined): "completed" | "error" | "skipped" {
    if (isFailedResultStatus(status)) {
        return "error";
    }
    if (status === "skipped") {
        return "skipped";
    }
    return "completed";
}

function applyNodeResultToTree(step: VisualizerStep, context: BuildContext, nodeId: string, resultData: VisualizerStep["data"]["workflowNodeExecutionResult"]): void {
    const groupNode = findAgentForStep(step, context);
    if (!groupNode) return;

    const targetNode = findNodeById(groupNode, nodeId);
    if (!targetNode) {
        if (resultData?.status === "skipped") {
            placeSkippedNode(step, context, groupNode, nodeId, resultData.metadata);
        }
        return;
    }

    targetNode.data.status = nodeStatusFromResult(resultData?.status);

    if (targetNode.type === "switch") {
        applySwitchSelection(targetNode, resultData?.metadata);
    }
    if (targetNode.type === "decision") {
        applyDecisionResult(targetNode, resultData?.metadata);
    }
}

function applyDecisionResult(targetNode: LayoutNode, metadata: Record<string, unknown> | undefined): void {
    const decision = decisionResultOf(metadata);
    if (!decision) return;
    targetNode.data.decision = decision;
    targetNode.data.selectedBranch = decisionRoute(decision);
}

const PRUNE_REASON = /^not selected by (\w+) "((?:[^"\\]|\\.)*)"$/;

const DRAWN_PRUNER_TYPES = new Set(["decision", "agent", "workflow", "tool"]);

function unquoteGoString(quoted: string): string {
    try {
        return JSON.parse(`"${quoted}"`) as string;
    } catch {
        return quoted;
    }
}

function decisionNodesIn(root: LayoutNode): LayoutNode[] {
    const found: LayoutNode[] = [];
    for (const child of root.children) {
        if (child.type === "decision") found.push(child);
        if (child.type !== "group") found.push(...decisionNodesIn(child));
    }
    return found;
}

function escalateTargetOf(decisionNode: LayoutNode): string | undefined {
    const decision = decisionNode.data.decision;
    return decision?.escalated ? decision.selectedBranch : undefined;
}

interface PruneSource {
    anchorId: string;
    escalatedDecision?: LayoutNode;
}

function pruneSourceOf(groupNode: LayoutNode, skipReason: unknown): PruneSource | undefined {
    const match = typeof skipReason === "string" ? PRUNE_REASON.exec(skipReason) : null;
    if (!match || !DRAWN_PRUNER_TYPES.has(match[1])) return undefined;
    const prunerType = match[1];
    const prunerId = unquoteGoString(match[2]);
    const pruner = findNodeById(groupNode, prunerId);
    const prunerHasOwnResult = pruner?.type === "decision" && pruner.data.decision !== undefined;
    const escalatedDecision = decisionNodesIn(groupNode).find(d => escalateTargetOf(d) === prunerId);
    if (escalatedDecision && !prunerHasOwnResult) return { anchorId: prunerId, escalatedDecision };
    if (prunerType === "decision" && pruner?.type === "decision") return { anchorId: prunerId };
    return undefined;
}

function noteEscalationRouteCandidate(context: BuildContext, groupNode: LayoutNode, nodeId: string, dependsOn: string[]): void {
    for (const decisionNode of decisionNodesIn(groupNode)) {
        const escalateTarget = escalateTargetOf(decisionNode);
        if (!escalateTarget || nodeId === escalateTarget) continue;
        if (!dependsOn.includes(decisionNode.data.nodeId!) || !dependsOn.includes(escalateTarget)) continue;
        const routing = escalationRoutingOf(context, decisionNode);
        routing.candidates.push({ nodeId, dependsOn });
        decisionNode.data.escalationRoute = escalationRouteOf(routing);
    }
}

function noteEscalationPrune(context: BuildContext, decisionNode: LayoutNode, prunedId: string): void {
    const routing = escalationRoutingOf(context, decisionNode);
    routing.pruned.push(prunedId);
    decisionNode.data.escalationRoute = escalationRouteOf(routing);
}

function escalationRoutingOf(context: BuildContext, decisionNode: LayoutNode): EscalationRouting {
    context.escalationRouting ??= new Map();
    let routing = context.escalationRouting.get(decisionNode.id);
    if (!routing) {
        routing = { pruned: [], candidates: [] };
        context.escalationRouting.set(decisionNode.id, routing);
    }
    return routing;
}

function escalationRouteOf({ pruned, candidates }: EscalationRouting): string | undefined {
    if (pruned.length === 0) return undefined;
    const waitsOnNoPrunedBranch = (candidate: EscalationRouting["candidates"][number]) => !pruned.includes(candidate.nodeId) && !candidate.dependsOn.some(dep => pruned.includes(dep));
    return candidates.find(waitsOnNoPrunedBranch)?.nodeId;
}

function placeSkippedNode(step: VisualizerStep, context: BuildContext, groupNode: LayoutNode, nodeId: string, metadata: Record<string, unknown> | undefined): void {
    const placement = skippedNodePlacement(context, groupNode, nodeId, metadata);
    const source = pruneSourceOf(groupNode, metadata?.skip_reason);
    if (source) {
        placeUnselectedDecisionTarget(step, context, groupNode, nodeId, placement, source);
        return;
    }

    const placeholder = createSkippedPlaceholder(step, context, nodeId, placement, placement.dependsOn);
    if (!placement.dependsOn) {
        placeInLevelRow(groupNode, placeholder, levelAfterDeepest(context, groupNode), context);
        return;
    }
    const level = levelBelow(context, groupNode, placement.dependsOn);
    dagLevelsOf(context, groupNode.id).set(nodeId, level);
    placeInLevelRow(groupNode, placeholder, level, context);
}

function levelBelow(context: BuildContext, groupNode: LayoutNode, dependsOn: string[]): number {
    const levels = context.dagLevelsByGroup?.get(groupNode.id);
    let level = 0;
    for (const dep of dependsOn) {
        const depLevel = levels?.get(dep) ?? drawnRowOf(context, groupNode, dep);
        if (depLevel !== undefined) level = Math.max(level, depLevel + 1);
    }
    return level;
}

function drawnRowOf(context: BuildContext, groupNode: LayoutNode, nodeId: string): number | undefined {
    const prefix = `${groupNode.id}:level:`;
    for (const [key, block] of context.dagLevelBlocks ?? []) {
        if (key.startsWith(prefix) && block.children.some(child => child.data.nodeId === nodeId)) {
            return Number(key.slice(prefix.length));
        }
    }
    return undefined;
}

function placeUnselectedDecisionTarget(step: VisualizerStep, context: BuildContext, groupNode: LayoutNode, nodeId: string, placement: SkippedNodePlacement, source: PruneSource): void {
    const { anchorId, escalatedDecision } = source;
    if (escalatedDecision) noteEscalationPrune(context, escalatedDecision, nodeId);

    const placeholder = createSkippedPlaceholder(step, context, nodeId, { ...placement, inferredFrom: undefined }, [anchorId]);
    const anchorLevel = context.dagLevelsByGroup?.get(groupNode.id)?.get(anchorId) ?? 0;
    placeInLevelRow(groupNode, placeholder, anchorLevel + 1, context);
}

interface SkippedNodePlacement {
    nodeType?: string;
    dependsOn?: string[];
    agentName?: string;
    toolName?: string;
    inferredFrom?: string;
}

const INFERRED_FROM_DEFINITION = "Position taken from the currently deployed workflow, which may differ from the version this run used";
const INFERRED_FROM_SKIP_REASON = "Position taken from the skip reason";
const POSITION_UNKNOWN = "This run did not record where this node sits, so it is drawn after the last row";

const CASCADE_REASON = /^all dependencies skipped \(parent: (.+)\)$/;

const stringOf = (value: unknown): string | undefined => (typeof value === "string" && value ? value : undefined);
const stringsOf = (value: unknown): string[] | undefined => (Array.isArray(value) && value.every(item => typeof item === "string") ? value : undefined);

function skippedNodePlacement(context: BuildContext, groupNode: LayoutNode, nodeId: string, metadata: Record<string, unknown> | undefined): SkippedNodePlacement {
    const fromEvent: SkippedNodePlacement = {
        nodeType: stringOf(metadata?.node_type),
        dependsOn: stringsOf(metadata?.depends_on),
        agentName: stringOf(metadata?.agent_name),
        toolName: stringOf(metadata?.tool_name),
    };
    if (fromEvent.nodeType && fromEvent.dependsOn) return fromEvent;

    const workflowName = groupNode.data.workflowName;
    const defined = workflowName ? context.workflowDefinitions?.[workflowName]?.find(node => node.id === nodeId) : undefined;
    if (defined) {
        return {
            nodeType: fromEvent.nodeType ?? defined.type,
            dependsOn: fromEvent.dependsOn ?? defined.depends_on ?? [],
            agentName: fromEvent.agentName ?? defined.agent_name ?? defined.workflow_name,
            toolName: fromEvent.toolName ?? defined.tool_name,
            inferredFrom: INFERRED_FROM_DEFINITION,
        };
    }

    if (fromEvent.dependsOn) return fromEvent;
    const cascadeParent = typeof metadata?.skip_reason === "string" ? CASCADE_REASON.exec(metadata.skip_reason)?.[1] : undefined;
    if (cascadeParent) return { ...fromEvent, dependsOn: [cascadeParent], inferredFrom: INFERRED_FROM_SKIP_REASON };
    return { ...fromEvent, inferredFrom: POSITION_UNKNOWN };
}

function createSkippedPlaceholder(step: VisualizerStep, context: BuildContext, nodeId: string, placement: SkippedNodePlacement, dependsOn: string[] | undefined): LayoutNode {
    const isTool = placement.nodeType === "tool";
    return createNode(
        context,
        isTool ? "tool" : "agent",
        {
            label: isTool || !placement.agentName ? nodeId : resolveAgentName(context, placement.agentName),
            description: placement.inferredFrom,
            visualizerStepId: step.id,
            status: "skipped",
            isSkipped: true,
            nodeId,
            dependsOn,
            toolName: isTool ? placement.toolName : undefined,
        },
        step.owningTaskId
    );
}

function levelAfterDeepest(context: BuildContext, groupNode: LayoutNode): number {
    const levels = context.dagLevelsByGroup?.get(groupNode.id);
    return levels && levels.size > 0 ? Math.max(...levels.values()) + 1 : 0;
}

function completeInProgressLLMChildren(context: BuildContext, nodeId: string): void {
    for (const [subTaskId, agentNode] of context.taskToNodeMap.entries()) {
        if (!subTaskId.includes(nodeId) && agentNode.data.nodeId !== nodeId) {
            continue;
        }
        for (const child of agentNode.children) {
            if (child.type === "llm" && child.data.status === "in-progress") {
                child.data.status = "completed";
            }
        }
        return;
    }
}

function findNodeById(root: LayoutNode, nodeId: string): LayoutNode | null {
    for (const child of [...root.children, ...(root.parallelBranches ?? []).flat()]) {
        if (child.data.nodeId === nodeId) return child;
        if (child.type === "group") continue;
        const found = findNodeById(child, nodeId);
        if (found) return found;
    }
    return null;
}

function findAgentForStep(step: VisualizerStep, context: BuildContext): LayoutNode | null {
    if (step.owningTaskId) {
        const node = context.taskToNodeMap.get(step.owningTaskId);
        if (node) return node;
    }

    return context.currentAgentNode;
}

function createNode(context: BuildContext, type: LayoutNode["type"], data: LayoutNode["data"], owningTaskId?: string): LayoutNode {
    const id = `${type}_${context.nodeCounter++}`;

    return {
        id,
        type,
        data,
        x: 0,
        y: 0,
        width: 0,
        height: 0,
        children: [],
        owningTaskId,
    };
}

function calculateLayout(rootNodes: LayoutNode[]): LayoutNode[] {
    let maxWidth = 0;
    for (const node of rootNodes) {
        measureNode(node);
        maxWidth = Math.max(maxWidth, node.width);
    }

    const centerX = maxWidth / 2 + 100;

    let currentY = 50;

    for (let i = 0; i < rootNodes.length; i++) {
        const node = rootNodes[i];
        const nextNode = rootNodes[i + 1];

        node.x = centerX - node.width / 2;
        node.y = currentY;
        positionNode(node);

        let spacing = SPACING.AGENT_VERTICAL;
        if (node.type === "user" || (nextNode && nextNode.type === "user")) {
            spacing = SPACING.VERTICAL;
        }

        currentY = node.y + node.height + spacing;
    }

    return rootNodes;
}

function measureNode(node: LayoutNode): void {
    for (const child of node.children) {
        measureNode(child);
    }

    if (node.parallelBranches) {
        for (const branch of node.parallelBranches) {
            for (const branchNode of branch) {
                measureNode(branchNode);
            }
        }
    }

    switch (node.type) {
        case "agent":
            measureAgentNode(node);
            break;
        case "tool":
            node.width = NODE_WIDTHS.TOOL;
            node.height = NODE_HEIGHTS.TOOL;
            break;
        case "llm":
            node.width = NODE_WIDTHS.LLM;
            node.height = NODE_HEIGHTS.LLM;
            break;
        case "user":
            node.width = NODE_WIDTHS.USER;
            node.height = NODE_HEIGHTS.USER;
            break;
        case "switch":
        case "decision":
            node.width = NODE_WIDTHS.SWITCH;
            node.height = NODE_HEIGHTS.SWITCH;
            break;
        case "loop":
            measureLoopNode(node);
            break;
        case "map":
            measureMapNode(node);
            break;
        case "group":
            measureGroupNode(node);
            break;
        case "parallelBlock":
            measureParallelBlockNode(node);
            break;
    }
}

function measureAgentNode(node: LayoutNode): void {
    let contentWidth = NODE_WIDTHS.MIN_AGENT_CONTENT;
    let contentHeight = 0;

    if (node.data.variant === "pill") {
        node.width = 100;
        node.height = 40;
        return;
    }

    if (node.children.length > 0) {
        for (const child of node.children) {
            contentWidth = Math.max(contentWidth, child.width);
            contentHeight += child.height + SPACING.VERTICAL;
        }
        contentHeight -= SPACING.VERTICAL;
    }

    if (node.parallelBranches && node.parallelBranches.length > 0) {
        if (node.children.length > 0) {
            contentHeight += SPACING.VERTICAL;
        }

        let branchWidth = 0;
        let maxBranchHeight = 0;

        for (const branch of node.parallelBranches) {
            let branchHeight = 0;
            let branchMaxWidth = 0;

            for (const branchNode of branch) {
                branchHeight += branchNode.height + SPACING.VERTICAL;
                branchMaxWidth = Math.max(branchMaxWidth, branchNode.width);
            }

            if (branch.length > 0) {
                branchHeight -= SPACING.VERTICAL;
            }

            branchWidth += branchMaxWidth + SPACING.HORIZONTAL;
            maxBranchHeight = Math.max(maxBranchHeight, branchHeight);
        }

        if (node.parallelBranches.length > 0) {
            branchWidth -= SPACING.HORIZONTAL;
        }

        contentWidth = Math.max(contentWidth, branchWidth);
        contentHeight += maxBranchHeight;
    }

    node.width = contentWidth + SPACING.PADDING * 2;
    node.height = NODE_HEIGHTS.AGENT_HEADER + contentHeight + SPACING.PADDING;
}

function measureLoopNode(node: LayoutNode): void {
    if (node.children.length === 0) {
        node.width = NODE_WIDTHS.LOOP;
        node.height = NODE_HEIGHTS.LOOP;
        return;
    }

    let contentWidth = 200;
    let contentHeight = 0;

    const iterationLabelHeight = 16;

    for (const child of node.children) {
        contentWidth = Math.max(contentWidth, child.width);
        contentHeight += iterationLabelHeight + child.height + SPACING.VERTICAL;
    }

    if (node.children.length > 0) {
        contentHeight -= SPACING.VERTICAL;
    }

    const loopPadding = 16;
    const topLabelOffset = -4;
    node.width = contentWidth + loopPadding * 2;
    node.height = contentHeight + loopPadding + topLabelOffset + loopPadding;
}

function measureMapNode(node: LayoutNode): void {
    const branches = new Map<number, LayoutNode[]>();
    for (const child of node.children) {
        const iterationIndex = child.data.iterationIndex ?? 0;
        if (!branches.has(iterationIndex)) {
            branches.set(iterationIndex, []);
        }
        branches.get(iterationIndex)!.push(child);
    }

    if (branches.size === 0) {
        node.width = NODE_WIDTHS.MAP;
        node.height = NODE_HEIGHTS.MAP;
        return;
    }

    let totalWidth = 0;
    let maxBranchHeight = 0;

    const iterationLabelHeight = 20;

    const sortedBranches = Array.from(branches.entries()).sort((a, b) => a[0] - b[0]);

    for (const [, branchChildren] of sortedBranches) {
        let branchWidth = 0;
        let branchHeight = iterationLabelHeight;

        for (const child of branchChildren) {
            branchWidth = Math.max(branchWidth, child.width);
            branchHeight += child.height + SPACING.VERTICAL;
        }

        if (branchChildren.length > 0) {
            branchHeight -= SPACING.VERTICAL;
        }

        totalWidth += branchWidth + SPACING.HORIZONTAL;
        maxBranchHeight = Math.max(maxBranchHeight, branchHeight);
    }

    if (sortedBranches.length > 0) {
        totalWidth -= SPACING.HORIZONTAL;
    }

    const containerPadding = 16;
    const topLabelOffset = -4;
    node.width = totalWidth + containerPadding * 2;
    node.height = maxBranchHeight + containerPadding + topLabelOffset + containerPadding;
}

function measureGroupNode(node: LayoutNode): void {
    let contentWidth = 200;
    let contentHeight = 0;

    for (const child of node.children) {
        contentWidth = Math.max(contentWidth, child.width);
        contentHeight += child.height + SPACING.VERTICAL;
    }

    if (node.children.length > 0) {
        contentHeight -= SPACING.VERTICAL;
    }

    const groupPadding = 24;
    node.width = contentWidth + groupPadding * 2;
    node.height = contentHeight + groupPadding * 2;
}

function measureParallelBlockNode(node: LayoutNode): void {
    const branches = new Map<number, LayoutNode[]>();
    for (const child of node.children) {
        const branchIdx = child.data.iterationIndex ?? 0;
        if (!branches.has(branchIdx)) {
            branches.set(branchIdx, []);
        }
        branches.get(branchIdx)!.push(child);
    }

    if (branches.size <= 1 && node.children.every(c => c.data.iterationIndex === undefined)) {
        let totalWidth = 0;
        let maxHeight = 0;

        for (const child of node.children) {
            totalWidth += child.width + SPACING.HORIZONTAL;
            maxHeight = Math.max(maxHeight, child.height);
        }

        if (node.children.length > 0) {
            totalWidth -= SPACING.HORIZONTAL;
        }

        const blockPadding = 16;
        node.width = totalWidth + blockPadding * 2;
        node.height = maxHeight + blockPadding * 2;
        return;
    }

    let totalWidth = 0;
    let maxBranchHeight = 0;

    const sortedBranches = Array.from(branches.entries()).sort((a, b) => a[0] - b[0]);
    for (const [, branchChildren] of sortedBranches) {
        let branchWidth = 0;
        let branchHeight = 0;

        for (const child of branchChildren) {
            branchWidth = Math.max(branchWidth, child.width);
            branchHeight += child.height + SPACING.VERTICAL;
        }

        if (branchChildren.length > 0) {
            branchHeight -= SPACING.VERTICAL;
        }

        totalWidth += branchWidth + SPACING.HORIZONTAL;
        maxBranchHeight = Math.max(maxBranchHeight, branchHeight);
    }

    if (sortedBranches.length > 0) {
        totalWidth -= SPACING.HORIZONTAL;
    }

    const blockPadding = 16;
    node.width = totalWidth + blockPadding * 2;
    node.height = maxBranchHeight + blockPadding * 2;
}

function positionNode(node: LayoutNode): void {
    if (node.type === "agent" && node.data.variant !== "pill") {
        let currentY = node.y + NODE_HEIGHTS.AGENT_HEADER + SPACING.PADDING;
        const centerX = node.x + node.width / 2;

        for (const child of node.children) {
            child.x = centerX - child.width / 2;
            child.y = currentY;
            positionNode(child);
            currentY += child.height + SPACING.VERTICAL;
        }

        if (node.parallelBranches) {
            let branchX = node.x + SPACING.PADDING;

            for (const branch of node.parallelBranches) {
                let branchMaxWidth = 0;
                let branchY = currentY;

                for (const branchNode of branch) {
                    branchNode.x = branchX;
                    branchNode.y = branchY;
                    positionNode(branchNode);
                    branchY += branchNode.height + SPACING.VERTICAL;
                    branchMaxWidth = Math.max(branchMaxWidth, branchNode.width);
                }

                branchX += branchMaxWidth + SPACING.HORIZONTAL;
            }
        }
    } else if (node.type === "group") {
        let currentY = node.y + SPACING.PADDING + 30;
        const centerX = node.x + node.width / 2;

        for (const child of node.children) {
            child.x = centerX - child.width / 2;
            child.y = currentY;
            positionNode(child);
            currentY += child.height + SPACING.VERTICAL;
        }
    } else if (node.type === "parallelBlock") {
        const branches = new Map<number, LayoutNode[]>();
        for (const child of node.children) {
            const branchIdx = child.data.iterationIndex ?? 0;
            if (!branches.has(branchIdx)) {
                branches.set(branchIdx, []);
            }
            branches.get(branchIdx)!.push(child);
        }

        const blockPadding = 16;

        if (branches.size <= 1 && node.children.every(c => c.data.iterationIndex === undefined)) {
            let currentX = node.x + blockPadding;
            for (const child of node.children) {
                child.x = currentX;
                child.y = node.y + blockPadding;
                positionNode(child);
                currentX += child.width + SPACING.HORIZONTAL;
            }
        } else {
            const sortedBranches = Array.from(branches.entries()).sort((a, b) => a[0] - b[0]);
            let currentX = node.x + blockPadding;

            for (const [, branchChildren] of sortedBranches) {
                let currentY = node.y + blockPadding;
                let branchMaxWidth = 0;

                for (const child of branchChildren) {
                    child.x = currentX;
                    child.y = currentY;
                    positionNode(child);
                    currentY += child.height + SPACING.VERTICAL;
                    branchMaxWidth = Math.max(branchMaxWidth, child.width);
                }

                currentX += branchMaxWidth + SPACING.HORIZONTAL;
            }
        }
    } else if (node.type === "loop" && node.children.length > 0) {
        const loopPadding = 16;
        const topLabelOffset = -4;
        const iterationLabelHeight = 16;
        let currentY = node.y + loopPadding + topLabelOffset;
        const centerX = node.x + node.width / 2;

        for (const child of node.children) {
            currentY += iterationLabelHeight;
            child.x = centerX - child.width / 2;
            child.y = currentY;
            positionNode(child);
            currentY += child.height + SPACING.VERTICAL;
        }
    } else if (node.type === "map" && node.children.length > 0) {
        const branches = new Map<number, LayoutNode[]>();
        for (const child of node.children) {
            const iterationIndex = child.data.iterationIndex ?? 0;
            if (!branches.has(iterationIndex)) {
                branches.set(iterationIndex, []);
            }
            branches.get(iterationIndex)!.push(child);
        }

        const containerPadding = 16;
        const topLabelOffset = -4;
        const iterationLabelHeight = 20;
        let currentX = node.x + containerPadding;
        const startY = node.y + containerPadding + topLabelOffset;

        const sortedBranches = Array.from(branches.entries()).sort((a, b) => a[0] - b[0]);

        for (const [, branchChildren] of sortedBranches) {
            let branchMaxWidth = 0;
            let currentY = startY + iterationLabelHeight;

            for (const child of branchChildren) {
                child.x = currentX;
                child.y = currentY;
                positionNode(child);
                currentY += child.height + SPACING.VERTICAL;
                branchMaxWidth = Math.max(branchMaxWidth, child.width);
            }

            currentX += branchMaxWidth + SPACING.HORIZONTAL;
        }
    }
}

function calculateEdges(nodes: LayoutNode[]): Edge[] {
    const edges: Edge[] = [];
    const flatNodes = flattenNodes(nodes);

    for (let i = 0; i < flatNodes.length - 1; i++) {
        const source = flatNodes[i];
        const target = flatNodes[i + 1];

        if (shouldConnectNodes(source, target)) {
            edges.push({
                id: `edge_${source.id}_${target.id}`,
                source: source.id,
                target: target.id,
                sourceX: source.x + source.width / 2,
                sourceY: source.y + source.height,
                targetX: target.x + target.width / 2,
                targetY: target.y,
            });
        }
    }

    return edges;
}

function flattenNodes(nodes: LayoutNode[]): LayoutNode[] {
    const result: LayoutNode[] = [];

    function traverse(node: LayoutNode) {
        result.push(node);
        for (const child of node.children) {
            traverse(child);
        }
        if (node.parallelBranches) {
            for (const branch of node.parallelBranches) {
                for (const branchNode of branch) {
                    traverse(branchNode);
                }
            }
        }
    }

    for (const node of nodes) {
        traverse(node);
    }

    return result;
}

function shouldConnectNodes(source: LayoutNode, target: LayoutNode): boolean {
    if (source.type === "user" && source.data.isTopNode && target.type === "agent") {
        return true;
    }

    if (source.type === "agent" && target.type === "user" && target.data.isBottomNode) {
        return true;
    }

    if (source.type === "agent" && target.type === "agent") {
        return true;
    }

    return false;
}

function calculateCanvasSize(nodes: LayoutNode[]): { totalWidth: number; totalHeight: number } {
    let maxX = 0;
    let maxY = 0;

    const flatNodes = flattenNodes(nodes);

    for (const node of flatNodes) {
        maxX = Math.max(maxX, node.x + node.width);
        maxY = Math.max(maxY, node.y + node.height);
    }

    return {
        totalWidth: maxX + 100,
        totalHeight: maxY + 100,
    };
}

export function detectCollapsibleDetail(nodes: readonly LayoutNode[], depth = 0): boolean {
    for (const node of nodes) {
        if (node.type === "agent" && depth > 0) return true;
        if (node.type === "group") return true;
        if (node.parallelBranches && node.parallelBranches.length > 0) return true;
        if (detectCollapsibleDetail(node.children, depth + 1)) return true;
        for (const branch of node.parallelBranches ?? []) {
            if (detectCollapsibleDetail(branch, depth + 1)) return true;
        }
    }
    return false;
}
