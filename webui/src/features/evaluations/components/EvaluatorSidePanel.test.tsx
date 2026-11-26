// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import type { ModelConfig } from "@/api/models/types";

import type { EvalEvaluator } from "@/types/eval";

import { StoryProvider } from "@/testing/providers/StoryProvider";

vi.mock("@/api/models/hooks", async () => {
    const actual = await vi.importActual<Record<string, unknown>>("@/api/models/hooks");
    return { ...actual, useModelConfigs: () => ({ data: [] as ModelConfig[] }) };
});

import { EvaluatorSidePanel } from "./EvaluatorSidePanel";

const judge: EvalEvaluator = {
    id: "ev-1",
    name: "Factuality",
    description: "Checks facts",
    type: "llm_judge",
    isDefault: true,
    promptTemplate: "grade {{Response}}",
    experimentCount: 0,
    createdBy: "system",
    updatedBy: "system",
    createdAt: "",
    updatedAt: "",
};

function renderPanel(evaluator: EvalEvaluator) {
    render(
        <StoryProvider>
            <MemoryRouter>
                <EvaluatorSidePanel evaluator={evaluator} onClose={vi.fn()} />
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("EvaluatorSidePanel", () => {
    test("a built-in judge with no model of its own shows the general model as its default and can be opened", () => {
        renderPanel(judge);
        expect(screen.getByText("Default (general model)")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Open Evaluator" })).toBeInTheDocument();
    });

    test("a built-in judge whose model matches no configuration shows that model, not the default", () => {
        renderPanel({ ...judge, model: "retired-model" });
        expect(screen.getByText("retired-model")).toBeInTheDocument();
        expect(screen.queryByText("Default (general model)")).toBeNull();
    });

    test("a custom judge with no model says none is configured", () => {
        renderPanel({ ...judge, isDefault: false });
        expect(screen.getByText("No model configured")).toBeInTheDocument();
    });

    test("a built-in heuristic cannot be opened", () => {
        renderPanel({ ...judge, type: "heuristic", heuristicType: "valid_json", promptTemplate: undefined });
        expect(screen.queryByRole("button", { name: "Open Evaluator" })).toBeNull();
    });
});
