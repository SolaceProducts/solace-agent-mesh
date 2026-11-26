// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

describe("BuilderChatPanel — Activity layer state is not shared across pages", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let chatContext: Record<string, unknown>;
    const setActiveSidePanelTab = vi.fn((tab: string) => {
        chatContext.activeSidePanelTab = tab;
    });

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();

        chatContext = {
            agents: [{ name: "Builder" }],
            handleSubmit: vi.fn(),
            sessionId: "test-session-id",
            isResponding: false,
            messages: [],
            activeSidePanelTab: "files",
            setActiveSidePanelTab,
            setTaskIdInSidePanel: vi.fn(),
            openSidePanelTab: vi.fn(),
            displayError: vi.fn(),
            allArtifacts: [],
            taskIdInSidePanel: null,
            activityEventTasks: {},
        };

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: { name: "b", components: [] }, isLoading: false, saveManifest: vi.fn() }),
        }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({
            useBuilderCanvasData: () => ({ componentData: new Map() }),
        }));
        vi.doMock("./hooks/useTestArtifacts", () => ({
            useApplyComplete: () => ({ applyComplete: null, isLoading: false, runNumber: null }),
        }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));
        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagValue: () => false, useBooleanFlagDetails: () => ({ value: false }) }));
        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => chatContext,
            useTaskContext: () => ({ monitoredTasks: {}, loadTaskFromBackend: vi.fn() }),
        }));
        vi.doMock("@/features/chat/ChatArea", () => ({ ChatArea: () => <div data-testid="chat-area" /> }));
        vi.doMock("@/features/activities/flowChart/FlowChartPanel", () => ({ default: () => <div data-testid="flow-chart-panel" /> }));
        vi.doMock("@/features/activities/FlowChartDetails", () => ({ FlowChartDetails: () => <div data-testid="flow-chart-details" /> }));
        vi.doMock("@/features/activities/useReconciledActivityTask", () => ({
            useReconciledActivityTask: () => ({ visualizedTask: null, isLoadingTask: false }),
        }));
        vi.doMock("@/features/chat/a2ui/UserInputMessage", () => ({ UserInputMessage: () => null }));
        vi.doMock("@/features/activities/taskVisualizerProcessor", () => ({ processTaskForVisualization: () => null }));
        vi.doMock("./TestModePanel", async importOriginal => {
            const real = await importOriginal<typeof import("./TestModePanel")>();
            return { ...real, TestModePanel: () => <div data-testid="test-mode-panel" /> };
        });

        const mod = await import("./BuilderChatPanel");
        BuilderChatPanel = mod.BuilderChatPanel;
    });

    function baseProps() {
        return {
            mode: "fullscreen" as const,
            onModeChange: vi.fn(),
            builderActiveTab: "build" as const,
            onBuilderActiveTabChange: vi.fn(),
            testTargetName: null,
            testRunning: false,
            onTestRunningChange: vi.fn(),
            stopTestRef: { current: null },
            onLatestRunTaskIdChange: vi.fn(),
            selectedTestActivityRun: null,
            onSelectTestActivityRun: vi.fn(),
            isApplyingChanges: false,
            onApplyingChangesChange: vi.fn(),
        };
    }

    test("a leftover 'activity' tab from another page does NOT open the activity layer on mount, and is cleared to 'files'", () => {
        chatContext.activeSidePanelTab = "activity";

        render(<BuilderChatPanel {...baseProps()} />);

        expect(screen.queryByText("No task selected to display")).not.toBeInTheDocument();
        expect(setActiveSidePanelTab).toHaveBeenCalledWith("files");
    });

    test("an activity request made while the builder is mounted DOES open the activity layer", () => {
        const { rerender } = render(<BuilderChatPanel {...baseProps()} />);
        expect(screen.queryByText("No task selected to display")).not.toBeInTheDocument();

        chatContext.activeSidePanelTab = "activity";
        rerender(<BuilderChatPanel {...baseProps()} />);

        expect(screen.getByText("No task selected to display")).toBeInTheDocument();
    });

    describe("Build/Test toggle while the Activity layer is open", () => {
        test("is hidden in the Activity layer and returns when the user goes Back to chat", () => {
            render(<BuilderChatPanel {...baseProps()} />);
            expect(screen.getByRole("tab", { name: /test/i })).toBeInTheDocument();

            fireEvent.click(screen.getByRole("button", { name: "Activity" }));

            expect(screen.getByText("No task selected to display")).toBeInTheDocument();
            expect(screen.queryByRole("tab", { name: /build/i })).not.toBeInTheDocument();
            expect(screen.queryByRole("tab", { name: /test/i })).not.toBeInTheDocument();

            fireEvent.click(screen.getByRole("button", { name: "Back to chat" }));

            expect(screen.queryByText("No task selected to display")).not.toBeInTheDocument();
            expect(screen.getByRole("tab", { name: /build/i })).toBeInTheDocument();
            expect(screen.getByRole("tab", { name: /test/i })).toBeInTheDocument();
        });
    });
});
