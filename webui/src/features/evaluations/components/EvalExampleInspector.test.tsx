// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { EvalRunResult } from "@/types/eval";

import { EvalExampleInspector } from "@/features/evaluations/components/EvalExampleInspector";
import type { ExampleRow } from "@/features/evaluations/components/ExamplesTable";

vi.mock("@/providers/contexts", async importActual => {
    const actual = await importActual<typeof import("@/providers/contexts")>();
    return { ...actual, useChatContext: () => ({ displayError: vi.fn() }) };
});

const MODEL = "general";

function trial(overrides: Partial<EvalRunResult> = {}): EvalRunResult {
    return {
        id: "res-1",
        runId: "run-1",
        exampleId: "ex-1",
        trialIndex: 0,
        inputPrompt: "Review this Helm values file.",
        scores: [],
        ...overrides,
    } as EvalRunResult;
}

function renderInspector(trials: EvalRunResult[]) {
    const row: ExampleRow = {
        key: "ex-1",
        exampleId: "ex-1",
        prompt: "Review this Helm values file.",
        expectedResponse: "Flags the missing resource limits.",
        perModelTrials: { [MODEL]: trials },
    };
    const client = new QueryClient({ defaultOptions: { queries: { retry: 0 } } });
    return render(
        <QueryClientProvider client={client}>
            <OpenFeatureTestProvider flagValueMap={{}}>
                <EvalExampleInspector row={row} exampleNumber={1} models={[MODEL]} allModels={[MODEL]} evaluators={[]} onClose={vi.fn()} />
            </OpenFeatureTestProvider>
        </QueryClientProvider>
    );
}

describe("EvalExampleInspector input file", () => {
    test("renders the input file in the example details, not per trial", () => {
        renderInspector([trial({ inputArtifact: { filename: "values.yaml", mimeType: "application/x-yaml", version: 0, size: 126 } })]);

        expect(screen.getByText("Input File")).toBeTruthy();
        expect(screen.getByText("values.yaml")).toBeTruthy();
        expect(screen.getAllByText("values.yaml")).toHaveLength(1);
    });

    test("shows the file for a trial that never produced a response", () => {
        renderInspector([trial({ errorMessage: "Run cancelled before task completed", inputArtifact: { filename: "values.yaml", version: 0 } })]);

        expect(screen.getByText("Input File")).toBeTruthy();
        expect(screen.getByText("values.yaml")).toBeTruthy();
    });

    test("dedupes one file across trials but keeps a mid-run replacement distinct", () => {
        renderInspector([
            trial({ id: "res-1", inputArtifact: { filename: "values.yaml", version: 0 } }),
            trial({ id: "res-2", trialIndex: 1, inputArtifact: { filename: "values.yaml", version: 0 } }),
            trial({ id: "res-3", trialIndex: 2, inputArtifact: { filename: "values.yaml", version: 1 } }),
        ]);

        expect(screen.getAllByText("values.yaml")).toHaveLength(2);
    });

    test("clicking the file opens the preview takeover", async () => {
        renderInspector([trial({ inputArtifact: { filename: "values.yaml", mimeType: "application/x-yaml", version: 0, size: 126 } })]);

        await userEvent.click(screen.getByRole("button", { name: "Preview values.yaml" }));

        expect(screen.getByText("Preview")).toBeTruthy();
    });

    test("omits the section entirely for a text-only example", () => {
        renderInspector([trial({ agentResponse: "ok" })]);

        expect(screen.queryByText("Input File")).toBeNull();
    });
});

describe("EvalExampleInspector trial response", () => {
    const openTrial = async () => userEvent.click(screen.getByText("1"));

    test("shows why a workflow trial ended early, as the example page does", async () => {
        renderInspector([trial({ targetType: "workflow", failureKind: "node_failed", errorMessage: 'Workflow "w" failed: node "lookup" failed: boom' })]);
        await openTrial();

        expect(screen.getByTestId("trialEndedEarly")).toHaveTextContent("The lookup step failed.");
    });

    test("warns about a scored workflow trial that lost a step", async () => {
        renderInspector([trial({ targetType: "workflow", failureKind: "partial_failure", agentResponse: '{"ok":true}' })]);
        await openTrial();

        expect(screen.getByTestId("trialPartialFailure")).toBeTruthy();
        expect(screen.getByText('{"ok":true}')).toBeTruthy();
    });
});
