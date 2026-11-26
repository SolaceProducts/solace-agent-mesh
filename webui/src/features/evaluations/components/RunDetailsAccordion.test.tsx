// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { userEvent } from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { EvalRun, EvalRunGroup } from "@/types/eval";

import { RunDetailsAccordion } from "@/features/evaluations/components/RunDetailsAccordion";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

vi.mock("@/api/models/hooks", async importActual => {
    const actual = await importActual<typeof import("@/api/models/hooks")>();
    return { ...actual, useModelConfigs: () => ({ data: [] }) };
});

function group(run: Partial<EvalRun>): EvalRunGroup {
    return {
        id: "group-1",
        experimentId: "exp-1",
        runName: "Nightly",
        status: "completed",
        progressCurrent: 1,
        progressTotal: 1,
        startedAt: "2026-01-01T00:00:00Z",
        runs: [{ id: "run-1", experimentId: "exp-1", runGroupId: "group-1", status: "completed", progressCurrent: 1, progressTotal: 1, startedAt: "2026-01-01T00:00:00Z", model: "", ...run } as EvalRun],
    } as EvalRunGroup;
}

describe("RunDetailsAccordion", () => {
    test("a workflow run names its workflow, not an empty agent, and lists no model of its own", async () => {
        renderWithProviders(<RunDetailsAccordion group={group({ configSnapshot: { experiment: { name: "Flow eval" }, workflow: { name: "order_flow" } } })} />);
        await userEvent.click(screen.getByText("Report Details:"));

        expect(screen.getByTestId("runDetailsWorkflow")).toHaveTextContent("Workflow");
        expect(screen.getByTestId("runDetailsWorkflow")).toHaveTextContent("order_flow");
        expect(screen.queryByText("Agent")).toBeNull();
        expect(screen.queryByRole("button", { name: "" })).toBeNull();
    });

    test("an agent run keeps its agent link", async () => {
        renderWithProviders(<RunDetailsAccordion group={group({ model: "cfg-1", configSnapshot: { experiment: { name: "Agent eval" }, agent: { name: "Orchestrator" } } })} />);
        await userEvent.click(screen.getByText("Report Details:"));

        expect(screen.getByText("Agent")).toBeTruthy();
        expect(screen.getByRole("button", { name: "Orchestrator" })).toBeTruthy();
        expect(screen.queryByTestId("runDetailsWorkflow")).toBeNull();
    });
});
