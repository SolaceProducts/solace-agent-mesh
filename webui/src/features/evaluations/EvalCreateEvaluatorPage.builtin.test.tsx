// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { ModelConfig } from "@/api/models/types";

import type { Capability } from "@/providers/contexts/UserContext";
import { CAPABILITIES } from "@/providers/contexts/UserContext";

import type { EvalEvaluator, EvalExperiment } from "@/types/eval";

import { anthropicModelConfig } from "@/testing/data/models";
import { StoryProvider } from "@/testing/providers/StoryProvider";

const mockGetEvaluator = vi.fn<() => Promise<EvalEvaluator>>();
const mockUpdate = vi.fn((args: { id: string; data: Record<string, unknown> }) => Promise.resolve({ ...builtin, model: (args.data.model as string | null) ?? undefined }));

vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return { ...actual, useLoaderData: () => ({ evaluatorId: "ev-builtin" }) };
});

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations");
    return {
        ...actual,
        useCreateEvaluator: () => ({ mutateAsync: vi.fn(), isPending: false }),
        useUpdateEvaluator: () => ({ mutateAsync: mockUpdate, isPending: false, reset: vi.fn() }),
        useDeleteEvaluator: () => ({ mutateAsync: vi.fn(), isPending: false }),
    };
});

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: { ...actual.evaluationsApi, getEvaluator: () => mockGetEvaluator(), getExperiments: () => Promise.resolve([] as EvalExperiment[]) },
    };
});

vi.mock("@/api/models/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/models/hooks");
    const models = [anthropicModelConfig] as ModelConfig[];
    return { ...actual, useModelConfigs: () => ({ data: models }), useChatModelConfigs: () => ({ data: models, isLoading: false }) };
});

import { EvalCreateEvaluatorPage } from "./EvalCreateEvaluatorPage";

const builtin: EvalEvaluator = {
    id: "ev-builtin",
    name: "Factuality",
    description: "Checks facts",
    type: "llm_judge",
    isDefault: true,
    promptTemplate: "grade {{Response}}",
    choiceScores: { Yes: 1, No: 0 },
    experimentCount: 0,
    createdBy: "system",
    updatedBy: "system",
    createdAt: "",
    updatedAt: "",
};

function renderPage(evaluator: EvalEvaluator, canUpdate = true) {
    mockGetEvaluator.mockResolvedValue(evaluator);
    const held: Capability[] = canUpdate ? [CAPABILITIES.updateEvaluations, CAPABILITIES.createEvaluations] : [CAPABILITIES.createEvaluations];
    return render(
        <StoryProvider userContextValues={{ userHasCapability: capability => held.includes(capability) }}>
            <MemoryRouter initialEntries={["/evaluations/evaluators/ev-builtin"]}>
                <Routes>
                    <Route path="/evaluations/evaluators/:evaluatorId" element={<EvalCreateEvaluatorPage />} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

afterEach(() => {
    vi.clearAllMocks();
});

describe("a built-in LLM judge", () => {
    test("shows the general model as its default and offers Edit Model to an updater", async () => {
        renderPage(builtin);
        expect(await screen.findByText("Default (general model)")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Edit Model/ })).toBeInTheDocument();
    });

    test("offers no Edit Model without the update capability", async () => {
        renderPage(builtin, false);
        await screen.findByText("Default (general model)");
        expect(screen.queryByRole("button", { name: /Edit Model/ })).toBeNull();
    });

    test("edits only the model, in a dialog, and saves only the model", async () => {
        const user = userEvent.setup();
        renderPage(builtin);
        await user.click(await screen.findByRole("button", { name: /Edit Model/ }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Edit Model")).toBeInTheDocument();
        expect(within(dialog).queryByRole("textbox")).toBeNull();
        expect(screen.queryByDisplayValue("Factuality")).toBeNull();

        await user.click(within(dialog).getByTestId("modelSelectTrigger"));
        await user.click(await screen.findByText(anthropicModelConfig.alias));
        await user.click(within(dialog).getByRole("button", { name: "Save" }));

        await waitFor(() => expect(mockUpdate).toHaveBeenCalledWith({ id: "ev-builtin", data: { model: anthropicModelConfig.id } }));
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
        expect(screen.getByText(anthropicModelConfig.alias)).toBeInTheDocument();
    });

    test("removing its model saves a reset to the general model", async () => {
        const user = userEvent.setup();
        renderPage({ ...builtin, model: anthropicModelConfig.id });
        await user.click(await screen.findByRole("button", { name: /Edit Model/ }));

        const dialog = await screen.findByRole("dialog");
        await user.click(within(dialog).getByTitle("Remove"));
        expect(within(dialog).getByText("Default (general model)")).toBeInTheDocument();
        await user.click(within(dialog).getByRole("button", { name: "Save" }));

        await waitFor(() => expect(mockUpdate).toHaveBeenCalledWith({ id: "ev-builtin", data: { model: null } }));
    });

    test("a model that matches no configuration is shown as set and can still be removed", async () => {
        const user = userEvent.setup();
        renderPage({ ...builtin, model: "retired-model" });
        await user.click(await screen.findByRole("button", { name: /Edit Model/ }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByTestId("modelSelectTrigger")).toHaveTextContent("retired-model");
        await user.click(within(dialog).getByTitle("Remove"));
        await user.click(within(dialog).getByRole("button", { name: "Save" }));

        await waitFor(() => expect(mockUpdate).toHaveBeenCalledWith({ id: "ev-builtin", data: { model: null } }));
    });

    test("a built-in heuristic stays read-only", async () => {
        renderPage({ ...builtin, type: "heuristic", heuristicType: "valid_json", promptTemplate: undefined, choiceScores: undefined });
        await screen.findByText("Factuality");
        expect(screen.queryByRole("button", { name: /Edit/ })).toBeNull();
    });
});
