// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

describe("BuilderChatPanel — a blocked build", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;

    const buildMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "built" }], metadata: { messageId: "a1" } } as unknown as MessageFE;

    const blockedConnector = { filename: "inventory_api.json", type: "connector", name: "inventory_api", status: "blocked", blocked_reason: "the spec URL", origin: "session" };
    const approvedAgent = { filename: "a.yaml", type: "agent", name: "InventoryChecker", status: "validated", origin: "session", connectors: ["inventory_api"] };
    const unapprovedAgent = { ...approvedAgent, status: "draft" };

    async function load(components: unknown[], { applyComplete }: { applyComplete: boolean }) {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;
        sessionStorage.clear();

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: { name: "b", components }, isLoading: false, saveManifest: vi.fn() }),
        }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({ useBuilderCanvasData: () => ({ componentData: new Map() }) }));
        vi.doMock("./hooks/useTestArtifacts", () => ({
            useApplyComplete: () => ({ applyComplete: applyComplete ? { summary: "done", changesApplied: [] } : null, isLoading: false, runNumber: applyComplete ? 1 : null }),
        }));
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
                handleSubmit: vi.fn(),
                sessionId: "test-session-id",
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
    }

    beforeEach(() => sessionStorage.clear());

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

    function addonAtBuildMessage() {
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");
        return render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
    }

    test("an earlier Apply Changes summary does not take the slot: the card names what is blocked and Test Mode is not offered", async () => {
        await load([blockedConnector, approvedAgent], { applyComplete: true });
        const anchor = addonAtBuildMessage();
        expect(anchor.getByRole("alert")).toHaveTextContent("needs the spec URL");
        expect(anchor.queryByText(/Switch to Test Mode/)).toBeNull();
        expect(anchor.queryByText(/Changes are completed/)).toBeNull();
    });

    test("with no component built yet the plan is unapproved, and the plan row alone says what is needed", async () => {
        await load([blockedConnector, unapprovedAgent], { applyComplete: false });
        const anchor = addonAtBuildMessage();
        expect(anchor.queryByRole("alert")).toBeNull();
    });
});
