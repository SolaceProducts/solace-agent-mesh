// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor, within } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { EvalDataset, EvalExample } from "@/types/eval";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const calls: string[] = [];
const createExample = vi.fn();
const updateExample = vi.fn();
const uploadExampleArtifact = vi.fn();
const deleteExample = vi.fn();
const deleteExampleArtifact = vi.fn();
const fetchExamples = vi.fn().mockResolvedValue(undefined);
const getExampleArtifactBlob = vi.fn();
const downloadBlob = vi.fn();

let examples: EvalExample[] = [];

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: {
            ...actual.evaluationsApi,
            getDataset: vi.fn().mockResolvedValue({ id: "ds-1", name: "Fixture", exampleCount: 0 }),
            createExample: (...args: unknown[]) => {
                calls.push("createExample");
                return createExample(...args);
            },
            uploadExampleArtifact: (...args: unknown[]) => {
                calls.push("uploadExampleArtifact");
                return uploadExampleArtifact(...args);
            },
            deleteExample: (...args: unknown[]) => {
                calls.push("deleteExample");
                return deleteExample(...args);
            },
            deleteExampleArtifact: (...args: unknown[]) => deleteExampleArtifact(...args),
            updateExample: (...args: unknown[]) => updateExample(...args),
            getExampleArtifactBlob: (...args: unknown[]) => getExampleArtifactBlob(...args),
        },
    };
});

vi.mock("@/utils/download", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/utils/download");
    return { ...actual, downloadBlob: (...args: unknown[]) => downloadBlob(...args) };
});

vi.mock("@/features/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/evaluations/hooks");
    return {
        ...actual,
        useEvalExamplesPaginated: () => ({
            examples,
            loading: false,
            pageNumber: 1,
            setPageNumber: vi.fn(),
            totalPages: 1,
            totalCount: examples.length,
            contentFilter: "",
            setContentFilter: vi.fn(),
            fetchExamples,
        }),
    };
});

import { DatasetFormBody } from "./DatasetFormBody";

const dataset: EvalDataset = {
    id: "ds-1",
    name: "Fixture",
    description: "d",
    exampleCount: 0,
    experimentCount: 0,
    createdAt: "2026-01-01T00:00:00.000Z",
    updatedAt: "2026-01-01T00:00:00.000Z",
    createdBy: "tester",
    updatedBy: "tester",
};

function renderBody() {
    return render(
        <StoryProvider>
            <DatasetFormBody datasetId="ds-1" dataset={dataset} onDatasetChange={vi.fn()} name="Fixture" onNameChange={vi.fn()} description="d" onDescriptionChange={vi.fn()} />
        </StoryProvider>
    );
}

async function startNewExample(prompt: string) {
    await userEvent.click(screen.getByRole("button", { name: "+ Add Example" }));
    await userEvent.type(screen.getByPlaceholderText("Enter prompt..."), prompt);
}

beforeEach(() => {
    calls.length = 0;
    examples = [];
    createExample.mockReset().mockResolvedValue({ id: "ex-new" });
    uploadExampleArtifact.mockReset().mockResolvedValue({ filename: "config.yaml", version: 0 });
    deleteExample.mockReset().mockResolvedValue(undefined);
    deleteExampleArtifact.mockReset().mockResolvedValue(undefined);
    fetchExamples.mockClear();
    getExampleArtifactBlob.mockReset().mockResolvedValue(new Blob(["name: x"]));
    downloadBlob.mockReset();
});

const exampleWithFile: EvalExample[] = [
    {
        id: "ex-1",
        datasetId: "ds-1",
        sequenceNumber: 1,
        prompt: "has a file",
        inputArtifact: { filename: "spec.yaml", version: 3 },
        createdAt: "2026-01-01T00:00:00.000Z",
        updatedAt: "2026-01-01T00:00:00.000Z",
    } as EvalExample,
];

describe("DatasetFormBody workflow input", () => {
    function renderWithWorkflowEvals() {
        return render(
            <StoryProvider flagValues={{ workflow_evals: true }}>
                <DatasetFormBody datasetId="ds-1" dataset={dataset} onDatasetChange={vi.fn()} name="Fixture" onNameChange={vi.fn()} description="d" onDescriptionChange={vi.fn()} />
            </StoryProvider>
        );
    }

    test("creates an example from its input alone", async () => {
        renderWithWorkflowEvals();
        await userEvent.click(screen.getByRole("button", { name: "+ Add Example" }));
        await userEvent.type(screen.getByLabelText("Input data (JSON)"), '{{"orderId":"O-5003"}');

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(createExample).toHaveBeenCalledWith("ds-1", { prompt: "", expectedResponse: undefined, inputData: { orderId: "O-5003" } }));
    });

    test("creates an example with its expected path", async () => {
        renderWithWorkflowEvals();
        await userEvent.click(screen.getByRole("button", { name: "+ Add Example" }));
        await userEvent.type(screen.getByLabelText("Input data (JSON)"), '{{"orderId":"O-1"}');
        await userEvent.type(screen.getByLabelText("Expected steps (JSON)"), '{{"nodes":{{"approve":"ran"}}');

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(createExample).toHaveBeenCalledWith("ds-1", expect.objectContaining({ inputData: { orderId: "O-1" }, expectedPath: { nodes: { approve: "ran" } } })));
    });

    test("refuses an expected path the evaluator cannot check", async () => {
        renderWithWorkflowEvals();
        await userEvent.click(screen.getByRole("button", { name: "+ Add Example" }));
        await userEvent.type(screen.getByLabelText("Input data (JSON)"), '{{"orderId":"O-1"}');
        await userEvent.type(screen.getByLabelText("Expected steps (JSON)"), '{{"nodes":{{"approve":"maybe"}}');

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText(/Node "approve" must be one of/)).toBeTruthy();
        expect(createExample).not.toHaveBeenCalled();
    });

    test("refuses an input that is not a JSON object", async () => {
        renderWithWorkflowEvals();
        await userEvent.click(screen.getByRole("button", { name: "+ Add Example" }));
        await userEvent.type(screen.getByLabelText("Input data (JSON)"), "[[1]");

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        expect(await screen.findByText(/JSON object/)).toBeTruthy();
        expect(createExample).not.toHaveBeenCalled();
    });

    test("saves an example's expected values inline, and clears a corrected JSON error", async () => {
        updateExample.mockReset().mockResolvedValue({});
        examples = [{ id: "ex-1", datasetId: "ds-1", sequenceNumber: 1, prompt: "", inputData: { orderId: "O-1" }, expectedFacts: { required: ["refund"] }, createdAt: "", updatedAt: "" } as EvalExample];
        renderWithWorkflowEvals();

        await userEvent.click(screen.getByText('{"required":["refund"]}'));
        const editor = screen.getByDisplayValue('{"required":["refund"]}');
        await userEvent.clear(editor);
        await userEvent.type(editor, "[[1]");
        await userEvent.keyboard("{Enter}");
        expect(await screen.findByText(/JSON object/)).toBeInTheDocument();

        const again = screen.getByDisplayValue("[1]");
        await userEvent.clear(again);
        await userEvent.type(again, '{{"required":[["approved"]}');
        await userEvent.keyboard("{Enter}");

        await waitFor(() => expect(updateExample).toHaveBeenCalledWith("ex-1", { expectedFacts: { required: ["approved"] } }));
        expect(screen.queryByText(/JSON object/)).not.toBeInTheDocument();
    });

    test("is not offered with the flag off", async () => {
        renderBody();
        expect(screen.getByText(/each has a prompt \(required\) and an optional expected response/)).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "+ Add Example" }));
        expect(screen.queryByLabelText("Input data (JSON)")).toBeNull();
        expect(screen.queryByText("Prompt or Input")).toBeNull();
    });
});

describe("DatasetFormBody input file column", () => {
    test("uploads the attached file only after the example exists", async () => {
        renderBody();
        await startNewExample("summarize this config");
        await userEvent.upload(screen.getByLabelText("Attach input file") as HTMLInputElement, new File(["name: x"], "config.yaml", { type: "application/yaml" }));

        expect(calls).toEqual([]);

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(calls).toEqual(["createExample", "uploadExampleArtifact"]));
        expect(uploadExampleArtifact).toHaveBeenCalledWith("ex-new", "input", expect.objectContaining({ name: "config.yaml" }));
    });

    test("deletes the example when the upload fails", async () => {
        uploadExampleArtifact.mockRejectedValue(new Error("file exceeds the 10000000 byte limit"));
        renderBody();
        await startNewExample("summarize this config");
        await userEvent.upload(screen.getByLabelText("Attach input file") as HTMLInputElement, new File(["x"], "big.bin"));

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(calls).toEqual(["createExample", "uploadExampleArtifact", "deleteExample"]));
        expect(deleteExample).toHaveBeenCalledWith("ex-new");
        expect(screen.getByText(/file exceeds the 10000000 byte limit/)).toBeTruthy();
    });

    test("routes an expected-file rejection to the expected slot", async () => {
        uploadExampleArtifact.mockImplementation((_id: unknown, role: unknown) => (role === "expected" ? Promise.reject(new Error("Only text files can be graded")) : Promise.resolve({ filename: "config.yaml", version: 0 })));
        renderBody();
        await startNewExample("summarize this config");
        await userEvent.upload(screen.getByLabelText("Attach expected file") as HTMLInputElement, new File(["ref"], "reference.txt"));

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        const alert = await screen.findByText("Only text files can be graded");
        const cell = alert.closest("td");
        expect(cell).toBeTruthy();
        expect(within(cell as HTMLElement).getByLabelText("Replace expected file")).toBeTruthy();
    });

    test("creates a text-only example with no upload call", async () => {
        renderBody();
        await startNewExample("no file here");

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(calls).toEqual(["createExample"]));
    });

    test("removing an existing file calls the delete route and refreshes", async () => {
        examples = [
            {
                id: "ex-1",
                datasetId: "ds-1",
                sequenceNumber: 1,
                prompt: "has a file",
                inputArtifact: { filename: "spec.yaml", version: 0 },
                createdAt: "2026-01-01T00:00:00.000Z",
                updatedAt: "2026-01-01T00:00:00.000Z",
            },
        ];
        renderBody();

        await userEvent.click(screen.getByRole("button", { name: /remove/i }));

        await waitFor(() => expect(deleteExampleArtifact).toHaveBeenCalledWith("ex-1", "input"));
        expect(fetchExamples).toHaveBeenCalled();
    });

    test("attaching an expected file uploads under the expected role", async () => {
        examples = [{ id: "ex-6", datasetId: "ds-1", sequenceNumber: 1, prompt: "no files", createdAt: "2026-01-01T00:00:00.000Z", updatedAt: "2026-01-01T00:00:00.000Z" } as EvalExample];
        renderBody();

        await userEvent.upload(screen.getByLabelText("Attach expected file") as HTMLInputElement, new File(["ref"], "ref.md", { type: "text/markdown" }));

        await waitFor(() => expect(uploadExampleArtifact).toHaveBeenCalledWith("ex-6", "expected", expect.any(File)));
        expect(fetchExamples).toHaveBeenCalled();
    });

    test("uploads both draft files after the example exists", async () => {
        renderBody();
        await startNewExample("carry both");
        await userEvent.upload(screen.getByLabelText("Attach input file") as HTMLInputElement, new File(["name: x"], "config.yaml", { type: "application/yaml" }));
        await userEvent.upload(screen.getByLabelText("Attach expected file") as HTMLInputElement, new File(["ref"], "ref.md", { type: "text/markdown" }));

        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(calls).toEqual(["createExample", "uploadExampleArtifact", "uploadExampleArtifact"]));
        expect(uploadExampleArtifact).toHaveBeenNthCalledWith(1, "ex-new", "input", expect.objectContaining({ name: "config.yaml" }));
        expect(uploadExampleArtifact).toHaveBeenNthCalledWith(2, "ex-new", "expected", expect.objectContaining({ name: "ref.md" }));
    });

    test("attaching to an existing row uploads against that row and refreshes", async () => {
        examples = [{ id: "ex-5", datasetId: "ds-1", sequenceNumber: 1, prompt: "no file yet", createdAt: "2026-01-01T00:00:00.000Z", updatedAt: "2026-01-01T00:00:00.000Z" } as EvalExample];
        renderBody();

        await userEvent.upload(screen.getByLabelText("Attach input file") as HTMLInputElement, new File(["name: x"], "spec.yaml", { type: "application/yaml" }));

        await waitFor(() => expect(uploadExampleArtifact).toHaveBeenCalledWith("ex-5", "input", expect.any(File)));
        expect(fetchExamples).toHaveBeenCalled();
    });

    test("downloading an existing file asks for the recorded version", async () => {
        examples = exampleWithFile;
        renderBody();

        await userEvent.click(screen.getByText("spec.yaml"));

        await waitFor(() => expect(getExampleArtifactBlob).toHaveBeenCalledWith("ex-1", "input", "spec.yaml", 3));
        await waitFor(() => expect(downloadBlob).toHaveBeenCalledWith(expect.any(Blob), "spec.yaml"));
    });

    test("cancelling the draft row clears the prompt and the held file", async () => {
        renderBody();
        await startNewExample("draft prompt");
        await userEvent.upload(screen.getByLabelText("Attach input file") as HTMLInputElement, new File(["name: x"], "draft.yaml", { type: "application/yaml" }));
        expect(screen.getByText("draft.yaml")).toBeTruthy();

        await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

        expect(screen.queryByText("draft.yaml")).toBeNull();
        expect(createExample).not.toHaveBeenCalled();
    });

    test("the row delete control opens the confirmation naming that example", async () => {
        examples = exampleWithFile;
        renderBody();

        await userEvent.click(screen.getByLabelText("Delete example #1"));

        expect(await screen.findByText(/cannot be undone/i)).toBeTruthy();
    });
});
