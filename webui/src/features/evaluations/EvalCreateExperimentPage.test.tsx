// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { evaluationKeys } from "@/api/evaluations/keys";
import type { ModelConfig } from "@/api/models/types";

import type { EvalExperiment } from "@/types/eval";

import type { AgentOption } from "@/features/entrypoints/hooks";
import type { AgentActiveModel } from "@/features/evaluations/hooks/useAgentActiveModel";

import { mockModels } from "@/testing/data/models";
import { MockConfigProvider, MockUserProvider } from "@/testing/providers";

const mockGetExperiment = vi.fn();
const mockGetExperiments = vi.fn((..._args: unknown[]) => Promise.resolve<EvalExperiment[]>([]));
const mockTriggerRun = vi.fn();
const mockCreateMutate = vi.fn();
const mockAgentOptions = vi.fn<() => AgentOption[]>(() => []);
const mockUseAgentActiveModel = vi.fn<() => AgentActiveModel>(() => ({ activeModelId: undefined, activeModel: undefined }));
const mockUseModelConfigs = vi.fn(() => ({ data: [] as ModelConfig[], isLoading: false, error: null, refetch: vi.fn() }));

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations");
    return {
        ...actual,
        useCreateExperiment: () => ({ mutateAsync: mockCreateMutate, isPending: false, isError: false }),
        useEvalDatasets: () => ({
            data: [{ id: "ds-1", name: "Dataset 1", exampleCount: 5, experimentCount: 0, createdBy: "u", updatedBy: "u", createdAt: "", updatedAt: "" }],
            refetch: vi.fn(),
            isLoading: false,
        }),
        useEvalEvaluators: () => ({
            data: [{ id: "ev-1", name: "Evaluator 1", type: "llm_judge", isDefault: true, experimentCount: 0, createdBy: "u", updatedBy: "u", createdAt: "", updatedAt: "" }],
            refetch: vi.fn(),
            isLoading: false,
        }),
    };
});

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: {
            ...actual.evaluationsApi,
            getExperiment: (...args: unknown[]) => mockGetExperiment(...args),
            getExperiments: (...args: unknown[]) => mockGetExperiments(...args),
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
    return {
        ...actual,
        useAgentActiveModel: () => mockUseAgentActiveModel(),
        useResizableSidePanel: () => ({ containerRef: { current: null }, width: 320, handleDragStart: vi.fn() }),
    };
});

vi.mock("@/api/models/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/models/hooks");
    return { ...actual, useModelConfigs: () => mockUseModelConfigs(), useChatModelConfigs: () => mockUseModelConfigs() };
});

import { EvalCreateExperimentPage } from "./EvalCreateExperimentPage";

const cloneSource: EvalExperiment = {
    id: "source-exp-1",
    name: "Source Experiment",
    description: "Cloned experiment",
    targetAgent: "agent-broker-name",
    datasetId: "ds-1",
    models: [{ modelConfigId: "model-1", model: "gpt-4", alias: "GPT-4" }],
    runsPerExample: 2,
    maxWorkers: 1,
    primaryEvaluatorId: "ev-1",
    evaluatorIds: ["ev-1"],
    createdBy: "u",
    updatedBy: "u",
    createdAt: "",
    updatedAt: "",
};

function LocationProbe() {
    const location = useLocation();
    const state = location.state as { agentNotDeployed?: boolean } | null;
    const agentNotDeployedFlag = state?.agentNotDeployed ? "1" : "0";
    return (
        <>
            <div data-testid="location">{`${location.pathname}${location.search}`}</div>
            <div data-testid="agent-not-deployed-state">{agentNotDeployedFlag}</div>
        </>
    );
}

function renderPage(initialEntry = "/evaluations/experiments/new?cloneFromId=source-exp-1") {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const utils = render(
        <QueryClientProvider client={client}>
            <OpenFeatureTestProvider flagValueMap={{}}>
                <MockConfigProvider>
                    <MockUserProvider>
                        <MemoryRouter initialEntries={[initialEntry]}>
                            <Routes>
                                <Route path="/evaluations/experiments/new" element={<EvalCreateExperimentPage />} />
                                <Route path="/evaluations/experiments/:id" element={<LocationProbe />} />
                                <Route path="/evaluations/lab" element={<LocationProbe />} />
                            </Routes>
                        </MemoryRouter>
                    </MockUserProvider>
                </MockConfigProvider>
            </OpenFeatureTestProvider>
        </QueryClientProvider>
    );
    return { ...utils, client };
}

describe("EvalCreateExperimentPage — name uniqueness false-positive guard", () => {
    afterEach(() => {
        vi.clearAllMocks();
        mockGetExperiments.mockImplementation(() => Promise.resolve([]));
    });

    test("inline 'name already exists' is suppressed while a save is in flight, even if the cache contains the just-created name", async () => {
        const user = userEvent.setup();
        const createdExperiment: EvalExperiment = {
            ...cloneSource,
            id: "created-exp-42",
            name: "Source Experiment (copy)",
        };
        mockGetExperiment.mockResolvedValue(cloneSource);
        mockGetExperiments.mockResolvedValue([]);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Deployed Agent" }]);

        let resolveCreate: (value: EvalExperiment) => void = () => {};
        mockCreateMutate.mockImplementation(
            () =>
                new Promise<EvalExperiment>(resolve => {
                    resolveCreate = resolve;
                })
        );
        mockTriggerRun.mockResolvedValue([]);

        const { client } = renderPage();

        await waitFor(() => expect(mockGetExperiment).toHaveBeenCalledWith("source-exp-1"));
        const createAndRunButton = await screen.findByRole("button", { name: /Create & Run/i });
        await waitFor(() => expect(createAndRunButton).toBeEnabled());
        expect(screen.queryByText(/An experiment with this name already exists/i)).not.toBeInTheDocument();

        await user.click(createAndRunButton);
        await waitFor(() => expect(mockCreateMutate).toHaveBeenCalledTimes(1));

        client.setQueryData(evaluationKeys.experimentsList(), [createdExperiment]);
        await new Promise(resolve => setTimeout(resolve, 0));
        await new Promise(resolve => setTimeout(resolve, 0));

        expect(screen.queryByText(/An experiment with this name already exists/i)).not.toBeInTheDocument();

        resolveCreate(createdExperiment);
        await waitFor(() => expect(mockTriggerRun).toHaveBeenCalled());
    });

    test("inline 'name already exists' is shown for a genuine duplicate (regression guard)", async () => {
        mockGetExperiment.mockResolvedValue(cloneSource);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Deployed Agent" }]);

        mockGetExperiments.mockResolvedValue([{ ...cloneSource, id: "existing-1", name: "Source Experiment (copy)" }]);

        renderPage();

        await waitFor(() => expect(mockGetExperiment).toHaveBeenCalledWith("source-exp-1"));

        await waitFor(() => {
            expect(screen.getByText(/An experiment with this name already exists/i)).toBeInTheDocument();
        });

        const createAndRunButton = screen.getByRole("button", { name: /Create & Run/i });
        expect(createAndRunButton).toBeDisabled();
    });
});

describe("EvalCreateExperimentPage — agent-not-deployed handoff", () => {
    afterEach(() => {
        vi.clearAllMocks();
    });

    test("Create & Run with a not-deployed agent hands off to detail page via router state without triggering a run", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(cloneSource);
        mockCreateMutate.mockResolvedValue({ ...cloneSource, id: "created-exp-9" });
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "My Undeployed Agent", deploymentStatus: "not_deployed", agentId: "agent-id-42" }]);

        renderPage();

        await waitFor(() => expect(mockGetExperiment).toHaveBeenCalledWith("source-exp-1"));
        const createAndRunButton = await screen.findByRole("button", { name: /Create & Run/i });
        await waitFor(() => expect(createAndRunButton).toBeEnabled());

        await user.click(createAndRunButton);

        await waitFor(() => expect(mockCreateMutate).toHaveBeenCalledTimes(1));

        expect(mockCreateMutate.mock.calls[0][0]).toMatchObject({
            name: "Source Experiment (copy)",
            description: "Cloned experiment",
            targetAgent: "agent-broker-name",
            datasetId: "ds-1",
            evaluatorIds: ["ev-1"],
            primaryEvaluatorId: "ev-1",
            runsPerExample: 2,
            maxWorkers: 1,
        });

        await waitFor(() => {
            expect(screen.getByTestId("location").textContent).toBe("/evaluations/experiments/created-exp-9");
        });
        expect(screen.getByTestId("agent-not-deployed-state").textContent).toBe("1");
        expect(mockTriggerRun).not.toHaveBeenCalled();
    });

    test("a clone source without maxWorkers sends the default of 4", async () => {
        const user = userEvent.setup();
        const { maxWorkers: _omitted, ...sourceWithoutMaxWorkers } = cloneSource;
        mockGetExperiment.mockResolvedValue(sourceWithoutMaxWorkers);
        mockCreateMutate.mockResolvedValue({ ...cloneSource, id: "created-exp-2" });
        mockTriggerRun.mockResolvedValue([]);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "My Deployed Agent", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        renderPage();

        const createAndRunButton = await screen.findByRole("button", { name: /Create & Run/i });
        await waitFor(() => expect(createAndRunButton).toBeEnabled());
        await user.click(createAndRunButton);

        await waitFor(() => expect(mockCreateMutate).toHaveBeenCalledTimes(1));
        expect(mockCreateMutate.mock.calls[0][0]).toMatchObject({ maxWorkers: 4 });
    });

    test("an out-of-range Max Workers shows the error and blocks creating", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(cloneSource);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "My Deployed Agent", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        renderPage();

        const createAndRunButton = await screen.findByRole("button", { name: /Create & Run/i });
        await waitFor(() => expect(createAndRunButton).toBeEnabled());
        await user.click(screen.getByText("Advanced Options"));
        const field = screen.getByRole("spinbutton", { name: "Max Workers" });
        await user.clear(field);
        await user.type(field, "21");

        expect(screen.getByText("Max Workers must be a whole number from 1 to 20.")).toBeInTheDocument();
        expect(createAndRunButton).toBeDisabled();
    });

    test("Create & Run with a deployed agent triggers the run normally (no handoff state)", async () => {
        const user = userEvent.setup();
        mockGetExperiment.mockResolvedValue(cloneSource);
        mockCreateMutate.mockResolvedValue({ ...cloneSource, id: "created-exp-1" });
        mockTriggerRun.mockResolvedValue([]);
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "My Deployed Agent", deploymentStatus: "deployed", agentId: "agent-id-1" }]);

        renderPage();

        await waitFor(() => expect(mockGetExperiment).toHaveBeenCalled());
        const createAndRunButton = await screen.findByRole("button", { name: /Create & Run/i });
        await waitFor(() => expect(createAndRunButton).toBeEnabled());

        await user.click(createAndRunButton);

        await waitFor(() => expect(mockTriggerRun).toHaveBeenCalledWith("created-exp-1", {}));
        expect(screen.getByTestId("agent-not-deployed-state").textContent).toBe("0");
    });
});

describe("EvalCreateExperimentPage — model selection survives a model-list refetch", () => {
    afterEach(() => {
        vi.clearAllMocks();
        mockUseAgentActiveModel.mockImplementation(() => ({ activeModelId: undefined, activeModel: undefined }));
        mockUseModelConfigs.mockImplementation(() => ({ data: [], isLoading: false, error: null, refetch: vi.fn() }));
    });

    test("adding a second model is not wiped when the active-model object identity churns (regression guard)", async () => {
        const user = userEvent.setup();
        const [firstModel, secondModel] = mockModels;
        mockUseModelConfigs.mockImplementation(() => ({ data: mockModels, isLoading: false, error: null, refetch: vi.fn().mockResolvedValue({ data: mockModels }) }));
        mockUseAgentActiveModel.mockImplementation(() => ({
            activeModelId: firstModel.id,
            activeModel: { modelConfigId: firstModel.id, model: firstModel.modelName ?? "", alias: firstModel.alias },
        }));
        mockAgentOptions.mockReturnValue([{ name: "agent-broker-name", displayName: "Deployed Agent" }]);

        renderPage("/evaluations/experiments/new");

        await waitFor(() => expect(screen.getByText(/\(1\/3\)/)).toBeInTheDocument());

        await user.click(screen.getAllByText(firstModel.alias)[0]);
        await user.click(await screen.findByText(secondModel.alias));

        await waitFor(() => expect(screen.getByText(/\(2\/3\)/)).toBeInTheDocument());
        await new Promise(resolve => setTimeout(resolve, 0));
        await new Promise(resolve => setTimeout(resolve, 0));
        expect(screen.getByText(/\(2\/3\)/)).toBeInTheDocument();
    });
});
