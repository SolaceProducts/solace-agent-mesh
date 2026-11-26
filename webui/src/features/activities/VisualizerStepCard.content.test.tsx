// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { VisualizerStep } from "@/types";

import { VisualizerStepCard } from "./VisualizerStepCard";

const step = (over: Pick<VisualizerStep, "type" | "data">): VisualizerStep => ({
    id: "s-1",
    timestamp: new Date().toISOString(),
    title: "Step",
    rawEventIds: [],
    nestingLevel: 0,
    owningTaskId: "task-1",
    ...over,
});

function renderCard(visualizerStep: VisualizerStep) {
    return render(
        <MemoryRouter>
            <StoryProvider shouldIsolateQueryClient chatContextValues={{ sessionId: "test" }}>
                <VisualizerStepCard step={visualizerStep} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("VisualizerStepCard error text colour", () => {
    test("a failed task's error message uses the 100 red shade", () => {
        renderCard(step({ type: "TASK_FAILED", data: { errorDetails: { message: "Task blew up" } } }));
        expect(screen.getByText("Task blew up", { exact: false }).closest("div")).toHaveClass("text-(--error-w100)");
    });

    test("a failed workflow node's error message uses the 100 red shade", () => {
        renderCard(step({ type: "WORKFLOW_NODE_EXECUTION_RESULT", data: { workflowNodeExecutionResult: { nodeId: "n1", status: "error", errorMessage: "Node blew up" } } }));
        expect(screen.getByText("Node blew up", { exact: false })).toHaveClass("text-(--error-w100)");
    });

    test("a failed workflow's error message uses the 100 red shade", () => {
        renderCard(step({ type: "WORKFLOW_EXECUTION_RESULT", data: { workflowExecutionResult: { status: "error", errorMessage: "Workflow blew up" } } }));
        expect(screen.getByText("Workflow blew up", { exact: false })).toHaveClass("text-(--error-w100)");
    });
});

describe("VisualizerStepCard step content", () => {
    test("an LLM call step renders its prompt preview", () => {
        renderCard(step({ type: "AGENT_LLM_CALL", data: { llmCall: { modelName: "gpt-4o", promptPreview: "Summarize the quarterly report" } } }));
        expect(screen.getByText("Summarize the quarterly report")).toBeInTheDocument();
        expect(screen.getByText(/Prompt Preview:/)).toBeInTheDocument();
    });

    test("a tool invocation step renders its arguments", () => {
        renderCard(step({ type: "AGENT_TOOL_INVOCATION_START", data: { toolInvocationStart: { functionCallId: "fc-1", toolName: "web_search", toolArguments: { query: "solace agent mesh" } } } }));
        expect(screen.getByText(/Arguments:/)).toBeInTheDocument();
        expect(screen.getByText(/query/)).toBeInTheDocument();
        expect(screen.getByText(/solace agent mesh/)).toBeInTheDocument();
    });

    test("a load_skill invocation shows no separate Skill line", () => {
        renderCard(step({ type: "AGENT_TOOL_INVOCATION_START", data: { toolInvocationStart: { functionCallId: "fc-1", toolName: "load_skill", toolArguments: { name: "sam-operate" } } } }));
        expect(screen.queryByText(/^Skill:$/)).not.toBeInTheDocument();
        expect(screen.getByText(/Arguments:/)).toBeInTheDocument();
    });

    test("a tool result step renders its result data", () => {
        renderCard(step({ type: "AGENT_TOOL_EXECUTION_RESULT", data: { toolResult: { toolName: "calculator", resultData: "forty-two" } } }));
        expect(screen.getByText(/Result:/)).toBeInTheDocument();
        expect(screen.getByText("forty-two")).toBeInTheDocument();
    });

    test("a workflow start step renders its input", () => {
        renderCard(step({ type: "WORKFLOW_EXECUTION_START", data: { workflowExecutionStart: { workflowName: "triage", executionId: "ex-1", workflowInput: { ticket: "T-1" } } } }));
        expect(screen.getByText(/Input:/)).toBeInTheDocument();
        expect(screen.getByText(/ticket/)).toBeInTheDocument();
    });

    test("a workflow start step with no input renders no Input box", () => {
        renderCard(step({ type: "WORKFLOW_EXECUTION_START", data: { workflowExecutionStart: { workflowName: "triage", executionId: "ex-1" } } }));
        expect(screen.getByText("triage")).toBeInTheDocument();
        expect(screen.queryByText(/Input:/)).not.toBeInTheDocument();
    });

    test("a workflow result step renders its output", () => {
        renderCard(step({ type: "WORKFLOW_EXECUTION_RESULT", data: { workflowExecutionResult: { status: "success", workflowOutput: { verdict: "ok" } } } }));
        expect(screen.getByText(/Output:/)).toBeInTheDocument();
        expect(screen.getByText(/verdict/)).toBeInTheDocument();
    });

    test("a workflow result step with no output renders no Output box", () => {
        renderCard(step({ type: "WORKFLOW_EXECUTION_RESULT", data: { workflowExecutionResult: { status: "failure" } } }));
        expect(screen.getByText("failure")).toBeInTheDocument();
        expect(screen.queryByText(/Output:/)).not.toBeInTheDocument();
    });

    test("a step with text renders it", () => {
        renderCard(step({ type: "AGENT_RESPONSE_TEXT", data: { text: "Here is the answer" } }));
        expect(screen.getByText("Here is the answer")).toBeInTheDocument();
    });

    test("a completed task with no final message renders the success line", () => {
        renderCard(step({ type: "TASK_COMPLETED", data: {} }));
        expect(screen.getByText("Task completed successfully.")).toBeInTheDocument();
    });

    test("a completed task with a final message renders it instead of the success line", () => {
        renderCard(step({ type: "TASK_COMPLETED", data: { finalMessage: "All done" } }));
        expect(screen.getByText("All done")).toBeInTheDocument();
        expect(screen.queryByText("Task completed successfully.")).not.toBeInTheDocument();
    });

    test("a web search tool result with images renders the image grid, not the generic result body", () => {
        const resultData = JSON.stringify({ result: JSON.stringify({ images: [{ imageUrl: "https://example.test/mesh.png", title: "Mesh diagram", link: "https://example.test/mesh" }] }) });
        renderCard(step({ type: "AGENT_TOOL_EXECUTION_RESULT", data: { toolResult: { toolName: "web_search", resultData } } }));
        expect(screen.getByText(/Image Results:/)).toBeInTheDocument();
        expect(screen.getByAltText("Mesh diagram")).toBeInTheDocument();
        expect(screen.getByText("Show full result data")).toBeInTheDocument();
        expect(screen.queryByText(/^Result:$/)).not.toBeInTheDocument();
    });

    test("an expanded LLM response renders its response preview", async () => {
        const user = userEvent.setup();
        renderCard(step({ type: "AGENT_LLM_RESPONSE_TO_AGENT", data: { llmResponseToAgent: { modelName: "gpt-4o", responsePreview: "Nine widgets remain", response: "Nine widgets remain" } } }));
        await user.click(screen.getByRole("button", { name: "Show details" }));
        expect(screen.getByText(/Response Preview:/)).toBeInTheDocument();
        expect(screen.getByText("Nine widgets remain")).toBeInTheDocument();
    });
});
