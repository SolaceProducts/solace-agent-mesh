// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { evaluationsApi } from "@/api/evaluations";

import type { EvalDataset } from "@/types/eval";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { AiGenerationDialog } from "./AiGenerationDialog";

vi.mock("@/features/entrypoints/hooks", async importOriginal => ({ ...(await importOriginal<typeof import("@/features/entrypoints/hooks")>()), useAgentOptions: () => [] }));

describe("AiGenerationDialog", () => {
    afterEach(() => vi.restoreAllMocks());

    test("generates examples and tells the page to refresh", async () => {
        const generate = vi.spyOn(evaluationsApi, "generateExamples").mockResolvedValue();
        const onGenerated = vi.fn();

        renderWithProviders(<AiGenerationDialog open onClose={vi.fn()} mode="examples" datasetId="ds-1" existingCount={0} defaultTargetAgent="Orchestrator" onGenerated={onGenerated} />);
        fireEvent.click(screen.getByRole("button", { name: "Generate" }));

        await waitFor(() => expect(onGenerated).toHaveBeenCalledWith());
        expect(generate).toHaveBeenCalledWith("ds-1", { targetAgent: "Orchestrator", numExamples: 3, focus: undefined });
    });

    test("generates a dataset", async () => {
        const generate = vi.spyOn(evaluationsApi, "generateDataset").mockResolvedValue({ id: "ds-9" } as EvalDataset);
        const onGenerated = vi.fn();

        renderWithProviders(<AiGenerationDialog open onClose={vi.fn()} mode="dataset" defaultTargetAgent="Orchestrator" onGenerated={onGenerated} />);
        fireEvent.click(screen.getByRole("button", { name: "Generate" }));

        await waitFor(() => expect(onGenerated).toHaveBeenCalledWith({ id: "ds-9" }));
        expect(generate).toHaveBeenCalledWith({ targetAgent: "Orchestrator", numExamples: 10, focus: undefined });
    });

    test("shows why a generation failed", async () => {
        vi.spyOn(evaluationsApi, "generateExamples").mockRejectedValue(new Error("This dataset is already generating examples. Wait for it to finish."));
        const onGenerated = vi.fn();

        renderWithProviders(<AiGenerationDialog open onClose={vi.fn()} mode="examples" datasetId="ds-1" existingCount={0} defaultTargetAgent="Orchestrator" onGenerated={onGenerated} />);
        fireEvent.click(screen.getByRole("button", { name: "Generate" }));

        await expect(screen.findByText("This dataset is already generating examples. Wait for it to finish.")).resolves.toBeInTheDocument();
        expect(onGenerated).not.toHaveBeenCalled();
    });
});
