// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { VisualizerStep } from "@/types";

import type { LayoutNode } from "./types";

export interface CreatedArtifact {
    filename: string;
    version?: number;
    mimeType?: string;
    description?: string;
}

export interface NodeDetails {
    nodeType: LayoutNode["type"];
    label: string;
    description?: string;
    requestStep?: VisualizerStep;
    resultStep?: VisualizerStep;
    outputArtifactStep?: VisualizerStep;
    relatedSteps?: VisualizerStep[];
    createdArtifacts?: CreatedArtifact[];
}

export function findNodeDetails(node: LayoutNode, allSteps: VisualizerStep[]): NodeDetails {
    const visualizerStepId = node.data.visualizerStepId;

    if (!visualizerStepId) {
        return {
            nodeType: node.type,
            label: node.data.label,
            description: node.data.description,
        };
    }

    const primaryStep = allSteps.find(s => s.id === visualizerStepId);

    if (!primaryStep) {
        return {
            nodeType: node.type,
            label: node.data.label,
            description: node.data.description,
        };
    }

    if (node.data.isSkipped) {
        return {
            nodeType: node.type,
            label: node.data.label,
            description: node.data.description,
            resultStep: primaryStep,
        };
    }

    switch (node.type) {
        case "user":
            return findUserNodeDetails(node, primaryStep, allSteps);
        case "agent":
            return findAgentNodeDetails(node, primaryStep, allSteps);
        case "llm":
            return findLLMNodeDetails(node, primaryStep, allSteps);
        case "tool":
            return findToolNodeDetails(node, primaryStep, allSteps);
        case "switch":
        case "decision":
            return findBranchingNodeDetails(node, primaryStep, allSteps);
        case "loop":
            return findLoopNodeDetails(node, primaryStep, allSteps);
        case "group":
            return findWorkflowGroupDetails(node, primaryStep, allSteps);
        default:
            return {
                nodeType: node.type,
                label: node.data.label,
                description: node.data.description,
                requestStep: primaryStep,
            };
    }
}

function findUserNodeDetails(node: LayoutNode, primaryStep: VisualizerStep, allSteps: VisualizerStep[]): NodeDetails {
    if (node.data.isTopNode) {
        return {
            nodeType: "user",
            label: "User Input",
            requestStep: primaryStep,
        };
    }

    if (node.data.isBottomNode) {
        const topLevelResponses = allSteps.filter(s => s.type === "AGENT_RESPONSE_TEXT" && s.nestingLevel === 0);

        if (topLevelResponses.length > 1) {
            const combinedText = topLevelResponses.map(s => s.data.text || "").join("");
            const lastResponse = topLevelResponses[topLevelResponses.length - 1];

            const syntheticStep: VisualizerStep = {
                ...lastResponse,
                id: `synthetic-combined-response-${lastResponse.id}`,
                data: {
                    ...lastResponse.data,
                    text: combinedText,
                },
            };

            return {
                nodeType: "user",
                label: "Final Output",
                resultStep: syntheticStep,
            };
        }

        const finalResponse = topLevelResponses[0];

        return {
            nodeType: "user",
            label: "Final Output",
            resultStep: finalResponse,
        };
    }

    return {
        nodeType: "user",
        label: node.data.label,
        requestStep: primaryStep,
    };
}

function findAgentNodeDetails(node: LayoutNode, primaryStep: VisualizerStep, allSteps: VisualizerStep[]): NodeDetails {
    const isWorkflowAgent = primaryStep.type === "WORKFLOW_NODE_EXECUTION_START";
    const workflowNodeData = isWorkflowAgent ? primaryStep.data.workflowNodeExecutionStart : undefined;
    const subTaskId = workflowNodeData?.subTaskId;

    const agentTaskId = subTaskId || node.owningTaskId;

    let requestStep: VisualizerStep | undefined;

    const userRequest = allSteps.find(s => s.owningTaskId === agentTaskId && s.type === "USER_REQUEST");

    if (userRequest) {
        requestStep = userRequest;
    } else if (isWorkflowAgent) {
        const workflowAgentRequest = allSteps.find(s => s.owningTaskId === agentTaskId && s.type === "WORKFLOW_AGENT_REQUEST");
        requestStep = workflowAgentRequest || primaryStep;
    } else {
        const toolInvocation = allSteps.find(s => s.owningTaskId === agentTaskId && s.type === "AGENT_TOOL_INVOCATION_START");

        if (toolInvocation && toolInvocation.parentTaskId) {
            const parentUserRequest = allSteps.find(s => s.owningTaskId === toolInvocation.parentTaskId && s.type === "USER_REQUEST");
            requestStep = parentUserRequest || toolInvocation;
        } else {
            requestStep = primaryStep;
        }
    }

    const responseStep = allSteps.findLast(s => s.owningTaskId === agentTaskId && s.type === "AGENT_RESPONSE_TEXT");

    let outputArtifactStep: VisualizerStep | undefined;
    if (isWorkflowAgent && workflowNodeData?.nodeId) {
        const workflowExecutionId = primaryStep.owningTaskId;
        outputArtifactStep = allSteps.find(s => s.type === "WORKFLOW_NODE_EXECUTION_RESULT" && s.owningTaskId === workflowExecutionId && s.data.workflowNodeExecutionResult?.nodeId === workflowNodeData.nodeId);
    }

    const relatedSteps = allSteps.filter(s => s.owningTaskId === agentTaskId);

    return {
        nodeType: "agent" as const,
        label: node.data.label,
        description: node.data.description,
        requestStep,
        resultStep: responseStep,
        outputArtifactStep,
        relatedSteps,
    };
}

function findLLMNodeDetails(node: LayoutNode, primaryStep: VisualizerStep, allSteps: VisualizerStep[]): NodeDetails {
    let requestStep: VisualizerStep | undefined;
    let resultStep: VisualizerStep | undefined;

    if (primaryStep.type === "AGENT_LLM_CALL") {
        requestStep = primaryStep;

        const owningTaskId = requestStep.owningTaskId;
        const requestIndex = allSteps.indexOf(requestStep);

        resultStep = allSteps.slice(requestIndex + 1).find(s => s.owningTaskId === owningTaskId && (s.type === "AGENT_LLM_RESPONSE_TO_AGENT" || s.type === "AGENT_LLM_RESPONSE_TOOL_DECISION"));
    } else if (primaryStep.type === "AGENT_LLM_RESPONSE_TOOL_DECISION" || primaryStep.type === "AGENT_LLM_RESPONSE_TO_AGENT") {
        const owningTaskId = primaryStep.owningTaskId;
        const responseIndex = allSteps.indexOf(primaryStep);

        for (let i = responseIndex - 1; i >= 0; i--) {
            const s = allSteps[i];
            if (s.owningTaskId === owningTaskId && s.type === "AGENT_LLM_CALL") {
                requestStep = s;
                break;
            }
        }

        resultStep = primaryStep;
    }

    return {
        nodeType: "llm",
        label: node.data.label,
        description: node.data.description,
        requestStep,
        resultStep,
    };
}

function findToolNodeDetails(node: LayoutNode, primaryStep: VisualizerStep, allSteps: VisualizerStep[]): NodeDetails {
    if (primaryStep.type === "WORKFLOW_NODE_EXECUTION_START") {
        const nodeId = primaryStep.data.workflowNodeExecutionStart?.nodeId;
        const owningTaskId = primaryStep.owningTaskId;
        const startIndex = allSteps.indexOf(primaryStep);
        const resultStep = nodeId ? allSteps.slice(startIndex + 1).find(s => s.type === "WORKFLOW_NODE_EXECUTION_RESULT" && s.owningTaskId === owningTaskId && s.data.workflowNodeExecutionResult?.nodeId === nodeId) : undefined;

        return {
            nodeType: "tool",
            label: node.data.label,
            description: node.data.description,
            requestStep: primaryStep,
            resultStep,
            outputArtifactStep: resultStep,
            createdArtifacts: node.data.createdArtifacts,
        };
    }

    const requestStep = primaryStep.type === "AGENT_TOOL_INVOCATION_START" ? primaryStep : undefined;

    let resultStep: VisualizerStep | undefined;

    const functionCallId = requestStep?.data.toolInvocationStart?.functionCallId || requestStep?.functionCallId;

    if (functionCallId) {
        resultStep = allSteps.findLast(s => s.type === "AGENT_TOOL_EXECUTION_RESULT" && s.data.toolResult?.functionCallId === functionCallId);
    }

    const createdArtifacts = node.data.createdArtifacts;

    return {
        nodeType: "tool",
        label: node.data.label,
        description: node.data.description,
        requestStep,
        resultStep,
        createdArtifacts,
    };
}

function findBranchingNodeDetails(node: LayoutNode, primaryStep: VisualizerStep, allSteps: VisualizerStep[]): NodeDetails {
    const requestStep = primaryStep.type === "WORKFLOW_NODE_EXECUTION_START" ? primaryStep : undefined;

    let resultStep: VisualizerStep | undefined;

    if (requestStep?.data.workflowNodeExecutionStart) {
        const nodeId = requestStep.data.workflowNodeExecutionStart.nodeId;
        const owningTaskId = requestStep.owningTaskId;

        resultStep = allSteps.find(s => s.type === "WORKFLOW_NODE_EXECUTION_RESULT" && s.owningTaskId === owningTaskId && s.data.workflowNodeExecutionResult?.nodeId === nodeId);
    }

    return {
        nodeType: node.type,
        label: node.data.label,
        description: node.data.description,
        requestStep,
        resultStep,
    };
}

function findLoopNodeDetails(node: LayoutNode, primaryStep: VisualizerStep, allSteps: VisualizerStep[]): NodeDetails {
    const requestStep = primaryStep.type === "WORKFLOW_NODE_EXECUTION_START" ? primaryStep : undefined;

    let resultStep: VisualizerStep | undefined;

    if (requestStep?.data.workflowNodeExecutionStart) {
        const nodeId = requestStep.data.workflowNodeExecutionStart.nodeId;
        const owningTaskId = requestStep.owningTaskId;

        resultStep = allSteps.find(s => s.type === "WORKFLOW_NODE_EXECUTION_RESULT" && s.owningTaskId === owningTaskId && s.data.workflowNodeExecutionResult?.nodeId === nodeId);
    }

    const relatedSteps = requestStep
        ? allSteps.filter(s => s.owningTaskId === requestStep.owningTaskId && s.type === "WORKFLOW_NODE_EXECUTION_START" && s.data.workflowNodeExecutionStart?.parentNodeId === requestStep.data.workflowNodeExecutionStart?.nodeId)
        : undefined;

    return {
        nodeType: "loop",
        label: node.data.label,
        description: node.data.description,
        requestStep,
        resultStep,
        relatedSteps,
    };
}

function findWorkflowGroupDetails(node: LayoutNode, primaryStep: VisualizerStep, allSteps: VisualizerStep[]): NodeDetails {
    const requestStep = primaryStep.type === "WORKFLOW_EXECUTION_START" ? primaryStep : undefined;

    let resultStep: VisualizerStep | undefined;

    if (requestStep?.data.workflowExecutionStart) {
        const executionId = requestStep.data.workflowExecutionStart.executionId;

        resultStep = allSteps.find(s => s.type === "WORKFLOW_EXECUTION_RESULT" && s.owningTaskId === executionId);
    }

    const relatedSteps = allSteps.filter(s => s.owningTaskId === requestStep?.data.workflowExecutionStart?.executionId && (s.type.startsWith("WORKFLOW_NODE_") || s.type === "WORKFLOW_MAP_PROGRESS"));

    return {
        nodeType: "group",
        label: node.data.label,
        description: node.data.description,
        requestStep,
        resultStep,
        relatedSteps,
    };
}
