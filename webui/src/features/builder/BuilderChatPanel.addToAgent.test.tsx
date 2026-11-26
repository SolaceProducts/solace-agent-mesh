// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

describe("BuilderChatPanel — skill 'Add to an agent' CTA", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let submitMock: ReturnType<typeof vi.fn>;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;

    const aiMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "done" }], metadata: { messageId: "a1" } } as unknown as MessageFE;

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;
        submitMock = vi.fn();

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: { name: "b", components: [{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" }] }, isLoading: false, saveManifest: vi.fn() }),
        }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({ useBuilderCanvasData: () => ({ componentData: new Map() }) }));
        vi.doMock("./hooks/useTestArtifacts", () => ({ useApplyComplete: () => ({ applyComplete: null, isLoading: false, runNumber: null }) }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));
        vi.doMock("@/api/connectors", () => ({ useConnectors: () => ({ data: undefined }) }));
        vi.doMock("@/api/agents", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/agents")>()), useAgents: () => ({ data: undefined }) }));
        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagValue: () => false, useBooleanFlagDetails: () => ({ value: false }) }));

        vi.doMock("./InlinePlanCard", () => ({ InlinePlanCard: () => null }));
        vi.doMock("./InlineCreationProgress", () => ({ InlineCreationProgress: () => null }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                agents: [{ name: "Builder" }],
                handleSubmit: submitMock,
                sessionId: "test-session-id",
                isResponding: false,
                messages: [aiMsg],
                activeSidePanelTab: "files",
                setActiveSidePanelTab: vi.fn(),
                setTaskIdInSidePanel: vi.fn(),
                openSidePanelTab: vi.fn(),
                displayError: vi.fn(),
                allArtifacts: [],
            }),
            useTaskContext: () => ({ monitoredTasks: {}, loadTaskFromBackend: vi.fn() }),
        }));

        vi.doMock("@/features/chat/ChatArea", () => ({
            ChatArea: (props: { renderMessageAddon?: (m: MessageFE, i: number) => ReactNode }) => {
                capturedRenderAddon = props.renderMessageAddon ?? null;
                return <div data-testid="chat-area" />;
            },
        }));
        vi.doMock("@/features/activities/flowChart/FlowChartPanel", () => ({ default: () => <div /> }));
        vi.doMock("@/features/activities/FlowChartDetails", () => ({ FlowChartDetails: () => <div /> }));
        vi.doMock("@/features/chat/a2ui/UserInputMessage", () => ({ UserInputMessage: () => null }));
        vi.doMock("@/features/activities/taskVisualizerProcessor", () => ({ processTaskForVisualization: () => null }));
        vi.doMock("./TestModePanel", async importOriginal => {
            const real = await importOriginal<typeof import("./TestModePanel")>();
            return { ...real, TestModePanel: () => <div data-testid="test-mode-panel" /> };
        });

        BuilderChatPanel = (await import("./BuilderChatPanel")).BuilderChatPanel;
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

    test("clicking 'Add to an agent' submits a visible user turn (no hidden metadata)", () => {
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        render(<>{capturedRenderAddon!(aiMsg, 0)}</>);
        fireEvent.click(screen.getByRole("button", { name: "Add to an agent" }));

        expect(submitMock).toHaveBeenCalledTimes(1);
        const call = submitMock.mock.calls[0];
        expect(call[2]).toBe("Add the sample-geography skill to an agent.");
        const extra = call[8] as { hidden?: boolean } | undefined;
        expect(extra?.hidden).toBeUndefined();
    });
});
