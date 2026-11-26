// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import { mockVisualizerSteps } from "@/testing/data/visualizerSteps";
import { mockPanZoomLayout, transformOf, workflowCentreOf } from "@/testing/helpers/panZoomLayout";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import FlowChartPanel from "./FlowChartPanel";

vi.mock("@/api/agent-cards", () => ({ useAgentCards: () => ({ data: [], refetch: vi.fn() }) }));

const WORKFLOW_WIDTH = 600;

function renderPanel() {
    return renderWithProviders(
        <TooltipProvider>
            <FlowChartPanel processedSteps={mockVisualizerSteps} />
        </TooltipProvider>
    );
}

describe("FlowChartPanel centring", () => {
    beforeEach(() => {
        vi.useFakeTimers();
    });

    afterEach(() => {
        vi.clearAllTimers();
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    test("the fit that runs on open puts the middle of the workflow in the middle of the canvas", () => {
        mockPanZoomLayout({ canvasWidth: 1000, workflowWidth: WORKFLOW_WIDTH });
        const { container } = renderPanel();

        act(() => vi.runOnlyPendingTimers());

        expect(workflowCentreOf(container, WORKFLOW_WIDTH)).toBeCloseTo(500);
    });

    test("a fit on a hidden canvas keeps the last transform", () => {
        mockPanZoomLayout({ canvasWidth: 0, workflowWidth: WORKFLOW_WIDTH });
        const { container } = renderPanel();
        const before = transformOf(container);

        act(() => vi.runOnlyPendingTimers());

        expect(transformOf(container)).toBe(before);
    });
});
