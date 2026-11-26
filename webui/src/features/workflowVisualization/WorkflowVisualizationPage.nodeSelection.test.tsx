// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { Workflow } from "@/types/workflow";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}

const SELECTED_NODE_CLASS = "!border-(--accent-n2-wMain)";

const workflow: Workflow = {
    id: "019f8085-21d7-7407-a710-79570efa462c",
    name: "JiraDigestToConfluence",
    displayName: "Jira Digest to Confluence",
    description: "",
    appConfig: { nodes: [{ id: "summarize", type: "agent", agent_name: "SummarizerAgent" }] },
    deploymentStatus: "deployed",
    runtimeStatus: { status: "running" },
    createdBy: "u",
    updatedBy: "u",
    createdTime: 0,
    updatedTime: 0,
};

async function renderPage() {
    vi.resetModules();
    vi.stubGlobal("ResizeObserver", ResizeObserverStub);

    vi.doMock("@/features/workflowVisualization/WorkflowNodeDetailPanel", () => ({ default: () => <div data-testid="node-detail-panel" /> }));
    vi.doMock("@/features/workflowVisualization/WorkflowDetailsSidePanel", () => ({ default: () => <div data-testid="workflow-details-panel" /> }));
    vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false, error: null }) }));
    vi.doMock("@/api/workflows", () => ({ useWorkflowsQuery: () => ({ data: [workflow], isLoading: false, error: null }) }));
    vi.doMock("@/providers/contexts", () => ({
        useChatContext: () => ({ agents: [] }),
        useThemeContext: () => ({ currentTheme: "light" }),
        useUserContext: () => ({ userHasCapability: () => true, CAPABILITIES: { readWorkflows: "readWorkflows" } }),
    }));

    const { WorkflowVisualizationPage } = await import("@/features/workflowVisualization/WorkflowVisualizationPage");
    render(
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
            <MemoryRouter initialEntries={["/agent-mesh/workflows/JiraDigestToConfluence"]}>
                <Routes>
                    <Route path="/agent-mesh/workflows/:workflowName" element={<WorkflowVisualizationPage />} />
                </Routes>
            </MemoryRouter>
        </QueryClientProvider>
    );
}

describe("WorkflowVisualizationPage node selection", () => {
    beforeEach(() => {
        vi.unstubAllGlobals();
    });

    test("deselects the node when opening the workflow details closes the node panel", async () => {
        await renderPage();

        fireEvent.click(await screen.findByText("SummarizerAgent"));
        expect(screen.getByTestId("node-detail-panel")).toBeInTheDocument();
        expect(screen.getByText("SummarizerAgent").closest(`.${CSS.escape(SELECTED_NODE_CLASS)}`)).not.toBeNull();

        fireEvent.click(screen.getByRole("button", { name: /Open Workflow Details/ }));

        expect(screen.queryByTestId("node-detail-panel")).not.toBeInTheDocument();
        expect(screen.getByTestId("workflow-details-panel")).toBeInTheDocument();
        expect(screen.getByText("SummarizerAgent").closest(`.${CSS.escape(SELECTED_NODE_CLASS)}`)).toBeNull();
    });
});
