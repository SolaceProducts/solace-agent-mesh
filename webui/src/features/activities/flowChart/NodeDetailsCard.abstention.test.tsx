// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { NodeDetails } from "@/features/activities/flowChart/utils/nodeDetailsHelper";

import type { VisualizerStep } from "@/types";

async function loadCard() {
    vi.resetModules();

    vi.doMock("@/providers/contexts/ChatContext", () => ({
        useChatContext: () => ({
            setPreviewArtifact: vi.fn(),
            setActiveSidePanelTab: vi.fn(),
            setIsSidePanelCollapsed: vi.fn(),
            navigateArtifactVersion: vi.fn(),
            sessionId: "test-session",
        }),
    }));

    vi.doMock("@/api/artifacts", () => ({
        useFilteredChatArtifacts: () => ({ artifacts: [], allArtifacts: [], isLoading: false, refetch: () => Promise.resolve(), showWorkingArtifacts: false, toggleShowWorkingArtifacts: () => {}, workingArtifactCount: 0 }),
    }));

    const mod = await import("@/features/activities/flowChart/NodeDetailsCard");
    return mod.default;
}

function switchResultDetails(metadata: Record<string, unknown>): NodeDetails {
    const step: VisualizerStep = {
        id: "step-1",
        type: "WORKFLOW_NODE_EXECUTION_RESULT",
        timestamp: new Date().toISOString(),
        title: "Node Result",
        data: {
            workflowNodeExecutionResult: {
                nodeId: "gate",
                status: "success",
                metadata,
            },
        },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: "task-1",
    } as unknown as VisualizerStep;
    return { nodeType: "agent", label: "gate", resultStep: step } as unknown as NodeDetails;
}

describe("NodeDetailsCard — switch abstention signals", () => {
    let NodeDetailsCard: React.ComponentType<{ nodeDetails: NodeDetails }>;

    beforeEach(async () => {
        NodeDetailsCard = await loadCard();
    });

    test("a switch that selected nothing says so and reports the count", () => {
        render(<NodeDetailsCard nodeDetails={switchResultDetails({ cases_unresolved: 2 })} />);
        expect(screen.getByText("No branch selected")).toBeInTheDocument();
        expect(screen.getByText(/2 case\(s\) referenced a field no node produced/)).toBeInTheDocument();
    });

    test("a branch taken despite a dead case still reports the count", () => {
        render(<NodeDetailsCard nodeDetails={switchResultDetails({ cases_unresolved: 1, selected_branch: "rescue", selected_case_index: 0 })} />);
        expect(screen.getByText("rescue")).toBeInTheDocument();
        expect(screen.getByText(/1 case\(s\) referenced a field no node produced/)).toBeInTheDocument();
    });

    test("a clean default fall-through reports no count and no Matched Case", () => {
        const { container } = render(<NodeDetailsCard nodeDetails={switchResultDetails({ cases_unresolved: 0, selected_branch: "fallback", selected_case_index: null })} />);
        expect(screen.getByText(/Default branch - no case matched/)).toBeInTheDocument();
        expect(container.textContent).not.toMatch(/referenced a field no node produced/);
        expect(container.textContent).not.toMatch(/Matched Case/);
    });

    test("a matched case numbers from one", () => {
        render(<NodeDetailsCard nodeDetails={switchResultDetails({ cases_unresolved: 0, selected_branch: "premium", selected_case_index: 0 })} />);
        expect(screen.getByText("Matched Case #1")).toBeInTheDocument();
    });

    test("a loop that could not decide its condition says why", () => {
        render(<NodeDetailsCard nodeDetails={switchResultDetails({ iterations_completed: 1, stopped_reason: "condition_unresolved" })} />);
        expect(screen.getByText(/Loop stopped: its condition referenced a field no node produced/)).toBeInTheDocument();
    });
});
