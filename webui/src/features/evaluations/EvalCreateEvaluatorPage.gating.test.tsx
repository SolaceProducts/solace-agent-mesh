// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import type { ModelConfig } from "@/api/models/types";

import type { Capability } from "@/providers/contexts/UserContext";
import { CAPABILITIES } from "@/providers/contexts/UserContext";

import type { EvalEvaluator, EvalExperiment } from "@/types/eval";

import { StoryProvider } from "@/testing/providers/StoryProvider";

let loaderData: { evaluatorId?: string } = {};

const mockGetEvaluator = vi.fn<() => Promise<EvalEvaluator>>();
const mockGetExperiments = vi.fn<() => Promise<EvalExperiment[]>>(() => Promise.resolve([]));

vi.mock("react-router-dom", async () => {
    const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
    return { ...actual, useLoaderData: () => loaderData };
});

vi.mock("@/api/evaluations", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/evaluations");
    return {
        ...actual,
        useCreateEvaluator: () => ({ mutateAsync: vi.fn(), isPending: false }),
        useUpdateEvaluator: () => ({ mutateAsync: vi.fn(), isPending: false }),
        useDeleteEvaluator: () => ({ mutateAsync: vi.fn(), isPending: false }),
    };
});

vi.mock("@/api/evaluations/versioned", async () => {
    const actual = await vi.importActual<typeof import("@/api/evaluations/versioned")>("@/api/evaluations/versioned");
    return {
        ...actual,
        evaluationsApi: {
            ...actual.evaluationsApi,
            getEvaluator: () => mockGetEvaluator(),
            getExperiments: () => mockGetExperiments(),
        },
    };
});

vi.mock("@/api/models/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/models/hooks");
    return { ...actual, useModelConfigs: () => ({ data: [] as ModelConfig[] }), useChatModelConfigs: () => ({ data: [] as ModelConfig[] }) };
});

import { EvalCreateEvaluatorPage } from "./EvalCreateEvaluatorPage";

const evaluator: EvalEvaluator = {
    id: "ev-1",
    name: "My Evaluator",
    description: "Judges things",
    type: "llm_judge",
    isDefault: false,
    promptTemplate: "grade this",
    experimentCount: 0,
    createdBy: "u",
    updatedBy: "u",
    createdAt: "",
    updatedAt: "",
};

function EvaluatorsTabProbe() {
    const location = useLocation();
    return <div data-testid="evaluators-tab">{`${location.pathname}${location.search}`}</div>;
}

interface RenderOptions {
    readonly held: readonly Capability[];
    readonly editingIntent?: boolean;
}

function renderPage({ held, editingIntent = false }: RenderOptions) {
    const path = loaderData.evaluatorId ? "/evaluations/evaluators/ev-1" : "/evaluations/evaluators/new";
    const entry = editingIntent ? { pathname: path, state: { editing: true } } : path;
    return render(
        <StoryProvider userContextValues={{ userHasCapability: capability => held.includes(capability) }}>
            <MemoryRouter initialEntries={[entry]}>
                <Routes>
                    <Route path="/evaluations/evaluators/new" element={<EvalCreateEvaluatorPage />} />
                    <Route path="/evaluations/evaluators/:evaluatorId" element={<EvalCreateEvaluatorPage />} />
                    <Route path="/evaluations/lab" element={<EvaluatorsTabProbe />} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvalCreateEvaluatorPage — per-verb gating", () => {
    afterEach(() => {
        loaderData = {};
        vi.clearAllMocks();
    });

    test("a read-only user reaches the evaluator detail view instead of being redirected", async () => {
        loaderData = { evaluatorId: "ev-1" };
        mockGetEvaluator.mockResolvedValue(evaluator);

        renderPage({ held: [CAPABILITIES.readEvaluations] });

        expect(await screen.findByRole("heading", { name: "My Evaluator" })).toBeInTheDocument();
        expect(screen.queryByTestId("evaluators-tab")).not.toBeInTheDocument();
    });

    test("a read-only user is redirected away from the create route", async () => {
        renderPage({ held: [CAPABILITIES.readEvaluations] });

        await waitFor(() => expect(screen.getByTestId("evaluators-tab")).toBeInTheDocument());
        expect(screen.getByTestId("evaluators-tab").textContent).toContain("tab=evaluators");
    });

    test("a read-only user is redirected when deep-linking with editing intent", async () => {
        loaderData = { evaluatorId: "ev-1" };
        mockGetEvaluator.mockResolvedValue(evaluator);

        renderPage({ held: [CAPABILITIES.readEvaluations], editingIntent: true });

        await waitFor(() => expect(screen.getByTestId("evaluators-tab")).toBeInTheDocument());
    });

    test("an update-scoped user keeps editing intent on the detail route", async () => {
        loaderData = { evaluatorId: "ev-1" };
        mockGetEvaluator.mockResolvedValue(evaluator);

        renderPage({ held: [CAPABILITIES.readEvaluations, CAPABILITIES.updateEvaluations], editingIntent: true });

        expect(await screen.findByDisplayValue("My Evaluator")).toBeInTheDocument();
        expect(screen.queryByTestId("evaluators-tab")).not.toBeInTheDocument();
    });
});
