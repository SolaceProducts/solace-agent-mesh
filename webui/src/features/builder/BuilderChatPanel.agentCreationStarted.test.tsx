// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

describe("BuilderChatPanel — agent:creation_started on plan approval", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;
    let manifestComponents: Array<Record<string, unknown>>;
    const track = vi.fn();
    const SKIP_CONFIRM_KEY = "sam_builder_skip_activate_confirm";

    const buildMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "built" }], metadata: { messageId: "a1" } } as unknown as MessageFE;

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;
        sessionStorage.clear();
        localStorage.clear();
        localStorage.setItem(SKIP_CONFIRM_KEY, "true");

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: { name: "b", display_name: "Overnight Order Exception Handler", full_validation_passed: true, components: manifestComponents }, isLoading: false, saveManifest: vi.fn() }),
        }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({ useBuilderCanvasData: () => ({ componentData: new Map() }) }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useTestArtifacts", () => ({ useApplyComplete: () => ({ applyComplete: null, isLoading: false, runNumber: null }) }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));
        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagDetails: () => ({ value: false }), useBooleanFlagValue: () => false }));
        vi.doMock("./InlineCreationProgress", () => ({ InlineCreationProgress: () => null }));
        vi.doMock("./BuildCompleteCTA", () => ({ BuildCompleteCTA: () => null }));

        vi.doMock("@/common/product-analytics/report", () => ({ report: track, reportingSessionId: () => "session-one" }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                agents: [{ name: "Builder" }],
                handleSubmit: vi.fn(),
                sessionId: "build-session-1",
                isResponding: false,
                messages: [buildMsg],
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
            canBuildActivate: false,
            isBuildActivating: false,
            onBuildActivate: vi.fn(),
        };
    }

    function approveThePlan() {
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");
        const addon = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        const approve = screen.getByRole("button", { name: /^Build & Activate$/i });
        fireEvent.click(approve);
        return addon;
    }

    function startedCount(): number {
        return track.mock.calls.filter(call => call[0] === "agent:creation_started").length;
    }

    test("reports one start per agent the plan will create", () => {
        manifestComponents = [
            { filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "draft", origin: "session" },
            { filename: "b.yaml", type: "agent", name: "ExceptionSummarizer", status: "draft", origin: "session" },
        ];

        approveThePlan();

        expect(startedCount()).toBe(2);
    });

    test("reports nothing for a plan that builds no agent", () => {
        manifestComponents = [
            { filename: "wf.yaml", type: "workflow", name: "ExceptionTriage", status: "draft", origin: "session" },
            { filename: "c.yaml", type: "connector", name: "orders_db", status: "draft", origin: "session" },
        ];

        approveThePlan();

        expect(startedCount()).toBe(0);
    });

    test("counts only the agents this build creates, not the ones it reuses", () => {
        manifestComponents = [
            { filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "draft", origin: "session" },
            { filename: "p.yaml", type: "agent", name: "PlatformRouter", status: "deployed", origin: "platform" },
        ];

        approveThePlan();

        expect(startedCount()).toBe(1);
    });

    test("reports on rehydration too, when a session-origin component is already validated", () => {
        manifestComponents = [{ filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "validated", origin: "session" }];

        render(<BuilderChatPanel {...baseProps()} />);

        expect(startedCount()).toBe(1);
    });
});
