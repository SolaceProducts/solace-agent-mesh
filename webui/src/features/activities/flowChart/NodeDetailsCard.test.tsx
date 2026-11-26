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

function toolInvocationDetails(toolArguments: Record<string, unknown> | null | undefined): NodeDetails {
    const step: VisualizerStep = {
        id: "step-1",
        type: "AGENT_TOOL_INVOCATION_START",
        timestamp: new Date().toISOString(),
        title: "Tool Invocation",
        data: {
            toolInvocationStart: {
                functionCallId: "fc-1",
                toolName: "search",
                toolArguments: toolArguments as Record<string, unknown>,
            },
        },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: "task-1",
    };
    return {
        nodeType: "tool",
        label: "search",
        requestStep: step,
    };
}

describe("NodeDetailsCard — tool-argument null guards", () => {
    let NodeDetailsCard: React.ComponentType<{ nodeDetails: NodeDetails }>;

    beforeEach(async () => {
        NodeDetailsCard = await loadCard();
    });

    test("renders 'No arguments' when toolArguments is null (renderFormattedArguments guard)", () => {
        render(<NodeDetailsCard nodeDetails={toolInvocationDetails(null)} />);
        expect(screen.getByText("No arguments")).toBeInTheDocument();
    });

    test("renders 'No arguments' when toolArguments is undefined", () => {
        render(<NodeDetailsCard nodeDetails={toolInvocationDetails(undefined)} />);
        expect(screen.getByText("No arguments")).toBeInTheDocument();
    });

    test("handles toolArguments containing a null nested object (renderArgumentValue guard)", () => {
        render(<NodeDetailsCard nodeDetails={toolInvocationDetails({ wrapper: null })} />);
        expect(screen.getByText("wrapper")).toBeInTheDocument();
    });
});
