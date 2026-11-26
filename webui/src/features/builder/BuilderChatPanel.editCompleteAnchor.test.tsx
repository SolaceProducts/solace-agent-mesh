// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

describe("BuilderChatPanel — edit-complete card anchor", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;

    const buildMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "applied" }], metadata: { messageId: "a1" } } as unknown as MessageFE;
    const followOnMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "anything else?" }], metadata: { messageId: "a2" } } as unknown as MessageFE;
    const expiredQuestionMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "pick one" }], userInputRequest: { requestId: "q1", timedOut: true }, metadata: { messageId: "q1" } } as unknown as MessageFE;

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({
                manifest: { name: "b", components: [{ filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" }] },
                isLoading: false,
                saveManifest: vi.fn().mockResolvedValue(true),
            }),
        }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({ useBuilderCanvasData: () => ({ componentData: new Map() }) }));
        vi.doMock("./hooks/useTestArtifacts", () => ({ useApplyComplete: () => ({ applyComplete: { summary: "done", changesApplied: [] }, isLoading: false, runNumber: 1 }) }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));
        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagValue: () => false, useBooleanFlagDetails: () => ({ value: false }) }));

        vi.doMock("./InlinePlanCard", () => ({ InlinePlanCard: () => null }));
        vi.doMock("./InlineCreationProgress", () => ({ InlineCreationProgress: () => null }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                agents: [{ name: "Builder" }],
                handleSubmit: vi.fn(),
                sessionId: "test-session-id",
                isResponding: false,
                messages: [buildMsg, followOnMsg, expiredQuestionMsg],
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

    test("renders the edit-complete card on the latest assistant message", () => {
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const latest = render(<>{capturedRenderAddon!(followOnMsg, 1)}</>);
        expect(latest.queryByRole("button", { name: /Switch to Test Mode/i })).not.toBeNull();
        latest.unmount();

        const trailingQuestion = render(<>{capturedRenderAddon!(expiredQuestionMsg, 2)}</>);
        expect(trailingQuestion.queryByRole("button", { name: /Switch to Test Mode/i })).toBeNull();
        trailingQuestion.unmount();

        const earlier = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(earlier.queryByRole("button", { name: /Switch to Test Mode/i })).toBeNull();
        earlier.unmount();
    });

    test("while the platform check holds the edits back, the card offers a disabled Build & Activate under the verdict, not Test Mode", () => {
        const platformCheck = { check: { kind: "refused" as const, problems: [{ kind: "skill", name: "sample-geography", message: "name is taken" }], forbidden: [] }, canAskBuilder: false };
        render(<BuilderChatPanel {...baseProps()} canBuildActivate={false} onBuildActivate={vi.fn()} platformCheck={platformCheck} />);

        const latest = render(<>{capturedRenderAddon!(followOnMsg, 1)}</>);

        expect(latest.getByText("skill sample-geography: name is taken")).toBeInTheDocument();
        expect(latest.getByRole("button", { name: "Build & Activate" })).toBeDisabled();
        expect(latest.queryByRole("button", { name: /Switch to Test Mode/i })).toBeNull();
    });
});
