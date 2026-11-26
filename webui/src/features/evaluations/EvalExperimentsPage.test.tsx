// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { EvalExperiment } from "@/types/eval";

import type { AgentOption } from "@/features/entrypoints/hooks";
import type { WorkflowOptions } from "@/features/evaluations/hooks/useWorkflowOptions";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const mockAgentOptions = vi.fn<() => AgentOption[]>(() => []);
const mockExperiments = vi.fn<() => EvalExperiment[]>(() => []);
const mockTriggerRun = vi.fn();
const mockWorkflows = vi.fn<(_enabled?: boolean) => WorkflowOptions>(() => ({ options: [], readable: true, isLoading: false, isError: false }));

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations");
    return {
        ...actual,
        useEvalExperiments: () => ({ data: mockExperiments(), isLoading: false, refetch: vi.fn() }),
        useEvalEvaluators: () => ({ data: [], isLoading: false, refetch: vi.fn() }),
        useDeleteExperiment: () => ({ mutateAsync: vi.fn(), isPending: false }),
    };
});

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: {
            ...actual.evaluationsApi,
            triggerRun: (...args: unknown[]) => mockTriggerRun(...args),
        },
    };
});

vi.mock("@/features/entrypoints/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/entrypoints/hooks");
    return { ...actual, useAgentOptions: () => mockAgentOptions() };
});

vi.mock("@/features/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/evaluations/hooks");
    return { ...actual, useWorkflowOptions: (enabled: boolean) => mockWorkflows(enabled) };
});

vi.mock("@/api/models/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/models/hooks");
    return { ...actual, useModelConfigs: () => ({ data: [], isLoading: false }), useChatModelConfigs: () => ({ data: [], isLoading: false }) };
});

import { EvalExperimentsPage } from "./EvalExperimentsPage";

function makeExperiment(overrides: Partial<EvalExperiment> = {}): EvalExperiment {
    return {
        id: "exp-1",
        name: "My Experiment",
        description: "",
        targetAgent: "agent-broker-name",
        datasetId: "ds-1",
        datasetName: "Dataset 1",
        models: [{ modelConfigId: "model-1", model: "gpt-4", alias: "GPT-4" }],
        runsPerExample: 1,
        maxWorkers: 1,
        primaryEvaluatorId: "ev-1",
        evaluatorIds: ["ev-1"],
        createdBy: "u",
        updatedBy: "u",
        createdAt: "",
        updatedAt: "",
        ...overrides,
    };
}

function renderPage(flagValues: Record<string, boolean> = {}) {
    return render(
        <StoryProvider flagValues={flagValues}>
            <MemoryRouter initialEntries={["/evaluations/lab?tab=experiments"]}>
                <EvalExperimentsPage />
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvalExperimentsPage — agent display name + run handling", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    test("renders the agent's friendly display name in the Agent column", async () => {
        mockExperiments.mockReturnValue([makeExperiment({ targetAgent: "agent-broker-name" })]);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        renderPage();

        await waitFor(() => expect(screen.getByText("Pretty Agent Name")).toBeInTheDocument());
        expect(screen.queryByText("agent-broker-name")).not.toBeInTheDocument();
    });

    test("Run Experiment menu item opens the not-deployed dialog when the agent isn't deployed", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([makeExperiment({ name: "Undeployed Exp", targetAgent: "agent-broker-name" })]);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "not_deployed", agentId: "agent-id-42" }]);

        renderPage();
        await screen.findByText("Pretty Agent Name");

        await user.click(screen.getByRole("button", { name: /more/i }));
        await user.click(await screen.findByRole("menuitem", { name: /Run Experiment/i }));

        expect(await screen.findByText("Unable to Run Experiment")).toBeInTheDocument();
        expect(mockTriggerRun).not.toHaveBeenCalled();
    });

    test("matches by displayName as a fallback when targetAgent isn't a broker name", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([makeExperiment({ name: "Legacy Exp", targetAgent: "Pretty Agent Name" })]);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "not_deployed", agentId: "agent-id-1" }]);

        renderPage();
        await screen.findByText("Legacy Exp");

        await user.click(screen.getByRole("button", { name: /more/i }));
        await user.click(await screen.findByRole("menuitem", { name: /Run Experiment/i }));

        expect(await screen.findByText("Unable to Run Experiment")).toBeInTheDocument();
    });

    test("a refused run shows the server's reason", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([makeExperiment()]);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);
        mockTriggerRun.mockRejectedValue(new Error("A run of this experiment is already pending or running."));

        renderPage();
        await screen.findByText("My Experiment");
        await user.click(screen.getByRole("button", { name: /more/i }));
        await user.click(await screen.findByRole("menuitem", { name: /Run Experiment/i }));
        await user.click(await screen.findByRole("button", { name: "Run Experiment" }));

        expect(await screen.findByText("A run of this experiment is already pending or running.")).toBeInTheDocument();
    });
});

describe("EvalExperimentsPage — workflow experiments", () => {
    const workflowExperiment = makeExperiment({ name: "Flow Exp", targetType: "workflow", targetAgent: "", targetWorkflow: "order_flow", models: [] });

    afterEach(() => {
        vi.clearAllMocks();
        mockWorkflows.mockReturnValue({ options: [], readable: true, isLoading: false, isError: false });
    });

    const clickRun = async (user: ReturnType<typeof userEvent.setup>) => {
        await screen.findByText("Flow Exp");
        await user.click(screen.getByRole("button", { name: /more/i }));
        await user.click(await screen.findByRole("menuitem", { name: /Run Experiment/i }));
    };

    test("Run on a workflow that is not deployed opens the dialog in the workflow's words", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([workflowExperiment]);
        mockWorkflows.mockReturnValue({ options: [{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "not_deployed" }], readable: true, isLoading: false, isError: false });
        renderPage({ workflow_evals: true });

        await clickRun(user);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toHaveTextContent("This experiment cannot be run because the workflow Order Flow is not deployed.");
        expect(screen.getByRole("button", { name: "Go to Workflows" })).toBeInTheDocument();
    });

    test("Run while the workflow list has not loaded says so and opens nothing", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([workflowExperiment]);
        mockWorkflows.mockReturnValue({ options: [], readable: true, isLoading: false, isError: true });
        renderPage({ workflow_evals: true });

        await clickRun(user);

        expect(await screen.findByText(/cannot check that the workflow is deployed/)).toBeInTheDocument();
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    test("Run on a workflow experiment while workflow evaluations are off says so, and Clone is not offered", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([workflowExperiment]);
        renderPage();

        await clickRun(user);

        expect(await screen.findByText(/Workflow evaluations are turned off/)).toBeInTheDocument();
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: /more/i }));
        expect(screen.queryByRole("menuitem", { name: "Clone" })).not.toBeInTheDocument();
    });

    test("an agent-only list requests no workflows, and with the flag off an agent experiment keeps Clone", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([makeExperiment({ name: "Agent Exp" })]);
        renderPage();

        await screen.findByText("Agent Exp");
        expect(mockWorkflows).toHaveBeenLastCalledWith(false);
        await user.click(screen.getByRole("button", { name: /more/i }));
        expect(await screen.findByRole("menuitem", { name: "Clone" })).toBeInTheDocument();
    });

    test("a workflow experiment's row shows its workflow as the target", async () => {
        mockExperiments.mockReturnValue([workflowExperiment]);
        renderPage({ workflow_evals: true });

        expect(await screen.findByText("order_flow")).toBeInTheDocument();
    });

    test("Run on a deployed workflow opens the run dialog", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([workflowExperiment]);
        mockWorkflows.mockReturnValue({ options: [{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }], readable: true, isLoading: false, isError: false });
        renderPage({ workflow_evals: true });

        await clickRun(user);

        expect(await screen.findByRole("button", { name: "Run Experiment" })).toBeInTheDocument();
        expect(screen.queryByText("Unable to Run Experiment")).not.toBeInTheDocument();
        expect(mockWorkflows).toHaveBeenLastCalledWith(true);
    });
});

describe("EvalExperimentsPage — QuickStart banner dismissibility", () => {
    const STORAGE_KEY = "eval.experimentsQuickStartDismissed";

    afterEach(() => {
        vi.clearAllMocks();
        window.localStorage.removeItem(STORAGE_KEY);
    });

    test("renders the banner by default and persists dismissal across remounts", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([]);
        mockAgentOptions.mockReturnValue([]);

        const { unmount } = renderPage();

        const dismiss = await screen.findByRole("button", { name: "Dismiss" });
        await user.click(dismiss);

        await waitFor(() => expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument());
        expect(window.localStorage.getItem(STORAGE_KEY)).toBe("true");

        unmount();
        renderPage();
        await waitFor(() => expect(screen.getByText(/You haven't created any experiments yet/i)).toBeInTheDocument());
        expect(screen.queryByRole("button", { name: "Dismiss" })).not.toBeInTheDocument();
    });

    test("storage key is distinct from the Reports-page key", async () => {
        window.localStorage.setItem("eval.reportsQuickStartDismissed", "true");
        mockExperiments.mockReturnValue([]);
        mockAgentOptions.mockReturnValue([]);

        renderPage();

        expect(await screen.findByRole("button", { name: "Dismiss" })).toBeInTheDocument();
        window.localStorage.removeItem("eval.reportsQuickStartDismissed");
    });

    test("clicking the banner CTA navigates to the new-experiment wizard", async () => {
        const user = userEvent.setup();
        mockExperiments.mockReturnValue([]);
        mockAgentOptions.mockReturnValue([]);

        function LocationProbe() {
            const location = useLocation();
            return <div data-testid="location">{location.pathname}</div>;
        }

        render(
            <StoryProvider>
                <MemoryRouter initialEntries={["/evaluations/lab?tab=experiments"]}>
                    <Routes>
                        <Route path="/evaluations/lab" element={<EvalExperimentsPage />} />
                        <Route path="/evaluations/experiments/new" element={<LocationProbe />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );

        const cta = await screen.findByRole("button", { name: /Create Experiment/i });
        await user.click(cta);

        await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/evaluations/experiments/new"));
    });
});

describe("EvalExperimentsPage — dataset-changed run warning", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    async function openRunDialog() {
        const user = userEvent.setup();
        await screen.findByText("My Experiment");
        await user.click(screen.getByRole("button", { name: /more/i }));
        await user.click(await screen.findByText("Run Experiment"));
    }

    test("warns when the dataset changed after the latest run", async () => {
        mockExperiments.mockReturnValue([
            makeExperiment({
                datasetUpdatedTime: "2026-01-02T00:00:00.000+00:00",
                latestRunTime: "2026-01-01T00:00:00.000+00:00",
            }),
        ]);
        renderPage();

        await openRunDialog();

        expect(await screen.findByTestId("datasetChangedWarning")).toBeInTheDocument();
    });

    test("no warning when the latest run is newer than the dataset", async () => {
        mockExperiments.mockReturnValue([
            makeExperiment({
                datasetUpdatedTime: "2026-01-01T00:00:00.000+00:00",
                latestRunTime: "2026-01-02T00:00:00.000+00:00",
            }),
        ]);
        renderPage();

        await openRunDialog();

        await screen.findByText(/Provide a brief name for this run/i);
        expect(screen.queryByTestId("datasetChangedWarning")).not.toBeInTheDocument();
    });
});
