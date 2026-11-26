// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import type { EvalDataset, EvalExample } from "@/types/eval";

import { StoryProvider } from "@/testing/providers/StoryProvider";

const mockGetDataset = vi.fn<() => Promise<EvalDataset>>();

vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return { ...actual, useLoaderData: () => ({ datasetId: "ds-1" }) };
});

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return { ...actual, evaluationsApi: { ...actual.evaluationsApi, getDataset: () => mockGetDataset() } };
});

const pageWithoutInputData: EvalExample[] = [{ id: "ex-1", datasetId: "ds-1", sequenceNumber: 1, prompt: "What is 2 + 2?", createdAt: "", updatedAt: "" }];

vi.mock("@/features/evaluations/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/features/evaluations/hooks");
    return {
        ...actual,
        useEvalExamplesPaginated: () => ({ examples: pageWithoutInputData, loading: false, pageNumber: 1, setPageNumber: vi.fn(), totalPages: 2, totalCount: 11, contentFilter: "", setContentFilter: vi.fn(), fetchExamples: vi.fn() }),
    };
});

import { EvalDatasetDetailPage } from "./EvalDatasetDetailPage";

function dataset(inputDataExampleCount: number): EvalDataset {
    return { id: "ds-1", name: "Orders", description: "", exampleCount: 11, experimentCount: 0, inputDataExampleCount, createdAt: "", updatedAt: "", createdBy: "u", updatedBy: "u" };
}

function renderPage() {
    return render(
        <StoryProvider>
            <MemoryRouter>
                <EvalDatasetDetailPage />
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvalDatasetDetailPage columns", () => {
    test("follow the whole dataset: a page with no input data still reads as a workflow dataset", async () => {
        mockGetDataset.mockResolvedValue(dataset(3));
        renderPage();

        expect(await screen.findByText("Prompt or Input")).toBeInTheDocument();
        expect(screen.getByText(/or a JSON input for a workflow/)).toBeInTheDocument();
    });

    test("a dataset with no input data keeps the agent wording", async () => {
        mockGetDataset.mockResolvedValue(dataset(0));
        renderPage();

        expect(await screen.findByText("What is 2 + 2?")).toBeInTheDocument();
        expect(screen.queryByText("Prompt or Input")).not.toBeInTheDocument();
        expect(screen.getByText(/each has a prompt \(required\)/)).toBeInTheDocument();
    });
});
