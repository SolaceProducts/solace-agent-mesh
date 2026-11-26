// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import { SharedFlowChartPanel } from "@/features/share/SharedFlowChartPanel";

import type { VisualizerStep } from "@/types";

function makeStep(overrides: Partial<VisualizerStep> = {}): VisualizerStep {
    return {
        id: "step-1",
        type: "USER_REQUEST",
        timestamp: "2025-01-15T10:30:45.123Z",
        title: "Test Step",
        data: {},
        rawEventIds: ["evt-1"],
        nestingLevel: 0,
        owningTaskId: "task-1",
        ...overrides,
    };
}

function renderPanel(steps: VisualizerStep[], agentMap?: Record<string, string>) {
    return render(
        <TooltipProvider>
            <SharedFlowChartPanel processedSteps={steps} agentNameDisplayNameMap={agentMap} />
        </TooltipProvider>
    );
}

describe("SharedFlowChartPanel", () => {
    test("renders 'No events to display' when processedSteps is empty", () => {
        renderPanel([]);
        expect(screen.getByText(/No events to display/)).toBeInTheDocument();
    });

    test("does not show empty message when steps are provided", () => {
        renderPanel([makeStep()]);
        expect(screen.queryByText(/No events to display/)).not.toBeInTheDocument();
    });

    test("shows controls bar with center button and detail mode toggle", () => {
        renderPanel([makeStep()]);
        expect(screen.getByRole("button", { name: /center workflow/i })).toBeInTheDocument();
        expect(screen.getByText("Detail Mode")).toBeInTheDocument();
        expect(screen.getByRole("switch")).toBeInTheDocument();
    });

    test("detail mode toggle is initially checked (on)", () => {
        renderPanel([makeStep()]);
        const toggle = screen.getByRole("switch");
        expect(toggle).toHaveAttribute("aria-checked", "true");
    });

    test("detail mode toggle can be turned off", async () => {
        const user = userEvent.setup();
        renderPanel([makeStep()]);
        const toggle = screen.getByRole("switch");
        await user.click(toggle);
        expect(toggle).toHaveAttribute("aria-checked", "false");
    });

    test("detail mode toggle can be toggled back on", async () => {
        const user = userEvent.setup();
        renderPanel([makeStep()]);
        const toggle = screen.getByRole("switch");
        await user.click(toggle);
        expect(toggle).toHaveAttribute("aria-checked", "false");
        await user.click(toggle);
        expect(toggle).toHaveAttribute("aria-checked", "true");
    });

    test("renders with agent name display map without error", () => {
        renderPanel([makeStep()], { "agent-1": "My Agent Display Name" });
        expect(screen.queryByText(/No events to display/)).not.toBeInTheDocument();
    });

    test("renders multiple steps without error", () => {
        const steps = [makeStep({ id: "step-1", type: "USER_REQUEST", title: "User Input" }), makeStep({ id: "step-2", type: "TASK_COMPLETED", title: "Done" })];
        renderPanel(steps);
        expect(screen.queryByText(/No events to display/)).not.toBeInTheDocument();
    });

    test("empty state does not render controls bar", () => {
        renderPanel([]);
        expect(screen.queryByRole("button", { name: /center workflow/i })).not.toBeInTheDocument();
        expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    });
});
