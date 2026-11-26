// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import type { EvalExperiment, EvalRunGroup, EvalRunResult } from "@/types/eval";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const mockResults = vi.fn<() => EvalRunResult[]>(() => []);

vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return { ...actual, useLoaderData: () => ({ groupId: "group-1", exampleKey: "ex-1" }) };
});

const workflowExperiment = {
    id: "exp-1",
    name: "Order flow eval",
    targetType: "workflow",
    targetAgent: "",
    targetWorkflow: "order_flow",
    models: [],
    evaluatorIds: ["ev-1"],
    runsPerExample: 1,
    createdBy: "u",
    updatedBy: "u",
    createdAt: "",
    updatedAt: "",
} as unknown as EvalExperiment;

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return { ...actual, evaluationsApi: { ...actual.evaluationsApi, getExperiment: () => Promise.resolve(workflowExperiment) } };
});

const group = {
    id: "group-1",
    experimentId: "exp-1",
    runName: "Nightly",
    status: "completed",
    progressCurrent: 1,
    progressTotal: 1,
    startedAt: "2026-01-01T00:00:00Z",
    runs: [{ id: "run-1", model: "", status: "completed", evaluatorSnapshot: [{ id: "ev-1", name: "Output Facts", type: "heuristic", heuristic_type: "output_facts" }] }],
} as unknown as EvalRunGroup;

vi.mock("@/features/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/evaluations/hooks");
    return {
        ...actual,
        useEvalRunGroup: () => ({ group, loading: false, isActive: false }),
        useEvalRunGroupResults: () => ({ results: { groupId: "group-1", resultsByModel: { "": mockResults() } }, loading: false }),
        useEvalModelAxis: () => ({ effectiveModels: [], allModels: [""] }),
        useResizableSidePanel: () => ({ containerRef: { current: null }, width: 320, handleDragStart: vi.fn() }),
    };
});

import { EvalExampleDetailPage } from "./EvalExampleDetailPage";

function renderPage() {
    return render(
        <StoryProvider>
            <MemoryRouter>
                <EvalExampleDetailPage />
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvalExampleDetailPage for a workflow example", () => {
    test("shows the input data and the evaluator instructions, and why the trial ended early", async () => {
        mockResults.mockReturnValue([
            {
                id: "r1",
                runId: "run-1",
                exampleId: "ex-1",
                trialIndex: 0,
                targetType: "workflow",
                inputPrompt: 'Damaged item\n\nInput: {"orderId":"O-1"}',
                failureKind: "node_failed",
                errorMessage: 'Workflow "order_flow" failed: node "lookup" failed: boom',
                scores: [],
                createdAt: "",
            },
        ]);
        renderPage();

        expect(await screen.findByText("Input Data")).toBeInTheDocument();
        expect(screen.getByText("Evaluator Instructions")).toBeInTheDocument();
        expect(screen.getByText("Damaged item")).toBeInTheDocument();
        expect(screen.getByTestId("trialEndedEarly")).toHaveTextContent("The lookup step failed.");
        expect(screen.getAllByText("ended early").length).toBeGreaterThan(0);
    });
});
