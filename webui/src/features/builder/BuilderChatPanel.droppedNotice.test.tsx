// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

describe("BuilderChatPanel — the dropped notice", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;
    let manifestComponents: Array<Record<string, unknown>>;
    let manifestLoadedForEdit = false;
    let currentVersionOverrides: Record<string, number> = {};

    const buildMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "built" }], metadata: { messageId: "a1" } } as unknown as MessageFE;
    const followUpRequest = { isUser: true, parts: [{ kind: "text", text: "drop the old agent" }], metadata: { messageId: "u2" } } as unknown as MessageFE;
    const followUpReply = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "dropped" }], metadata: { messageId: "a2" } } as unknown as MessageFE;
    const expiredQuestion = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "pick one" }], userInputRequest: { requestId: "q1", timedOut: true }, metadata: { messageId: "q1" } } as unknown as MessageFE;
    let chatMessages: MessageFE[];

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;
        manifestLoadedForEdit = false;
        currentVersionOverrides = {};
        chatMessages = [buildMsg];

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({
                manifest: { name: "b", display_name: "Overnight Order Exception Handler", full_validation_passed: true, loaded_for_edit: manifestLoadedForEdit, components: manifestComponents },
                isLoading: false,
                saveManifest: vi.fn(),
            }),
        }));
        vi.doMock("./hooks/useArtifactVersions", () => ({
            useArtifactVersions: () => new Map((manifestComponents ?? []).filter(c => !!c.filename).map(c => [c.filename as string, currentVersionOverrides[c.filename as string] ?? (c.deployed_version as number) ?? 0])),
        }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({ useBuilderCanvasData: () => ({ componentData: new Map() }) }));
        vi.doMock("./hooks/useTestArtifacts", () => ({ useApplyComplete: () => ({ applyComplete: null, isLoading: false, runNumber: null }) }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));
        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagValue: () => false, useBooleanFlagDetails: () => ({ value: false }) }));

        vi.doMock("./InlineCreationProgress", () => ({ InlineCreationProgress: () => null }));
        vi.doMock("./BuildCompleteCTA", () => ({ BuildCompleteCTA: () => <div data-testid="build-complete-cta" /> }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                agents: [{ name: "Builder" }],
                handleSubmit: vi.fn(),
                sessionId: "test-session-id",
                isResponding: false,
                messages: chatMessages,
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
            canBuildActivate: true,
            isBuildActivating: false,
            onBuildActivate: vi.fn(),
        };
    }

    const dropped = { status: "ready" as const, checkFailed: false, failures: new Map<string, string>(), resources: [{ kind: "agent" as const, id: "agent-9", name: "Dropped Agent", createdBySession: true, deployment: "deployed" as const }] };

    const onRemoveDropped = vi.fn();
    const undeployed = [{ filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "validated", origin: "session" }];
    const deployed = [{ filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "deployed", origin: "session", deployed_version: 0 }];

    function addonFor(props: Record<string, unknown>) {
        render(<BuilderChatPanel {...baseProps()} {...props} />);
        expect(capturedRenderAddon).toBeTypeOf("function");
        return render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
    }

    test("shows the server's list with the plan card while there is something left to activate", () => {
        manifestComponents = undeployed;
        const addon = addonFor({ dropped, onRemoveDropped });

        expect(addon.queryByText(/Build Plan:/i)).not.toBeNull();
        expect(addon.getAllByText("Dropped Agent")).toHaveLength(1);
        addon.unmount();
    });

    test("shows it with the completion card once everything is deployed, and not on the plan card as well", () => {
        manifestComponents = deployed;
        const addon = addonFor({ dropped, onRemoveDropped, canBuildActivate: false });

        expect(addon.queryByTestId("build-complete-cta")).not.toBeNull();
        expect(addon.getAllByText("Dropped Agent")).toHaveLength(1);
        addon.unmount();
    });

    test("sits under the latest assistant message after a follow-up turn, never under the first plan message", () => {
        manifestComponents = undeployed;
        chatMessages = [buildMsg, followUpRequest, followUpReply];
        render(<BuilderChatPanel {...baseProps()} dropped={dropped} onRemoveDropped={onRemoveDropped} />);

        const underFirstPlan = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(underFirstPlan.queryByText(/Build Plan:/i)).toBeNull();
        expect(underFirstPlan.queryByText("Dropped Agent")).toBeNull();
        underFirstPlan.unmount();

        const underLatest = render(<>{capturedRenderAddon!(followUpReply, 2)}</>);
        expect(underLatest.queryByText(/Build Plan:/i)).not.toBeNull();
        expect(underLatest.getAllByText("Dropped Agent")).toHaveLength(1);
        underLatest.unmount();
    });

    test("keeps the approved plan card above a trailing question", () => {
        manifestComponents = undeployed;
        chatMessages = [buildMsg, expiredQuestion];
        render(<BuilderChatPanel {...baseProps()} dropped={dropped} onRemoveDropped={onRemoveDropped} />);

        const underPlan = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(underPlan.queryByText(/Build Plan:/i)).not.toBeNull();
        underPlan.unmount();

        const underQuestion = render(<>{capturedRenderAddon!(expiredQuestion, 1)}</>);
        expect(underQuestion.queryByText(/Build Plan:/i)).toBeNull();
        underQuestion.unmount();
    });

    test("puts the completion card's notice under the latest assistant message too", () => {
        manifestComponents = deployed;
        chatMessages = [buildMsg, followUpRequest, followUpReply];
        render(<BuilderChatPanel {...baseProps()} dropped={dropped} onRemoveDropped={onRemoveDropped} canBuildActivate={false} />);

        const underFirstPlan = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(underFirstPlan.queryByTestId("build-complete-cta")).toBeNull();
        expect(underFirstPlan.queryByText("Dropped Agent")).toBeNull();
        underFirstPlan.unmount();

        const underLatest = render(<>{capturedRenderAddon!(followUpReply, 2)}</>);
        expect(underLatest.queryByTestId("build-complete-cta")).not.toBeNull();
        expect(underLatest.getAllByText("Dropped Agent")).toHaveLength(1);
        underLatest.unmount();
    });

    test("shows nothing when the server dropped nothing", () => {
        manifestComponents = undeployed;
        const addon = addonFor({ dropped: { ...dropped, resources: [] }, onRemoveDropped });

        expect(addon.queryByText(/left in place/)).toBeNull();
        addon.unmount();
    });

    test("shows nothing when there is no way to remove, so a list is never shown without its dialog", () => {
        manifestComponents = undeployed;
        const addon = addonFor({ dropped });

        expect(addon.queryByText("Dropped Agent")).toBeNull();
        addon.unmount();
    });
});
