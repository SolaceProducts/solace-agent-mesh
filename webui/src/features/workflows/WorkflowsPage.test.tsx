// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import type { Workflow } from "@/types/workflow";

const ALL_WORKFLOW_CAPABILITIES: Capability[] = [CAPABILITIES.readWorkflows, CAPABILITIES.createWorkflows, CAPABILITIES.updateWorkflows, CAPABILITIES.deleteWorkflows];

let mockQueryResult: { data?: Workflow[]; isLoading?: boolean; error?: Error | null; refetch?: () => void } = {};
let heldCapabilities: Capability[] = [];
let seedCalls: unknown[][] = [];
let seedShouldThrow = false;

async function loadPage() {
    vi.resetModules();

    vi.doMock("@/features/workflows/seedWorkflowIntoBuilder", () => ({
        seedWorkflowIntoBuilder: (...args: unknown[]) => {
            seedCalls.push(args);
            return seedShouldThrow ? Promise.reject(new Error("seed boom")) : Promise.resolve();
        },
    }));

    vi.doMock("@/api/agents", () => ({ useAgents: () => ({ data: [] }), agentsApi: { getAll: () => Promise.resolve([]) } }));

    vi.doMock("@/api/workflows", () => ({
        useWorkflowsQuery: () => ({
            data: mockQueryResult.data,
            isLoading: mockQueryResult.isLoading ?? false,
            error: mockQueryResult.error ?? null,
            refetch: mockQueryResult.refetch ?? vi.fn(),
        }),
        workflowsApi: { getAll: () => Promise.resolve(mockQueryResult.data ?? []) },
    }));

    vi.doMock("@/providers/contexts", async () => {
        const { CAPABILITIES: realCapabilities } = await vi.importActual<typeof import("@/providers/contexts/UserContext")>("@/providers/contexts/UserContext");
        return {
            useUserContext: () => ({
                userHasCapability: (capability: Capability) => heldCapabilities.includes(capability),
                CAPABILITIES: realCapabilities,
            }),
            useChatContext: () => ({ addNotification: vi.fn() }),
            useThemeContext: () => ({ currentTheme: "light" }),
        };
    });

    const mod = await import("@/features/workflows/WorkflowsPage");
    return mod.WorkflowsPage;
}

function renderPage(Page: Awaited<ReturnType<typeof loadPage>>) {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={queryClient}>
                <MemoryRouter>
                    <Page />
                </MemoryRouter>
            </QueryClientProvider>
        </OpenFeatureTestProvider>
    );
}

describe("WorkflowsPage", () => {
    beforeEach(() => {
        mockQueryResult = {};
        heldCapabilities = ALL_WORKFLOW_CAPABILITIES;
        seedCalls = [];
        seedShouldThrow = false;
        sessionStorage.clear();
    });

    afterEach(() => {
        vi.restoreAllMocks();
        sessionStorage.clear();
    });

    test("shows loading state while workflows are fetching", async () => {
        mockQueryResult = { isLoading: true };
        const Page = await loadPage();
        renderPage(Page);
        expect(screen.getByText("Loading...")).toBeInTheDocument();
    });

    test("shows error state when workflows fail to load", async () => {
        mockQueryResult = { error: new Error("Network error") };
        const Page = await loadPage();
        renderPage(Page);
        expect(screen.getByText("Error loading workflows")).toBeInTheDocument();
    });

    test("renders workflows from platform API and maps status labels", async () => {
        mockQueryResult = {
            data: [
                {
                    id: "wf-active",
                    name: "active_workflow",
                    displayName: "Active Workflow",
                    description: "",
                    appConfig: { nodes: [{ id: "n1", type: "agent" }] },
                    deploymentStatus: "deployed",
                    runtimeStatus: { status: "running" },
                    createdBy: "user",
                    updatedBy: "user",
                    createdTime: 0,
                    updatedTime: 0,
                },
                {
                    id: "wf-disconnected",
                    name: "disconnected_workflow",
                    displayName: "Disconnected Workflow",
                    description: "",
                    appConfig: {},
                    deploymentStatus: "deployed",
                    runtimeStatus: { status: "disconnected" },
                    createdBy: "user",
                    updatedBy: "user",
                    createdTime: 0,
                    updatedTime: 0,
                },
                {
                    id: "wf-idle",
                    name: "idle_workflow",
                    displayName: "Idle Workflow",
                    description: "",
                    appConfig: {},
                    deploymentStatus: "not_deployed",
                    createdBy: "user",
                    updatedBy: "user",
                    createdTime: 0,
                    updatedTime: 0,
                },
                {
                    id: "wf-versioned",
                    name: "versioned_workflow",
                    displayName: "Versioned Workflow",
                    description: "",
                    appConfig: { version: "2.3.0" },
                    deploymentStatus: "not_deployed",
                    createdBy: "user",
                    updatedBy: "user",
                    createdTime: 0,
                    updatedTime: 0,
                },
            ] satisfies Workflow[],
        };
        const Page = await loadPage();
        renderPage(Page);

        expect(await screen.findByText("Active Workflow")).toBeInTheDocument();
        expect(await screen.findByText("Disconnected Workflow")).toBeInTheDocument();
        expect(await screen.findByText("Idle Workflow")).toBeInTheDocument();
        expect(await screen.findByText("Running")).toBeInTheDocument();
        expect(await screen.findByText("Disconnected")).toBeInTheDocument();
        expect(await screen.findAllByText("Not Deployed")).toHaveLength(2);
        expect(await screen.findByText("2.3.0")).toBeInTheDocument();
        expect(screen.getAllByText("1.0.0")).toHaveLength(3);
        expect(screen.queryByText("N/A")).not.toBeInTheDocument();
    });

    test("empty state offers the Build with AI CTA and seeds redirect keys for the builder when the user can create", async () => {
        mockQueryResult = { data: [] };
        heldCapabilities = [CAPABILITIES.readWorkflows, CAPABILITIES.createWorkflows];
        const Page = await loadPage();
        renderPage(Page);
        const user = userEvent.setup();

        const cta = await screen.findByRole("button", { name: /build with ai/i });
        expect(screen.getByText("Learn how to create workflows")).toBeInTheDocument();

        await user.click(cta);
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toContain("create a workflow");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID)).toBeTruthy();
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT)).toBe("true");
    });

    test("empty state falls back to the docs link when the user cannot create", async () => {
        mockQueryResult = { data: [] };
        heldCapabilities = [CAPABILITIES.readWorkflows, CAPABILITIES.updateWorkflows, CAPABILITIES.deleteWorkflows];
        const Page = await loadPage();
        renderPage(Page);

        expect(await screen.findByText("Learn how to create workflows")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /build with ai/i })).not.toBeInTheDocument();
    });

    test("Edit-in-Builder deterministically seeds the workflow, then sets the redirect keys", async () => {
        heldCapabilities = [CAPABILITIES.readWorkflows, CAPABILITIES.updateWorkflows];
        mockQueryResult = {
            data: [
                {
                    id: "wf-1",
                    name: "order_flow",
                    displayName: "Order Flow",
                    description: "",
                    appConfig: {},
                    deploymentStatus: "deployed",
                    runtimeStatus: { status: "running" },
                    createdBy: "user",
                    updatedBy: "user",
                    createdTime: 0,
                    updatedTime: 0,
                },
            ] satisfies Workflow[],
        };
        const Page = await loadPage();
        renderPage(Page);
        const user = userEvent.setup();

        await user.click(await screen.findByRole("button", { name: /more/i }));
        await user.click(await screen.findByText(/edit with ai/i));

        await waitFor(() => expect(seedCalls.length).toBe(1));
        const [sessionId, seededWorkflow] = seedCalls[0] as [string, Workflow];
        expect(sessionId).toBeTruthy();
        expect(seededWorkflow.name).toBe("order_flow");

        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID)).toBe(sessionId));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT)).toBe("true");
        const chip = JSON.parse(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP) ?? "null");
        expect(chip).toMatchObject({ label: "Order Flow", kind: "workflow" });
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toMatch(/already loaded/i);
    });

    test("Edit-in-Builder falls back to agent-load when seeding fails, still setting auto-submit + chip", async () => {
        seedShouldThrow = true;
        mockQueryResult = {
            data: [
                { id: "wf-1", name: "order_flow", displayName: "Order Flow", description: "", appConfig: {}, deploymentStatus: "deployed", runtimeStatus: { status: "running" }, createdBy: "user", updatedBy: "user", createdTime: 0, updatedTime: 0 },
            ] satisfies Workflow[],
        };
        const Page = await loadPage();
        renderPage(Page);
        const user = userEvent.setup();

        await user.click(await screen.findByRole("button", { name: /more/i }));
        await user.click(await screen.findByText(/edit with ai/i));

        await waitFor(() => expect(seedCalls.length).toBe(1));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toMatch(/Load the existing deployed workflow/));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).not.toMatch(/already loaded/i);
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT)).toBe("true");
        expect(JSON.parse(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP) ?? "null")).toMatchObject({ kind: "workflow" });
    });
});
