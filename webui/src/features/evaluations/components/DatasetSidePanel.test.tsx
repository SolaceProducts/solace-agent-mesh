// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import type { EvalDataset, EvalExample } from "@/types/eval";

import { DatasetSidePanel } from "./DatasetSidePanel";

const pageOfExamples: EvalExample[] = [{ id: "ex-1", datasetId: "ds-1", sequenceNumber: 1, prompt: "What is 2 + 2?", createdAt: "", updatedAt: "" }];

vi.mock("../hooks", async importActual => {
    const actual = await importActual<typeof import("../hooks")>();
    return {
        ...actual,
        useEvalExamplesPaginated: () => ({ examples: pageOfExamples, loading: false, pageNumber: 1, setPageNumber: vi.fn(), totalPages: 2, totalCount: 6, contentFilter: "", setContentFilter: vi.fn() }),
    };
});

function dataset(inputDataExampleCount: number): EvalDataset {
    return { id: "ds-1", name: "Mixed", description: "", exampleCount: 6, experimentCount: 0, inputDataExampleCount, createdAt: "", updatedAt: "", createdBy: "u", updatedBy: "u" };
}

function renderPanel(inputDataExampleCount: number) {
    return render(
        <MemoryRouter>
            <DatasetSidePanel dataset={dataset(inputDataExampleCount)} onClose={vi.fn()} previewMode />
        </MemoryRouter>
    );
}

describe("DatasetSidePanel columns", () => {
    test("follow the whole dataset, not the page: input data heads the column even on a page with none", () => {
        renderPanel(3);

        expect(screen.getByText("Input data")).toBeTruthy();
        expect(screen.getByText("Input File")).toBeTruthy();
        expect(screen.getByText("Expected File")).toBeTruthy();
    });

    test("a dataset with no input data keeps the prompt column and the file columns", () => {
        renderPanel(0);

        expect(screen.getByText("Prompt")).toBeTruthy();
        expect(screen.queryByText("Input data")).toBeNull();
        expect(screen.getByText("Input File")).toBeTruthy();
    });
});
