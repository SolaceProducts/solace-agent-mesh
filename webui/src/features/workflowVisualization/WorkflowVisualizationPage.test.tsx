// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Workflow } from "@/types/workflow";

import { toAgentCardInfo } from "@/features/workflows/toAgentCardInfo";

import type { AgentCardInfo } from "@/types";

let mockAgents: AgentCardInfo[] = [];
let mockWorkflows: Workflow[] = [];
let workflowsLoading = false;
let workflowsError: Error | null = null;
let canReadWorkflows = true;

const wf = (over: Partial<Workflow>): Workflow => ({
    id: "019f8085-21d7-7407-a710-79570efa462c",
    name: "JiraDigestToConfluence",
    displayName: "Jira Digest to Confluence",
    description: "",
    appConfig: { nodes: [{ id: "start", type: "agent" }] },
    deploymentStatus: "deployed",
    runtimeStatus: { status: "running" },
    createdBy: "u",
    updatedBy: "u",
    createdTime: 0,
    updatedTime: 0,
    ...over,
});

async function loadPage() {
    vi.resetModules();

    vi.doMock("@/features/workflowVisualization/WorkflowDiagram", () => ({
        default: (props: { knownWorkflows?: Set<string> }) => <div data-testid="workflow-diagram" data-known-workflows={props.knownWorkflows ? [...props.knownWorkflows].join(",") : ""} />,
    }));
    vi.doMock("@/features/workflowVisualization/WorkflowNodeDetailPanel", () => ({ default: () => null }));
    vi.doMock("@/features/workflowVisualization/WorkflowDetailsSidePanel", () => ({ default: () => null }));
    vi.doMock("@/features/workflowVisualization/CanvasControls", () => ({ default: () => null }));

    vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false, error: null }) }));
    vi.doMock("@/api/workflows", () => ({
        useWorkflowsQuery: ({ enabled = true }: { enabled?: boolean } = {}) => ({
            data: enabled ? mockWorkflows : [],
            isLoading: enabled ? workflowsLoading : false,
            error: enabled ? workflowsError : null,
        }),
    }));
    vi.doMock("@/providers/contexts", () => ({
        useChatContext: () => ({ agents: mockAgents }),
        useThemeContext: () => ({ currentTheme: "light" }),
        useUserContext: () => ({ userHasCapability: () => canReadWorkflows, CAPABILITIES: { readWorkflows: "readWorkflows" } }),
    }));

    const mod = await import("@/features/workflowVisualization/WorkflowVisualizationPage");
    return mod.WorkflowVisualizationPage;
}

function LocationProbe() {
    const location = useLocation();
    return <div data-testid="location">{`${location.pathname}${location.search}`}</div>;
}

function renderAt(Page: Awaited<ReturnType<typeof loadPage>>, path: string) {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
        <QueryClientProvider client={queryClient}>
            <MemoryRouter initialEntries={[path]}>
                <Routes>
                    <Route path="/agent-mesh/workflows/:workflowName" element={<Page />} />
                </Routes>
                <LocationProbe />
            </MemoryRouter>
        </QueryClientProvider>
    );
}

describe("WorkflowVisualizationPage", () => {
    beforeEach(() => {
        mockAgents = [];
        mockWorkflows = [];
        workflowsLoading = false;
        workflowsError = null;
        canReadWorkflows = true;
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("renders the diagram for a workflow resolved from the platform store by its authored name", async () => {
        mockWorkflows = [wf({})];
        mockAgents = [];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        expect(await screen.findByTestId("workflow-diagram")).toBeInTheDocument();
        expect(screen.queryByText("Workflow not found")).not.toBeInTheDocument();
    });

    test("without readWorkflows the store is not consulted and discovery alone resolves the workflow", async () => {
        canReadWorkflows = false;
        mockWorkflows = [wf({})];
        mockAgents = [toAgentCardInfo(wf({ name: "workflow_019f8085_21d7_7407_a710_79570efa462c", displayName: "Discovered Workflow" }))];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/workflow_019f8085_21d7_7407_a710_79570efa462c");

        expect(await screen.findByTestId("workflow-diagram")).toBeInTheDocument();
    });

    test("without readWorkflows a store-only workflow is not found", async () => {
        canReadWorkflows = false;
        mockWorkflows = [wf({})];
        mockAgents = [];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        expect(await screen.findByText("Workflow not found")).toBeInTheDocument();
    });

    test("falls back to agent-card discovery for a workflow absent from the platform store", async () => {
        mockWorkflows = [];
        mockAgents = [toAgentCardInfo(wf({ name: "workflow_019f8085_21d7_7407_a710_79570efa462c", displayName: "Discovered Workflow" }))];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/workflow_019f8085_21d7_7407_a710_79570efa462c");

        expect(await screen.findByTestId("workflow-diagram")).toBeInTheDocument();
        expect(screen.queryByText("Workflow not found")).not.toBeInTheDocument();
    });

    test("shows the not-found empty state when neither source has the workflow", async () => {
        mockWorkflows = [wf({})];
        mockAgents = [];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/does-not-exist");

        expect(await screen.findByText("Workflow not found")).toBeInTheDocument();
        expect(screen.queryByTestId("workflow-diagram")).not.toBeInTheDocument();
    });

    test("stays in the loading state until the workflow store query resolves", async () => {
        workflowsLoading = true;
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        expect(screen.getByText("Loading...")).toBeInTheDocument();
        expect(screen.queryByText("Workflow not found")).not.toBeInTheDocument();
    });

    test("surfaces a failed store fetch as an error state, not 'Workflow not found'", async () => {
        workflowsError = new Error("boom");
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        expect(await screen.findByText("Error loading data")).toBeInTheDocument();
        expect(screen.queryByText("Workflow not found")).not.toBeInTheDocument();
    });

    test("still renders from a discovered card when the store fetch fails, because the two sources are redundant fallbacks", async () => {
        workflowsError = new Error("boom");
        mockAgents = [toAgentCardInfo(wf({ name: "workflow_019f8085_21d7_7407_a710_79570efa462c", displayName: "Discovered Workflow" }))];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/workflow_019f8085_21d7_7407_a710_79570efa462c");

        expect(await screen.findByTestId("workflow-diagram")).toBeInTheDocument();
        expect(screen.queryByText("Error loading data")).not.toBeInTheDocument();
    });

    test("defers to a discovered card when the store record has no renderable config", async () => {
        mockWorkflows = [wf({ appConfig: {} })];
        mockAgents = [toAgentCardInfo(wf({ name: "JiraDigestToConfluence", appConfig: { nodes: [{ id: "start", type: "agent" }] } }))];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        expect(await screen.findByTestId("workflow-diagram")).toBeInTheDocument();
        expect(screen.queryByText("Workflow not found")).not.toBeInTheDocument();
    });

    test("shows a 'no diagram' state when the workflow exists but has no renderable config", async () => {
        mockWorkflows = [wf({ appConfig: {} })];
        mockAgents = [];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        expect(await screen.findByText("No diagram to display")).toBeInTheDocument();
        expect(screen.queryByText("Workflow not found")).not.toBeInTheDocument();
        expect(screen.queryByTestId("workflow-diagram")).not.toBeInTheDocument();
    });

    test("unions platform-store workflow names into knownWorkflows, not only discovered agent cards, so store-only workflows still count as nested references", async () => {
        mockWorkflows = [wf({}), wf({ id: "019f8085-21d7-7407-a710-000000000002", name: "NestedStoreOnly", displayName: "Nested Store Only", appConfig: {} })];
        mockAgents = [];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        const diagram = await screen.findByTestId("workflow-diagram");
        expect(diagram.getAttribute("data-known-workflows")).toContain("NestedStoreOnly");
    });

    test("the root breadcrumb leads to the Agent Mesh Workflows tab, because /workflows redirects to /chat without readWorkflows", async () => {
        mockWorkflows = [wf({})];
        const Page = await loadPage();
        renderAt(Page, "/agent-mesh/workflows/JiraDigestToConfluence");

        await userEvent.click(await screen.findByRole("button", { name: "Agent Mesh" }));

        expect(screen.getByTestId("location")).toHaveTextContent("/agent-mesh?tab=workflows");
    });
});
