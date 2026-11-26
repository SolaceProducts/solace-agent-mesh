// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { EvalExperiment } from "@/types/eval";

import type { WorkflowOption } from "@/features/evaluations/hooks";

import { MockConfigProvider, MockUserProvider } from "@/testing/providers";

const mockGetExperiment = vi.fn();
const mockCreateMutate = vi.fn();
const mockTriggerRun = vi.fn();
const mockGetExamples = vi.fn();
const mockWorkflowOptions = vi.fn<() => WorkflowOption[]>(() => []);
const mockWorkflowsFailed = vi.fn(() => false);
const mockWorkflowsLoading = vi.fn(() => false);
const mockWorkflowsReadable = vi.fn(() => true);
const mockWorkflowsRequested = vi.fn<(enabled: boolean) => void>();
const mockFlagsReady = vi.fn(() => true);

vi.mock("@openfeature/react-sdk", async () => {
    const actual = await vi.importActual<typeof import("@openfeature/react-sdk")>("@openfeature/react-sdk");
    return { ...actual, useWhenProviderReady: () => mockFlagsReady() };
});

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations")>("@/api/evaluations");
    const overrides = {
        getExperiment: (...args: unknown[]) => mockGetExperiment(...args),
        getExamples: (...args: unknown[]) => mockGetExamples(...args),
        triggerRun: (...args: unknown[]) => mockTriggerRun(...args),
    };
    return {
        ...actual,
        evaluationsApi: { ...actual.evaluationsApi, ...overrides },
        useEvalExperiments: () => ({ data: [] }),
        useCreateExperiment: () => ({ mutateAsync: mockCreateMutate, isPending: false, isError: false }),
        useEvalDatasets: () => ({
            data: [{ id: "ds-1", name: "Orders", exampleCount: 5, experimentCount: 0, createdBy: "u", updatedBy: "u", createdAt: "", updatedAt: "" }],
            refetch: vi.fn(),
            isLoading: false,
        }),
        useEvalEvaluators: () => ({
            data: [{ id: "ev-1", name: "Judge", type: "llm_judge", isDefault: true, experimentCount: 0, createdBy: "u", updatedBy: "u", createdAt: "", updatedAt: "" }],
            refetch: vi.fn(),
            isLoading: false,
        }),
    };
});

vi.mock("@/features/entrypoints/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/entrypoints/hooks");
    return { ...actual, useAgentOptions: () => [] };
});

vi.mock("@/features/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/evaluations/hooks");
    return {
        ...actual,
        useAgentActiveModel: () => ({ activeModelId: undefined, activeModel: undefined }),
        useWorkflowOptions: (enabled: boolean) => {
            mockWorkflowsRequested(enabled);
            return { options: mockWorkflowOptions(), readable: mockWorkflowsReadable(), isLoading: mockWorkflowsLoading(), isError: mockWorkflowsFailed() };
        },
        useResizableSidePanel: () => ({ containerRef: { current: null }, width: 320, handleDragStart: vi.fn() }),
    };
});

vi.mock("@/api/models/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/models/hooks");
    const none = () => ({ data: [], isLoading: false, error: null, refetch: vi.fn() });
    return { ...actual, useModelConfigs: none, useChatModelConfigs: none };
});

import { EvalCreateExperimentPage } from "./EvalCreateExperimentPage";

function ExperimentPageProbe() {
    const location = useLocation();
    const handoff = location.state as { agentNotDeployed?: boolean; runNotStarted?: string } | null;
    return <div data-testid="experiment-page" data-search={location.search} data-not-deployed={handoff?.agentNotDeployed ? "true" : "false"} data-run-not-started={handoff?.runNotStarted ?? ""} />;
}

const workflowExperiment: EvalExperiment = {
    id: "wf-exp-1",
    name: "Order regression",
    description: "Checks the order workflow",
    targetType: "workflow",
    targetAgent: "",
    targetWorkflow: "order_flow",
    datasetId: "ds-1",
    models: [],
    runsPerExample: 1,
    maxWorkers: 2,
    primaryEvaluatorId: "ev-1",
    evaluatorIds: ["ev-1"],
    createdBy: "u",
    updatedBy: "u",
    createdAt: "",
    updatedAt: "",
};

function renderPage(flags: Record<string, boolean>, entry = "/evaluations/experiments/new?cloneFromId=wf-exp-1") {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
        <QueryClientProvider client={client}>
            <OpenFeatureTestProvider flagValueMap={flags}>
                <MockConfigProvider>
                    <MockUserProvider>
                        <MemoryRouter initialEntries={[entry]}>
                            <Routes>
                                <Route path="/evaluations/experiments/new" element={<EvalCreateExperimentPage />} />
                                <Route path="/evaluations/experiments/:id" element={<ExperimentPageProbe />} />
                            </Routes>
                        </MemoryRouter>
                    </MockUserProvider>
                </MockConfigProvider>
            </OpenFeatureTestProvider>
        </QueryClientProvider>
    );
}

describe("EvalCreateExperimentPage — workflow targets", () => {
    afterEach(() => vi.clearAllMocks());

    test("a workflow target has no model field, so each step keeps its own model; an agent's stays required", async () => {
        renderPage({ workflow_evals: true }, "/evaluations/experiments/new");

        expect((await screen.findByText(/^Agent Model/)).textContent).toContain("*");
        await userEvent.click(screen.getByRole("radio", { name: "Workflow" }));

        await waitFor(() => expect(screen.queryByText(/^Agent Model/)).toBeNull());
        expect(screen.queryByText(/Model for AI agent steps/)).toBeNull();
    });

    test("a cloned workflow experiment is saved with its workflow, not an agent", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        mockCreateMutate.mockResolvedValue({ id: "new-exp" });
        renderPage({ workflow_evals: true });

        expect(await screen.findByTestId("experimentTargetType")).toBeInTheDocument();
        await waitFor(() => expect(screen.getByText("Order Flow")).toBeInTheDocument());
        await userEvent.click(screen.getByTestId("experimentWizardCreate"));

        await waitFor(() => expect(mockCreateMutate).toHaveBeenCalled());
        const payload = mockCreateMutate.mock.calls[0][0];
        expect(payload.targetWorkflow).toBe("order_flow");
        expect(payload).not.toHaveProperty("targetAgent");
        expect(payload.models).toEqual([]);
        expect(await screen.findByTestId("experiment-page")).toBeInTheDocument();
    });

    test("a copied workflow experiment keeps the models it was copied with", async () => {
        mockGetExperiment.mockResolvedValue({ ...workflowExperiment, models: [{ modelConfigId: "m1", alias: "fast", model: "gpt-x" }] });
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        mockCreateMutate.mockResolvedValue({ id: "new-exp" });
        renderPage({ workflow_evals: true });

        await waitFor(() => expect(screen.getByText("Order Flow")).toBeInTheDocument());
        await userEvent.click(screen.getByTestId("experimentWizardCreate"));

        await waitFor(() => expect(mockCreateMutate).toHaveBeenCalled());
        expect(mockCreateMutate.mock.calls[0][0].models).toEqual([{ modelConfigId: "m1", alias: "fast", model: "gpt-x" }]);
    });

    test("switching a copied agent experiment to a workflow drops the agent's models", async () => {
        mockGetExperiment.mockResolvedValue({ ...workflowExperiment, targetType: "agent", targetAgent: "Orchestrator", targetWorkflow: null, models: [{ modelConfigId: "m1", alias: "fast", model: "gpt-x" }] });
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        mockCreateMutate.mockResolvedValue({ id: "new-exp" });
        renderPage({ workflow_evals: true });

        await userEvent.click(await screen.findByRole("radio", { name: "Workflow" }));
        await userEvent.click(screen.getAllByRole("combobox")[0]);
        await userEvent.click(await screen.findByText("Order Flow"));
        await waitFor(() => expect(screen.getByTestId("experimentWizardCreate")).toBeEnabled());
        await userEvent.click(screen.getByTestId("experimentWizardCreate"));

        await waitFor(() => expect(mockCreateMutate).toHaveBeenCalled());
        expect(mockCreateMutate.mock.calls[0][0]).toMatchObject({ targetWorkflow: "order_flow", models: [] });
    });

    test("a refusal lists each example that cannot run once, by its editor row and with the value it sent", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockGetExamples.mockResolvedValue([
            { id: "e9", datasetId: "ds-1", sequenceNumber: 9, prompt: "", inputData: { action: "standard" }, createdAt: "", updatedAt: "" },
            { id: "e4", datasetId: "ds-1", sequenceNumber: 4, prompt: "", inputData: { action: "urgent", orderId: "O-1" }, createdAt: "", updatedAt: "" },
        ]);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        const refusal = Object.assign(new Error("2 examples cannot run against this workflow: examples[1].inputData: …"), {
            status: 422,
            validationDetails: {
                "examples[4].inputData": ["Validation error at 'action': at '/action': value must be one of 'classify', 'fail', 'ask'"],
                "examples[9].inputData": ["Validation error at 'root': at '': missing property 'orderId'"],
            },
        });
        mockCreateMutate.mockRejectedValue(refusal);
        renderPage({ workflow_evals: true });

        await waitFor(() => expect(screen.getByText("Order Flow")).toBeInTheDocument());
        await userEvent.click(screen.getByTestId("experimentWizardCreate"));

        const list = await screen.findByTestId("exampleProblems");
        expect(list).toHaveTextContent("The input data in 2 examples does not match Order Flow. Correct the examples listed.");
        expect(list).toHaveTextContent('Example 1: The action field in the input data is "urgent", which this workflow does not accept. Change it to one of these values: "classify", "fail", "ask".');
        expect(list).toHaveTextContent("Example 2: The input data does not include the required field orderId. Add this field to the input data.");
        expect(mockGetExamples).toHaveBeenCalledWith("ds-1");
        expect(screen.queryByText(/examples\[1\]/)).not.toBeInTheDocument();

        await userEvent.click(screen.getByRole("radio", { name: "Agent" }));
        await waitFor(() => expect(screen.queryByTestId("exampleProblems")).not.toBeInTheDocument());
        await userEvent.click(screen.getByRole("radio", { name: "Workflow" }));
        expect(await screen.findByTestId("exampleProblems")).toHaveTextContent("does not match Order Flow");
    });

    test("a workflow that is not deployed is called out", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "not_deployed" }]);
        renderPage({ workflow_evals: true });

        expect(await screen.findByText(/This workflow is not deployed/)).toBeInTheDocument();
    });

    test("while the workflow list loads, Create & Run waits but Create does not", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([]);
        mockWorkflowsLoading.mockReturnValue(true);
        try {
            renderPage({ workflow_evals: true });

            expect(await screen.findByTestId("experimentTargetType")).toBeInTheDocument();
            await waitFor(() => expect(screen.getByTestId("experimentWizardCreate")).toBeEnabled());
            expect(screen.getByTestId("experimentWizardCreateAndRun")).toBeDisabled();
            expect(screen.queryByTestId("workflowsLoadFailed")).not.toBeInTheDocument();
        } finally {
            mockWorkflowsLoading.mockReturnValue(false);
        }
    });

    test("Create & Run on a deployed workflow saves, starts a run and opens the runs tab", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        mockCreateMutate.mockResolvedValue({ id: "new-exp" });
        mockTriggerRun.mockResolvedValue({});
        renderPage({ workflow_evals: true });

        await waitFor(() => expect(screen.getByText("Order Flow")).toBeInTheDocument());
        await waitFor(() => expect(screen.getByTestId("experimentWizardCreateAndRun")).toBeEnabled());
        await userEvent.click(screen.getByTestId("experimentWizardCreateAndRun"));

        const page = await screen.findByTestId("experiment-page");
        expect(mockTriggerRun).toHaveBeenCalledWith("new-exp", {});
        expect(page).toHaveAttribute("data-search", "?tab=runs");
        expect(page).toHaveAttribute("data-not-deployed", "false");
    });

    test("Create & Run whose run does not start hands the reason to the experiment page", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        mockCreateMutate.mockResolvedValue({ id: "new-exp" });
        mockTriggerRun.mockRejectedValue(new Error("A run of this experiment is already pending or running."));
        renderPage({ workflow_evals: true });

        await waitFor(() => expect(screen.getByTestId("experimentWizardCreateAndRun")).toBeEnabled());
        await userEvent.click(screen.getByTestId("experimentWizardCreateAndRun"));

        const page = await screen.findByTestId("experiment-page");
        expect(page).toHaveAttribute("data-run-not-started", "A run of this experiment is already pending or running.");
        expect(page).toHaveAttribute("data-search", "");
    });

    test("Create & Run on a workflow that is not deployed saves without running and hands the reason to the experiment page", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "not_deployed" }]);
        mockCreateMutate.mockResolvedValue({ id: "new-exp" });
        renderPage({ workflow_evals: true });

        await waitFor(() => expect(screen.getByTestId("experimentWizardCreateAndRun")).toBeEnabled());
        await userEvent.click(screen.getByTestId("experimentWizardCreateAndRun"));

        const page = await screen.findByTestId("experiment-page");
        expect(mockTriggerRun).not.toHaveBeenCalled();
        expect(page).toHaveAttribute("data-not-deployed", "true");
    });

    test("a workflow list that failed to load says so and holds Create & Run, but not Create", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([]);
        mockWorkflowsFailed.mockReturnValue(true);
        renderPage({ workflow_evals: true });

        expect(await screen.findByTestId("workflowsLoadFailed")).toBeInTheDocument();
        await waitFor(() => expect(screen.getByTestId("experimentWizardCreate")).toBeEnabled());
        expect(screen.getByTestId("experimentWizardCreateAndRun")).toBeDisabled();
        mockWorkflowsFailed.mockReturnValue(false);
    });

    test("a copy waits for the flags before choosing its target, so a reload keeps a workflow copy a workflow", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        mockFlagsReady.mockReturnValue(false);
        try {
            const { rerender } = renderPage({ workflow_evals: true });
            await screen.findByTestId("experimentTargetType");
            expect(mockGetExperiment).not.toHaveBeenCalled();

            mockFlagsReady.mockReturnValue(true);
            rerender(
                <QueryClientProvider client={new QueryClient()}>
                    <OpenFeatureTestProvider flagValueMap={{ workflow_evals: true }}>
                        <MockConfigProvider>
                            <MockUserProvider>
                                <MemoryRouter initialEntries={["/evaluations/experiments/new?cloneFromId=wf-exp-1"]}>
                                    <Routes>
                                        <Route path="/evaluations/experiments/new" element={<EvalCreateExperimentPage />} />
                                    </Routes>
                                </MemoryRouter>
                            </MockUserProvider>
                        </MockConfigProvider>
                    </OpenFeatureTestProvider>
                </QueryClientProvider>
            );

            await waitFor(() => expect(screen.getByText("Order Flow")).toBeInTheDocument());
            expect(screen.getByRole("radio", { name: "Workflow" })).toBeChecked();
            expect(screen.queryByText(/workflow evaluations are turned off/)).not.toBeInTheDocument();
        } finally {
            mockFlagsReady.mockReturnValue(true);
        }
    });

    test("copying a workflow experiment with workflow evaluations off starts with no target and says why", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        renderPage({ workflow_evals: false });

        expect(await screen.findByText(/The experiment you copied evaluates a workflow/)).toBeInTheDocument();
        expect(screen.queryByTestId("experimentTargetType")).not.toBeInTheDocument();
        expect(screen.getByText("Agent")).toBeInTheDocument();
        expect(screen.getByTestId("experimentWizardCreate")).toBeDisabled();
    });

    test("the Workflow target choice carries the Early Access badge", async () => {
        renderPage({ workflow_evals: true }, "/evaluations/experiments/new");

        const choice = (await screen.findByRole("radio", { name: /Workflow/ })).closest("label") as HTMLElement;
        expect(within(choice).getByText("EARLY ACCESS")).toBeInTheDocument();
    });

    test("with the flag off no target choice is offered and no workflow is requested", async () => {
        renderPage({ workflow_evals: false }, "/evaluations/experiments/new");

        expect(await screen.findByText("Agent")).toBeInTheDocument();
        expect(screen.queryByTestId("experimentTargetType")).not.toBeInTheDocument();
        expect(mockWorkflowsRequested).toHaveBeenLastCalledWith(false);
    });

    test("an agent target's Create & Run does not wait on the workflow list", async () => {
        mockGetExperiment.mockResolvedValue({ ...workflowExperiment, targetType: "agent", targetAgent: "Orchestrator", targetWorkflow: null });
        mockWorkflowsLoading.mockReturnValue(true);
        try {
            renderPage({ workflow_evals: true });
            await waitFor(() => expect(screen.getByTestId("experimentWizardCreate")).toBeEnabled());
            expect(screen.getByTestId("experimentWizardCreateAndRun")).toBeEnabled();
            expect(mockWorkflowsRequested).toHaveBeenLastCalledWith(true);
        } finally {
            mockWorkflowsLoading.mockReturnValue(false);
        }
    });

    test("for a user who may not read workflows, Create & Run does not wait on a list it cannot load", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([]);
        mockWorkflowsReadable.mockReturnValue(false);
        mockWorkflowsLoading.mockReturnValue(true);
        try {
            renderPage({ workflow_evals: true });
            await waitFor(() => expect(screen.getByTestId("experimentWizardCreateAndRun")).toBeEnabled());
            expect(screen.queryByTestId("workflowsLoadFailed")).not.toBeInTheDocument();
        } finally {
            mockWorkflowsReadable.mockReturnValue(true);
            mockWorkflowsLoading.mockReturnValue(false);
        }
    });

    test("a name already taken says so", async () => {
        mockGetExperiment.mockResolvedValue(workflowExperiment);
        mockWorkflowOptions.mockReturnValue([{ name: "order_flow", displayName: "Order Flow", deploymentStatus: "deployed" }]);
        mockCreateMutate.mockRejectedValue(Object.assign(new Error("conflict"), { status: 409 }));
        renderPage({ workflow_evals: true });

        await waitFor(() => expect(screen.getByTestId("experimentWizardCreate")).toBeEnabled());
        await userEvent.click(screen.getByTestId("experimentWizardCreate"));

        expect(await screen.findByText(/already/i)).toBeInTheDocument();
        expect(screen.queryByTestId("exampleProblems")).not.toBeInTheDocument();
    });
});
