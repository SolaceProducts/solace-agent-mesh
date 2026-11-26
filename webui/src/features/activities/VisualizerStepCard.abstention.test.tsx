// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { VisualizerStepCard } from "./VisualizerStepCard";

const ts = new Date().toISOString();
const baseStep = { rawEventIds: [], nestingLevel: 0, owningTaskId: "task-1" };

function renderNodeResult(metadata: Record<string, unknown>, status = "skipped") {
    const step = {
        id: "s-1",
        type: "WORKFLOW_NODE_EXECUTION_RESULT" as const,
        timestamp: ts,
        title: "Node Result",
        data: {
            workflowNodeExecutionResult: {
                nodeId: "gate",
                status,
                metadata,
            },
        },
        ...baseStep,
    };
    return render(
        <MemoryRouter>
            <StoryProvider chatContextValues={{ sessionId: "test" }}>
                {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
                <VisualizerStepCard step={step as any} />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("workflow node result — abstention signals", () => {
    test("a skip that followed an unresolved reference says so", () => {
        renderNodeResult({ skip_reason: "when clause references a field the upstream node did not produce", unresolved_reference: true });
        expect(screen.getByText(/when clause references a field/)).toBeInTheDocument();
    });

    test("a deliberate prune shows its reason without claiming an unresolved reference", () => {
        const { container } = renderNodeResult({ skip_reason: 'not selected by switch "sw"' });
        expect(screen.getByText(/not selected by switch/)).toBeInTheDocument();
        expect(container.textContent).not.toMatch(/referenced a field no node produced/);
    });

    test("a switch that selected nothing reports the unresolved count", () => {
        renderNodeResult({ cases_unresolved: 2 }, "success");
        expect(screen.getByText(/No branch selected/)).toBeInTheDocument();
        expect(screen.getByText(/2 referenced a field no node produced/)).toBeInTheDocument();
    });

    test("a branch taken despite a dead case still reports the count", () => {
        renderNodeResult({ cases_unresolved: 1, selected_branch: "rescue", selected_case_index: 0 }, "success");
        expect(screen.getByText(/rescue/)).toBeInTheDocument();
        expect(screen.getByText(/1 referenced a field no node produced/)).toBeInTheDocument();
        expect(screen.queryByText(/No branch selected/)).not.toBeInTheDocument();
    });

    test("a clean switch reports no unresolved count", () => {
        const { container } = renderNodeResult({ cases_unresolved: 0, selected_branch: "premium", selected_case_index: 0 }, "success");
        expect(container.textContent).not.toMatch(/referenced a field no node produced/);
        expect(container.textContent).not.toMatch(/No branch selected/);
    });

    test("a loop that could not decide its condition says why", () => {
        renderNodeResult({ iterations_completed: 1, stopped_reason: "condition_unresolved" }, "success");
        expect(screen.getByText(/its condition referenced a field no node produced/)).toBeInTheDocument();
    });

    test("a loop that ended normally does not", () => {
        const { container } = renderNodeResult({ iterations_completed: 3, stopped_reason: "condition_false" }, "success");
        expect(container.textContent).not.toMatch(/its condition referenced a field/);
    });
});
