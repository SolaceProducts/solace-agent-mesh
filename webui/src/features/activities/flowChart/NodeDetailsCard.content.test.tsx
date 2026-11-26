// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type React from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types/chat";

import type { NodeDetails } from "@/features/activities/flowChart/utils/nodeDetailsHelper";

import type { VisualizerStep } from "@/types";

async function loadCard({ chatArtifacts = [], setPreviewArtifact = vi.fn() }: { chatArtifacts?: ArtifactInfo[]; setPreviewArtifact?: () => void } = {}) {
    vi.resetModules();

    vi.doMock("@/providers/contexts/ChatContext", () => ({
        useChatContext: () => ({
            setPreviewArtifact,
            setActiveSidePanelTab: vi.fn(),
            setIsSidePanelCollapsed: vi.fn(),
            navigateArtifactVersion: vi.fn(),
            sessionId: "test-session",
        }),
    }));

    vi.doMock("@/api/artifacts", () => ({
        useFilteredChatArtifacts: () => ({ artifacts: chatArtifacts, allArtifacts: [], isLoading: false, refetch: () => Promise.resolve(), showWorkingArtifacts: false, toggleShowWorkingArtifacts: () => {}, workingArtifactCount: 0 }),
    }));

    const mod = await import("@/features/activities/flowChart/NodeDetailsCard");
    return mod.default;
}

const step = (over: Pick<VisualizerStep, "type" | "data">): VisualizerStep => ({
    id: "step-1",
    timestamp: new Date().toISOString(),
    title: "Step",
    rawEventIds: [],
    nestingLevel: 0,
    owningTaskId: "task-1",
    ...over,
});

const requestOnly = (requestStep: VisualizerStep): NodeDetails => ({ nodeType: "tool", label: "node", requestStep });

describe("NodeDetailsCard step content", () => {
    let NodeDetailsCard: React.ComponentType<{ nodeDetails: NodeDetails }>;

    beforeEach(async () => {
        NodeDetailsCard = await loadCard();
    });

    const renderNode = (nodeStep: VisualizerStep) => render(<NodeDetailsCard nodeDetails={requestOnly(nodeStep)} />);

    test("a user request node renders its text", () => {
        renderNode(step({ type: "USER_REQUEST", data: { text: "What is the refund policy?" } }));
        expect(screen.getByText("User Input")).toBeInTheDocument();
        expect(screen.getByText("What is the refund policy?")).toBeInTheDocument();
    });

    test("a user request node with no text renders only its heading", () => {
        renderNode(step({ type: "USER_REQUEST", data: {} }));
        expect(screen.getByText("User Input")).toBeInTheDocument();
        expect(screen.queryByText(/hidden/i)).not.toBeInTheDocument();
    });

    test("an agent response node renders its text", () => {
        renderNode(step({ type: "AGENT_RESPONSE_TEXT", data: { text: "Refunds take five days" } }));
        expect(screen.getByText("Agent Response")).toBeInTheDocument();
        expect(screen.getByText("Refunds take five days")).toBeInTheDocument();
    });

    test("a workflow agent request node renders its instruction", () => {
        renderNode(step({ type: "WORKFLOW_AGENT_REQUEST", data: { workflowAgentRequest: { agentName: "triage-agent", instruction: "Classify this ticket" } } }));
        expect(screen.getByText(/Instruction:/)).toBeInTheDocument();
        expect(screen.getByText("Classify this ticket")).toBeInTheDocument();
    });

    test("an LLM call node renders its prompt", () => {
        renderNode(step({ type: "AGENT_LLM_CALL", data: { llmCall: { modelName: "gpt-4o", promptPreview: "Draft the summary" } } }));
        expect(screen.getByText(/Prompt:/)).toBeInTheDocument();
        expect(screen.getByText("Draft the summary")).toBeInTheDocument();
    });

    test("an LLM response node renders its response body", () => {
        renderNode(step({ type: "AGENT_LLM_RESPONSE_TO_AGENT", data: { llmResponseToAgent: { modelName: "gpt-4o", responsePreview: "Four items shipped", response: "Four items shipped" } } }));
        expect(screen.getByText("Four items shipped")).toBeInTheDocument();
    });

    test("a tool decision node lists the tools to invoke", () => {
        renderNode(step({ type: "AGENT_LLM_RESPONSE_TOOL_DECISION", data: { toolDecision: { isParallel: false, decisions: [{ functionCallId: "c1", toolName: "web_search", toolArguments: {}, isPeerDelegation: false }] } } }));
        expect(screen.getByText(/Tools to invoke:/)).toBeInTheDocument();
        expect(screen.getByText("web_search")).toBeInTheDocument();
    });

    test("a tool invocation node renders its arguments", () => {
        renderNode(step({ type: "AGENT_TOOL_INVOCATION_START", data: { toolInvocationStart: { functionCallId: "fc-1", toolName: "web_search", toolArguments: { query: "solace" } } } }));
        expect(screen.getByText(/Arguments:/)).toBeInTheDocument();
        expect(screen.getByText("query")).toBeInTheDocument();
    });

    test("a tool result node renders its result", () => {
        renderNode(step({ type: "AGENT_TOOL_EXECUTION_RESULT", data: { toolResult: { toolName: "web_search", resultData: "seven hits" } } }));
        expect(screen.getByText(/Result:/)).toBeInTheDocument();
        expect(screen.getByText("seven hits")).toBeInTheDocument();
    });

    test("a workflow start node renders its input", () => {
        renderNode(step({ type: "WORKFLOW_EXECUTION_START", data: { workflowExecutionStart: { workflowName: "triage", executionId: "ex-1", workflowInput: { ticket: "T-1" } } } }));
        expect(screen.getByText(/Input:/)).toBeInTheDocument();
        expect(screen.getByText("ticket")).toBeInTheDocument();
    });

    test("a workflow result node renders its output", () => {
        renderNode(step({ type: "WORKFLOW_EXECUTION_RESULT", data: { workflowExecutionResult: { status: "success", workflowOutput: { verdict: "ok" } } } }));
        expect(screen.getByText(/Output:/)).toBeInTheDocument();
        expect(screen.getByText("verdict")).toBeInTheDocument();
    });
});

describe("NodeDetailsCard error text colour", () => {
    const failedNodeStep = step({ type: "WORKFLOW_NODE_EXECUTION_RESULT", data: { workflowNodeExecutionResult: { nodeId: "n1", status: "error", errorMessage: "Node blew up" } } });
    const failedWorkflowStep = step({ type: "WORKFLOW_EXECUTION_RESULT", data: { workflowExecutionResult: { status: "error", errorMessage: "Workflow blew up" } } });

    test("a failed workflow node's error text and status use the 100 red shade", async () => {
        const NodeDetailsCard = await loadCard();
        render(<NodeDetailsCard nodeDetails={requestOnly(failedNodeStep)} />);

        expect(screen.getByText("Node blew up", { exact: false })).toHaveClass("text-(--error-w100)");
        expect(screen.getByText("error")).toHaveClass("text-(--error-w100)");
    });

    test("a failed workflow's error text and status use the 100 red shade", async () => {
        const NodeDetailsCard = await loadCard();
        render(<NodeDetailsCard nodeDetails={requestOnly(failedWorkflowStep)} />);

        expect(screen.getByText("Workflow blew up", { exact: false })).toHaveClass("text-(--error-w100)");
        expect(screen.getByText("error")).toHaveClass("text-(--error-w100)");
    });
});

describe("NodeDetailsCard created artifacts", () => {
    const createdArtifact = { filename: "report.md", version: 2, mimeType: "text/markdown" };
    const artifactDetails = (): NodeDetails => ({
        nodeType: "tool",
        label: "node",
        resultStep: step({ type: "AGENT_TOOL_EXECUTION_RESULT", data: { toolResult: { toolName: "lookup_limit", resultData: "seven hits" } } }),
        createdArtifacts: [createdArtifact],
    });

    test("renders a created artifact's filename as a button that opens it", async () => {
        const setPreviewArtifact = vi.fn();
        const chatArtifact = { filename: "report.md", version: 2, mime_type: "text/markdown" } as unknown as ArtifactInfo;
        const onClose = vi.fn();
        const NodeDetailsCard = await loadCard({ chatArtifacts: [chatArtifact], setPreviewArtifact });

        render(<NodeDetailsCard nodeDetails={artifactDetails()} onClose={onClose} />);
        await userEvent.click(screen.getByRole("button", { name: "report.md" }));

        expect(setPreviewArtifact).toHaveBeenCalledWith(chatArtifact);
        expect(onClose).toHaveBeenCalled();
    });
});
