// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { ApiV2Error } from "@/api/common";
import type { ModelConfig } from "@/api/models/types";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import type { EvalExperiment, EvalRun } from "@/types/eval";

import type { AgentOption } from "@/features/entrypoints/hooks";
import type { WorkflowOption } from "@/features/evaluations/hooks/useWorkflowOptions";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const mockGetExperiment = vi.fn();
const mockAgentOptions = vi.fn<() => AgentOption[]>(() => []);
const mockWorkflowOptions = vi.fn<() => WorkflowOption[]>(() => []);
const mockWorkflowsLoading = vi.fn(() => false);
const mockWorkflowsRequested = vi.fn<(enabled: boolean) => void>();
const mockTriggerRun = vi.fn();
const mockRuns = vi.fn<() => EvalRun[]>(() => []);
const mockUpdateExperiment = vi.fn();
const mockUseModelConfigs = vi.fn<() => { data: ModelConfig[] }>(() => ({ data: [] }));
const defaultToolAuthChecklist = {
    runAs: "system:eval",
    agents: [{ agentName: "Test Agent", isTarget: true, reachable: true, tools: [{ credentialKey: "gmail", displayName: "Gmail", interactive: true, status: "not_connected" }] }],
    allowListIsWildcard: false,
};
const mockToolAuthChecklist = vi.fn<() => unknown>(() => defaultToolAuthChecklist);

vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return { ...actual, useLoaderData: () => ({ experimentId: "exp-under-test" }) };
});

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations");
    return {
        ...actual,
        useEvalDatasets: () => ({ data: [], refetch: vi.fn(), isLoading: false }),
        useEvalEvaluators: () => ({ data: [], refetch: vi.fn(), isLoading: false }),
        useUpdateExperiment: () => ({ mutateAsync: mockUpdateExperiment, isPending: false }),
        useDeleteExperiment: () => ({ mutateAsync: vi.fn(), isPending: false }),
        useDeleteRunGroup: () => ({ mutateAsync: vi.fn(), isPending: false }),
        useExperimentToolAuth: () => ({ data: mockToolAuthChecklist(), isLoading: false, error: null }),
        useBeginExperimentToolAuth: () => ({ mutateAsync: vi.fn(), isPending: false, variables: undefined }),
        useDisconnectExperimentToolAuth: () => ({ mutateAsync: vi.fn(), isPending: false }),
    };
});

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: {
            ...actual.evaluationsApi,
            getExperiment: (...args: unknown[]) => mockGetExperiment(...args),
        },
    };
});

vi.mock("@/api/models/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/models/hooks");
    return { ...actual, useModelConfigs: () => mockUseModelConfigs(), useChatModelConfigs: () => mockUseModelConfigs() };
});

vi.mock("@/features/entrypoints/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/entrypoints/hooks");
    return { ...actual, useAgentOptions: () => mockAgentOptions() };
});

vi.mock("@/features/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/evaluations/hooks");
    return {
        ...actual,
        useAgentActiveModel: () => ({ activeModelId: undefined, activeModel: undefined }),
        useEvalRuns: () => ({ runs: mockRuns(), loading: false, triggerRun: mockTriggerRun, fetchRuns: vi.fn() }),
        useResizableSidePanel: () => ({ containerRef: { current: null }, width: 320, handleDragStart: vi.fn() }),
        useWorkflowOptions: (enabled: boolean) => {
            mockWorkflowsRequested(enabled);
            return { options: mockWorkflowOptions(), readable: true, isLoading: mockWorkflowsLoading(), isError: false };
        },
    };
});

import { EvalExperimentDetailPage } from "./EvalExperimentDetailPage";

const baseExperiment: EvalExperiment = {
    id: "exp-under-test",
    name: "My Experiment",
    description: "An experiment",
    targetAgent: "agent-broker-name",
    datasetId: "ds-1",
    models: [{ modelConfigId: "model-1", model: "gpt-4", alias: "GPT-4" }],
    runsPerExample: 1,
    maxWorkers: 1,
    primaryEvaluatorId: "ev-1",
    evaluatorIds: ["ev-1"],
    createdBy: "u",
    updatedBy: "u",
    createdAt: "",
    updatedAt: "",
};

function makeModelConfig(over: Partial<ModelConfig>): ModelConfig {
    return {
        id: "cfg",
        alias: "alias",
        provider: null,
        modelName: null,
        apiBase: null,
        authType: null,
        authConfig: {},
        modelParams: {},
        maxInputTokens: null,
        description: null,
        createdBy: "",
        updatedBy: "",
        createdAt: "",
        updatedAt: "",
        ...over,
    };
}

interface RenderOptions {
    readonly handoffState?: boolean;
    readonly runNotStarted?: string;
    readonly flagValues?: Record<string, boolean>;
}

function renderPage(options: RenderOptions = {}) {
    const handoff = options.handoffState || options.runNotStarted ? { agentNotDeployed: options.handoffState, runNotStarted: options.runNotStarted } : undefined;
    const initialEntry = handoff ? { pathname: "/evaluations/experiments/exp-under-test", state: handoff } : "/evaluations/experiments/exp-under-test";
    return render(
        <StoryProvider flagValues={options.flagValues}>
            <MemoryRouter initialEntries={[initialEntry]}>
                <Routes>
                    <Route path="/evaluations/experiments/:experimentId" element={<EvalExperimentDetailPage />} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvalExperimentDetailPage — agent-not-deployed handling", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    test("Run Experiment button opens the not-deployed dialog (run-flow copy) when the target agent isn't deployed", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(baseExperiment);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "not_deployed", agentId: "agent-id-1" }]);

        renderPage();

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        expect(await screen.findByText("Unable to Run Experiment")).toBeInTheDocument();
        expect(screen.queryByText(/has been saved/i)).not.toBeInTheDocument();
        expect(screen.getByText(/cannot be run/i)).toBeInTheDocument();
        expect(screen.getAllByText("Pretty Agent Name").length).toBeGreaterThan(0);
    });

    test("Run Experiment button does NOT open the not-deployed dialog when the agent is deployed", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(baseExperiment);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Deployed Agent", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        renderPage();

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        expect(screen.queryByText("Unable to Run Experiment")).not.toBeInTheDocument();
    });

    test("uses the agent display name in the configuration sidebar", async () => {
        mockGetExperiment.mockResolvedValue(baseExperiment);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        renderPage();

        await waitFor(() => expect(screen.getByText("Pretty Agent Name")).toBeInTheDocument());
    });

    test("says the run did not start when the create page hands that over", async () => {
        mockGetExperiment.mockResolvedValue(baseExperiment);
        renderPage({ runNotStarted: "A run of this experiment is already pending or running." });

        expect(await screen.findByText("The experiment was saved, but its run did not start: A run of this experiment is already pending or running.")).toBeInTheDocument();
    });

    test("surfaces the not-deployed dialog with create-flow copy when arriving via per-navigation router state", async () => {
        mockGetExperiment.mockResolvedValue(baseExperiment);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "not_deployed", agentId: "agent-id-1" }]);

        renderPage({ handoffState: true });

        expect(await screen.findByText("Unable to Run Experiment")).toBeInTheDocument();
        expect(screen.getByText(/has been saved/i)).toBeInTheDocument();
    });
});

describe("EvalExperimentDetailPage — workflow experiments", () => {
    const workflowExperiment: EvalExperiment = { ...baseExperiment, targetType: "workflow", targetAgent: "", targetWorkflow: "order_flow", models: [] };

    afterEach(() => {
        vi.clearAllMocks();
    });

    test("Run Experiment on a workflow that is not deployed names the workflow and points to Workflows", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "not_deployed" }]);
        renderPage({ flagValues: { workflow_evals: true } });

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toHaveTextContent("This experiment cannot be run because the workflow Order Flow is not deployed.");
        expect(dialog).toHaveTextContent("Deploy the workflow, then run the experiment from the Experiment Lab.");
        expect(screen.getByRole("button", { name: "Go to Workflows" })).toBeInTheDocument();
    });

    test("Run Experiment while the workflow list is loading says so instead of running", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowsLoading.mockReturnValue(true);
        renderPage({ flagValues: { workflow_evals: true } });

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        expect(await screen.findByText(/cannot check that the workflow is deployed/)).toBeInTheDocument();
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        mockWorkflowsLoading.mockReturnValue(false);
    });

    test("Run Experiment on a deployed workflow opens the run dialog", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        renderPage({ flagValues: { workflow_evals: true } });

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).not.toHaveTextContent("Unable to Run Experiment");
        expect(within(dialog).getByRole("button", { name: /Run/ })).toBeInTheDocument();
        expect(mockWorkflowsRequested).toHaveBeenLastCalledWith(true);
    });

    test("Run Experiment while workflow evaluations are off says so instead of running", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        renderPage();

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        expect(await screen.findByText(/Workflow evaluations are turned off/)).toBeInTheDocument();
        expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
        expect(mockWorkflowsRequested).toHaveBeenLastCalledWith(false);
    });

    test("an agent experiment requests no workflows", async () => {
        mockGetExperiment.mockResolvedValue(baseExperiment);
        renderPage({ flagValues: { workflow_evals: true } });

        await screen.findAllByRole("button", { name: /Run Experiment/i });
        expect(mockWorkflowsRequested).toHaveBeenLastCalledWith(false);
    });

    test("a refused save lists the examples the workflow cannot take, and an unchanged dataset is not sent", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockUpdateExperiment.mockRejectedValue(
            Object.assign(new Error("1 example cannot run against this workflow"), { status: 422, validationDetails: { "examples[3].inputData": ["Validation error at 'root': at '': missing property 'orderId'"] } })
        );
        renderPage({ flagValues: { workflow_evals: true } });

        await user.click(await screen.findByRole("button", { name: "Edit" }));
        await user.click(screen.getByRole("button", { name: "Save" }));

        const list = await screen.findByTestId("exampleProblems");
        expect(list).toHaveTextContent("The input data does not include the required field orderId. Add this field to the input data.");
        expect(screen.queryByText(/examples\[3\]/)).not.toBeInTheDocument();
        expect(mockUpdateExperiment.mock.calls[0][0].data).not.toHaveProperty("datasetId");
    });

    test("editing a workflow experiment can be saved, and the save leaves the target alone", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockUpdateExperiment.mockResolvedValue({ ...workflowExperiment, name: "Renamed" });
        renderPage({ flagValues: { workflow_evals: true } });

        await user.click(await screen.findByRole("button", { name: "Edit" }));
        const save = screen.getByRole("button", { name: "Save" });
        expect(save).toBeEnabled();
        await user.click(save);

        await waitFor(() => expect(mockUpdateExperiment).toHaveBeenCalledTimes(1));
        expect(mockUpdateExperiment.mock.calls[0][0].data).not.toHaveProperty("targetAgent");
        expect(mockUpdateExperiment.mock.calls[0][0].data).not.toHaveProperty("targetWorkflow");
    });
});

describe("EvalExperimentDetailPage — configuration sidebar", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    test("renders an em-dash placeholder when the experiment has no target agent", async () => {
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, targetAgent: "" });
        mockAgentOptions.mockReturnValue([]);

        renderPage();

        await screen.findByText("Agent");
        expect(screen.getAllByText("—").length).toBeGreaterThan(0);
    });

    test("clicking the dataset link navigates to its lab detail page", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, datasetId: "ds-42", datasetName: "Pretty Dataset" });
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        render(
            <StoryProvider>
                <MemoryRouter initialEntries={["/evaluations/experiments/exp-under-test"]}>
                    <Routes>
                        <Route path="/evaluations/experiments/:experimentId" element={<EvalExperimentDetailPage />} />
                        <Route path="/evaluations/lab" element={<LabRouteProbe />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );

        const link = await screen.findByRole("button", { name: "Pretty Dataset" });
        await user.click(link);

        await waitFor(() => expect(screen.getByTestId("lab-route")).toBeInTheDocument());
        expect(screen.getByTestId("lab-route").textContent).toContain("ds-42");
    });

    test("clicking an evaluator link navigates to its lab detail page", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, evaluatorIds: ["ev-7"], primaryEvaluatorId: "ev-7" });
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        render(
            <StoryProvider>
                <MemoryRouter initialEntries={["/evaluations/experiments/exp-under-test"]}>
                    <Routes>
                        <Route path="/evaluations/experiments/:experimentId" element={<EvalExperimentDetailPage />} />
                        <Route path="/evaluations/lab" element={<LabRouteProbe />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );

        const link = await screen.findByRole("button", { name: "ev-7" });
        await user.click(link);

        await waitFor(() => expect(screen.getByTestId("lab-route")).toBeInTheDocument());
        expect(screen.getByTestId("lab-route").textContent).toContain("ev-7");
    });

    test("clicking a model with a modelConfigId navigates to its models detail page", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, models: [{ modelConfigId: "model-9", model: "gpt-4", alias: "GPT-4" }] });
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);
        mockUseModelConfigs.mockReturnValue({ data: [] });

        render(
            <StoryProvider>
                <MemoryRouter initialEntries={["/evaluations/experiments/exp-under-test"]}>
                    <Routes>
                        <Route path="/evaluations/experiments/:experimentId" element={<EvalExperimentDetailPage />} />
                        <Route path="/models/:id" element={<ModelRouteProbe />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );

        const link = await screen.findByRole("button", { name: "GPT-4" });
        await user.click(link);

        await waitFor(() => expect(screen.getByTestId("models-route")).toBeInTheDocument());
        expect(screen.getByTestId("models-route").textContent).toContain("model-9");
    });

    test("resolves an alias-only model (no modelConfigId) to its config via the alias and links to it", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, models: [{ model: "general", alias: "general" }] });
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);
        mockUseModelConfigs.mockReturnValue({ data: [makeModelConfig({ id: "cfg-general", alias: "general" })] });

        render(
            <StoryProvider>
                <MemoryRouter initialEntries={["/evaluations/experiments/exp-under-test"]}>
                    <Routes>
                        <Route path="/evaluations/experiments/:experimentId" element={<EvalExperimentDetailPage />} />
                        <Route path="/models/:id" element={<ModelRouteProbe />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );

        const link = await screen.findByRole("button", { name: "general" });
        await user.click(link);

        await waitFor(() => expect(screen.getByTestId("models-route")).toBeInTheDocument());
        expect(screen.getByTestId("models-route").textContent).toContain("cfg-general");
    });

    test("renders an alias-only model with no matching config as plain text, not a dead link", async () => {
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, models: [{ model: "general", alias: "general" }] });
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);
        mockUseModelConfigs.mockReturnValue({ data: [] });

        renderPage();

        await screen.findByText("Agent Models");
        expect(screen.queryByRole("button", { name: "general" })).not.toBeInTheDocument();
        expect(screen.getAllByText("general").length).toBeGreaterThan(0);
    });

    test("renders a model as plain text when the viewer cannot read model configs", async () => {
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, models: [{ modelConfigId: "model-9", model: "gpt-4", alias: "GPT-4" }] });
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);
        mockUseModelConfigs.mockReturnValue({ data: [] });

        render(
            <StoryProvider userContextValues={{ userHasCapability: capability => capability !== CAPABILITIES.readModelConfig }}>
                <MemoryRouter initialEntries={["/evaluations/experiments/exp-under-test"]}>
                    <Routes>
                        <Route path="/evaluations/experiments/:experimentId" element={<EvalExperimentDetailPage />} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );

        await screen.findByText("Agent Models");
        expect(screen.queryByRole("button", { name: "GPT-4" })).not.toBeInTheDocument();
        expect(screen.getAllByText("GPT-4").length).toBeGreaterThan(0);
    });
});

describe("EvalExperimentDetailPage — dataset-changed run warning", () => {
    const deployedAgent: AgentOption = { name: "agent-broker-name", displayName: "Deployed Agent", deploymentStatus: "deployed", agentId: "agent-id-1" };

    test("run dialog warns when the dataset changed after the latest run", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue({
            ...baseExperiment,
            datasetUpdatedTime: "2026-08-19T12:00:00.000Z",
            latestRunTime: "2026-08-19T11:00:00.000Z",
        });
        mockAgentOptions.mockReturnValue([deployedAgent]);
        renderPage();

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        expect(await screen.findByTestId("datasetChangedWarning")).toBeInTheDocument();
        expect(screen.getByText(/dataset has changed since the last run/i)).toBeInTheDocument();
    });

    test("run dialog shows no warning when the dataset is unchanged", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue({
            ...baseExperiment,
            datasetUpdatedTime: "2026-08-19T10:00:00.000Z",
            latestRunTime: "2026-08-19T11:00:00.000Z",
        });
        mockAgentOptions.mockReturnValue([deployedAgent]);
        renderPage();

        const runButtons = await screen.findAllByRole("button", { name: /Run Experiment/i });
        await user.click(runButtons[0]);

        expect(await screen.findByRole("button", { name: /^Run Experiment$/i, hidden: false })).toBeInTheDocument();
        expect(screen.queryByTestId("datasetChangedWarning")).not.toBeInTheDocument();
    });
});

describe("EvalExperimentDetailPage — save errors", () => {
    test("a rename to a taken name says the name is taken", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(baseExperiment);
        mockUpdateExperiment.mockRejectedValue(new ApiV2Error(409, "Unable to update experiment"));
        renderPage();

        await user.click(await screen.findByRole("button", { name: "Edit" }));
        await user.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText("An experiment with this name already exists.")).toBeInTheDocument();
    });
});

describe("EvalExperimentDetailPage — Max Workers", () => {
    test("the sidebar shows the experiment's Max Workers", async () => {
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, maxWorkers: 7 });
        renderPage();

        const label = await screen.findByText("Max Workers");
        expect(label.nextElementSibling).toHaveTextContent("7");
    });

    test("the sidebar falls back to the default of 4 when the experiment has none", async () => {
        const { maxWorkers: _omitted, ...withoutMaxWorkers } = baseExperiment;
        mockGetExperiment.mockResolvedValue(withoutMaxWorkers);
        renderPage();

        const label = await screen.findByText("Max Workers");
        expect(label.nextElementSibling).toHaveTextContent("4");
    });

    test("editing Max Workers and saving sends the new value", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(baseExperiment);
        mockUpdateExperiment.mockResolvedValue({ ...baseExperiment, maxWorkers: 9 });
        renderPage();

        await user.click(await screen.findByRole("button", { name: "Edit" }));
        await user.click(screen.getByText("Advanced Options"));
        const input = screen.getByRole("spinbutton", { name: "Max Workers" });
        expect(input).toHaveValue(1);
        await user.clear(input);
        await user.type(input, "9");
        await user.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(mockUpdateExperiment).toHaveBeenCalledTimes(1));
        expect(mockUpdateExperiment.mock.calls[0][0].data).toMatchObject({ maxWorkers: 9 });
    });

    test("an out-of-range Max Workers shows the error and disables Save", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(baseExperiment);
        renderPage();

        await user.click(await screen.findByRole("button", { name: "Edit" }));
        await user.click(screen.getByText("Advanced Options"));
        const input = screen.getByRole("spinbutton", { name: "Max Workers" });
        await user.clear(input);
        await user.type(input, "21");

        expect(screen.getByText("Max Workers must be a whole number from 1 to 20.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    });
});

describe("EvalExperimentDetailPage — Connect Tools panel", () => {
    afterEach(() => {
        vi.clearAllMocks();
        mockToolAuthChecklist.mockReturnValue(defaultToolAuthChecklist);
    });

    test("panel renders once the experiment loads", async () => {
        mockGetExperiment.mockResolvedValue(baseExperiment);
        renderPage();
        expect(await screen.findByTestId("connectToolsPanel")).toBeInTheDocument();
    });

    test("panel stays hidden when no agent in the graph has credentials to connect", async () => {
        mockToolAuthChecklist.mockReturnValue({ runAs: "system:eval", agents: [{ agentName: "Test Agent", isTarget: true, reachable: true, tools: [] }], allowListIsWildcard: false });
        mockGetExperiment.mockResolvedValue(baseExperiment);
        renderPage();
        await screen.findAllByRole("button", { name: /Run Experiment/i });
        expect(screen.queryByTestId("connectToolsPanel")).not.toBeInTheDocument();
    });

    test("panel lists a workflow experiment's tool-node credentials and node agents", async () => {
        mockToolAuthChecklist.mockReturnValue({
            runAs: "system:eval",
            agents: [
                { agentName: "order_flow", isTarget: true, reachable: true, tools: [{ credentialKey: "erp", displayName: "ERP", interactive: true, status: "not_connected" }] },
                { agentName: "OrderClassifier", isTarget: false, nodeIds: ["classify"], reachable: true, tools: [{ credentialKey: "crm", displayName: "CRM", interactive: true, status: "not_connected" }] },
            ],
            allowListIsWildcard: false,
        });
        mockGetExperiment.mockResolvedValue({ ...baseExperiment, targetType: "workflow", targetAgent: "", targetWorkflow: "order_flow" });
        renderPage();
        expect(await screen.findByTestId("connectToolsPanel")).toBeInTheDocument();
        expect(screen.getByText("Required by tool steps in order_flow")).toBeInTheDocument();
        expect(screen.getByText("Required by the classify step, which calls the OrderClassifier agent")).toBeInTheDocument();
        expect(screen.getByText("Models for AI agent steps")).toBeInTheDocument();
    });
});

function LabRouteProbe() {
    const loc = useLocation();
    return <div data-testid="lab-route">{`${loc.pathname}${loc.search}`}</div>;
}

function ModelRouteProbe() {
    const loc = useLocation();
    return <div data-testid="models-route">{loc.pathname}</div>;
}

describe("EvalExperimentDetailPage — Report History delete", () => {
    afterEach(() => {
        vi.clearAllMocks();
        mockRuns.mockReturnValue([]);
    });

    async function openRowActions(status: EvalRun["status"]) {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(baseExperiment);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Pretty Agent Name", deploymentStatus: "deployed", agentId: "agent-id-1" }]);
        mockRuns.mockReturnValue([{ id: "run-1", runGroupId: "group-1", experimentId: "exp-under-test", model: "gpt-4", status, startedAt: "2026-01-01T00:00:00Z" } as EvalRun]);
        renderPage();
        await user.click(await screen.findByRole("button", { name: "Row actions" }));
        await screen.findByRole("menuitem", { name: "Open Report" });
    }

    test("Delete Report is hidden while the group has an active run", async () => {
        await openRowActions("running");
        expect(screen.queryByRole("menuitem", { name: "Delete Report" })).not.toBeInTheDocument();
    });

    test("Delete Report shows once every run of the group has finished", async () => {
        await openRowActions("completed");
        expect(screen.getByRole("menuitem", { name: "Delete Report" })).toBeInTheDocument();
    });
});
