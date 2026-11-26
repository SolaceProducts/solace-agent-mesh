// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { SeedSessionArtifacts } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ArtifactInfo } from "@/types";

import { VisualizerStepCard } from "./VisualizerStepCard";

const server = setupServer(http.get("*/api/v1/artifacts/:sessionId", () => HttpResponse.json({ data: [mockArtifact] })));

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const ts = new Date().toISOString();
const baseStep = { rawEventIds: [], nestingLevel: 0, owningTaskId: "task-1" };

const llmResponseStep = {
    id: "s-1",
    type: "AGENT_LLM_RESPONSE_TO_AGENT" as const,
    timestamp: ts,
    title: "LLM Response",
    data: {
        llmResponseToAgent: {
            modelName: "gpt-4o",
            responsePreview: "I will generate the report now.",
            response: "I will generate the report now.",
            isFinalResponse: false,
        },
    },
    ...baseStep,
};

function renderCard(props: Partial<Parameters<typeof VisualizerStepCard>[0]> = {}) {
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={{ sessionId: "test" }}>
                <VisualizerStepCard step={llmResponseStep} {...props} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("LLMResponseToAgentDetails expand/collapse (component extracted to module level)", () => {
    test("initially shows collapsed state with 'Show details' button", () => {
        renderCard();
        expect(screen.getByText("Show details")).toBeInTheDocument();
        expect(screen.queryByText("LLM Response Details:")).not.toBeInTheDocument();
    });

    test("clicking 'Show details' expands the detail panel", async () => {
        const user = userEvent.setup();
        renderCard();
        await user.click(screen.getByText("Show details"));
        expect(screen.getByText("LLM Response Details:")).toBeInTheDocument();
        expect(screen.getByText("Hide details")).toBeInTheDocument();
    });

    test("expanded state survives parent re-render (module-level extraction fix)", async () => {
        const user = userEvent.setup();
        const { rerender } = renderCard();
        await user.click(screen.getByText("Show details"));
        expect(screen.getByText("LLM Response Details:")).toBeInTheDocument();

        rerender(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test" }}>
                    <VisualizerStepCard step={llmResponseStep} isHighlighted={true} />
                </StoryProvider>
            </MemoryRouter>
        );

        expect(screen.getByText("LLM Response Details:")).toBeInTheDocument();
    });
});

const artifactStep = {
    id: "s-artifact",
    type: "AGENT_ARTIFACT_NOTIFICATION" as const,
    timestamp: ts,
    title: "Created Artifact - report.md",
    data: {
        artifactNotification: {
            artifactName: "report.md",
            version: 2,
            mimeType: "text/markdown",
            description: "A generated report",
        },
    },
    ...baseStep,
};

const mockArtifact = {
    filename: "report.md",
    mimeType: "text/markdown",
    size: 1024,
    uri: "artifact://test/report.md",
    lastModified: new Date().toISOString(),
    version: 1,
};

describe("VisualizerStepCard View File with custom props", () => {
    test("passes undefined version when step has no version", async () => {
        const stepNoVersion = {
            ...artifactStep,
            data: {
                artifactNotification: { artifactName: "report.md", mimeType: "text/markdown" },
            },
        };
        const artifactLookup = vi.fn().mockReturnValue(mockArtifact);
        const onViewArtifact = vi.fn();

        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test" }}>
                    <VisualizerStepCard step={stepNoVersion} artifactLookup={artifactLookup} onViewArtifact={onViewArtifact} />
                </StoryProvider>
            </MemoryRouter>
        );

        await userEvent.setup().click(screen.getByRole("button", { name: "View File" }));

        expect(onViewArtifact).toHaveBeenCalledWith(mockArtifact, undefined);
    });

    test("does not call onViewArtifact when artifactLookup returns undefined", async () => {
        const artifactLookup = vi.fn().mockReturnValue(undefined);
        const onViewArtifact = vi.fn();

        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test" }}>
                    <VisualizerStepCard step={artifactStep} artifactLookup={artifactLookup} onViewArtifact={onViewArtifact} />
                </StoryProvider>
            </MemoryRouter>
        );

        await userEvent.setup().click(screen.getByRole("button", { name: "View File" }));

        expect(artifactLookup).toHaveBeenCalledWith("report.md");
        expect(onViewArtifact).not.toHaveBeenCalled();
    });

    test("falls back to ChatContext when props are not provided", async () => {
        const setPreviewArtifact = vi.fn();

        render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test", setPreviewArtifact }}>
                    <SeedSessionArtifacts sessionId="test" artifacts={[mockArtifact as unknown as ArtifactInfo]} />
                    <VisualizerStepCard step={artifactStep} />
                </StoryProvider>
            </MemoryRouter>
        );

        await userEvent.setup().click(await screen.findByRole("button", { name: "View File" }));

        expect(setPreviewArtifact).toHaveBeenCalledWith(mockArtifact);
    });
});

describe("VisualizerStepCard long-identifier truncation", () => {
    const longAgentName = "agent_019e8f3a_46d6_7c30_9beb_0c71fc5d8865";
    const longToolName = "peer_07c2_4f10_b9aa_d3e1_6655443322110099";

    const delegationStep = {
        id: "s-deleg",
        type: "AGENT_LLM_RESPONSE_TOOL_DECISION" as const,
        timestamp: ts,
        title: "LLM: Tool Decision",
        delegationInfo: [{ functionCallId: "fc-1", peerAgentName: longAgentName, subTaskId: "019e954e-d41f-7abc" }],
        data: {
            toolDecision: {
                isParallel: false,
                decisions: [{ functionCallId: "fc-1", toolName: longToolName, toolArguments: {}, isPeerDelegation: true }],
            },
        },
        ...baseStep,
    };

    function renderStep(step: Parameters<typeof VisualizerStepCard>[0]["step"]) {
        return render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test" }}>
                    <VisualizerStepCard step={step} />
                </StoryProvider>
            </MemoryRouter>
        );
    }

    test("delegation agent name exposes the full label+name via title for hover", () => {
        renderStep(delegationStep);
        const span = screen.getByText(longAgentName, { exact: false });
        expect(span).toHaveAttribute("title", `Delegated to: ${longAgentName}`);
    });

    test("tool-call name exposes the full value via title for hover", () => {
        renderStep(delegationStep);
        const code = screen.getByText(longToolName);
        expect(code).toHaveAttribute("title", longToolName);
    });
});

describe("VisualizerStepCard keyboard accessibility", () => {
    test("pressing Enter calls onClick when provided", () => {
        const onClick = vi.fn();
        renderCard({ onClick });
        const cardDiv = screen.getAllByRole("button").find(el => el.tagName === "DIV");
        expect(cardDiv).toBeDefined();
        fireEvent.keyDown(cardDiv!, { key: "Enter" });
        expect(onClick).toHaveBeenCalledTimes(1);
    });

    test("card div has no role=button when onClick is not provided", () => {
        renderCard();
        const divButtons = screen.getAllByRole("button").filter(el => el.tagName === "DIV");
        expect(divButtons).toHaveLength(0);
    });
});

describe("VisualizerStepCard decision node steps", () => {
    function renderWorkflowStep(data: Parameters<typeof VisualizerStepCard>[0]["step"]["data"], type: "WORKFLOW_NODE_EXECUTION_START" | "WORKFLOW_NODE_EXECUTION_RESULT") {
        return render(
            <MemoryRouter>
                <StoryProvider chatContextValues={{ sessionId: "test" }}>
                    <VisualizerStepCard step={{ id: "s-decision", type, timestamp: ts, title: "Node triage", data, ...baseStep }} />
                </StoryProvider>
            </MemoryRouter>
        );
    }

    test("a decision start carries the decision icon", () => {
        const { container } = renderWorkflowStep({ workflowNodeExecutionStart: { nodeId: "triage", nodeType: "decision", dependsOn: [] } }, "WORKFLOW_NODE_EXECUTION_START");
        expect(container.querySelector(".lucide-signpost")).not.toBeNull();
    });

    test("a confident decision result lists its answers and the routed branch", () => {
        const { container } = renderWorkflowStep(
            {
                workflowNodeExecutionResult: {
                    nodeId: "triage",
                    status: "success",
                    metadata: {
                        answers: { category: { type: "choice", choice: "billing", confidence: 0.92 }, urgent: { type: "boolean", value: true, probability: 0.81 } },
                        escalated: false,
                        escalation_reason: "",
                        selected_branch: "billing_agent",
                        routed_branch: "billing_agent",
                    },
                },
            },
            "WORKFLOW_NODE_EXECUTION_RESULT"
        );
        expect(container.textContent).toContain("category: billing · 92% confidence");
        expect(container.textContent).toContain("urgent: yes · 81% likely");
        expect(container.textContent).toContain("Selected Branch: billing_agent");
        expect(container.textContent).not.toMatch(/escalated/);
    });

    test("an escalated decision result names the reason and provider error", () => {
        const { container } = renderWorkflowStep(
            {
                workflowNodeExecutionResult: {
                    nodeId: "triage",
                    status: "success",
                    metadata: { answers: {}, escalated: true, escalation_reason: "provider_error", provider_error: "timeout", selected_branch: "human_review" },
                },
            },
            "WORKFLOW_NODE_EXECUTION_RESULT"
        );
        expect(container.textContent).toContain("Selected Branch: human_review");
        expect(screen.getByText(/escalated · provider error/)).toHaveTextContent("escalated · provider error (timeout)");
    });

    test("a switch result keeps its selected-branch line", () => {
        const { container } = renderWorkflowStep({ workflowNodeExecutionResult: { nodeId: "router", status: "success", metadata: { selected_branch: "report_agent", selected_case_index: 0 } } }, "WORKFLOW_NODE_EXECUTION_RESULT");
        expect(container.textContent).toContain("Selected Branch: report_agent");
    });
});
