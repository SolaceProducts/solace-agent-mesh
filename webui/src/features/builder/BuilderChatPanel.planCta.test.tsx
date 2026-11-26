// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

describe("BuilderChatPanel — plan card Build & Activate suppression", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;
    let manifestComponents: Array<Record<string, unknown>>;
    let manifestLoadedForEdit = false;
    let currentVersionOverrides: Record<string, number> = {};

    const buildMsg = { isUser: false, isComplete: true, parts: [{ kind: "text", text: "built" }], metadata: { messageId: "a1" } } as unknown as MessageFE;

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;
        manifestLoadedForEdit = false;
        currentVersionOverrides = {};

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
        vi.doMock("./BuildCompleteCTA", () => ({ BuildCompleteCTA: () => null }));

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

    test("suppresses Build & Activate once the whole build — gateway included — is deployed", () => {
        manifestComponents = [
            { filename: "wf.yaml", type: "workflow", name: "ExceptionTriage", status: "deployed", origin: "session", deployed_version: 0 },
            { filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "deployed", origin: "session", deployed_version: 0 },
            { filename: "gw.yaml", type: "gateway", name: "order_exception_gateway", status: "deployed", origin: "session", deployed_version: 0 },
        ];
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const addon = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(addon.queryByText(/Build Plan:/i)).not.toBeNull();
        expect(addon.queryByRole("button", { name: /Build & Activate/i })).toBeNull();
        addon.unmount();
    });

    test("keeps Build & Activate when the gateway (entrypoint) is undeployed — the build isn't fully activated", () => {
        manifestComponents = [
            { filename: "wf.yaml", type: "workflow", name: "ExceptionTriage", status: "deployed", origin: "session", deployed_version: 0 },
            { filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "deployed", origin: "session", deployed_version: 0 },
            { filename: "gw.yaml", type: "gateway", name: "order_exception_gateway", status: "validated", origin: "session" },
        ];
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const addon = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(addon.queryByText(/Build Plan:/i)).not.toBeNull();
        expect(addon.queryByRole("button", { name: /Build & Activate/i })).not.toBeNull();
        addon.unmount();
    });

    test("keeps Build & Activate for a built skill + undeployed gateway — the gateway (entrypoint) still needs deploying", () => {
        manifestComponents = [
            { filename: "s.yaml", type: "skill", name: "sample-geography", status: "validated", origin: "session" },
            { filename: "gw.yaml", type: "gateway", name: "order_exception_gateway", status: "validated", origin: "session" },
        ];
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const addon = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(addon.queryByText(/Build Plan:/i)).not.toBeNull();
        expect(addon.queryByRole("button", { name: /Build & Activate/i })).not.toBeNull();
        addon.unmount();
    });

    test("keeps Build & Activate while a testable component is still undeployed", () => {
        manifestComponents = [
            { filename: "wf.yaml", type: "workflow", name: "ExceptionTriage", status: "deployed", origin: "session", deployed_version: 0 },
            { filename: "a.yaml", type: "agent", name: "OrderInvestigator", status: "validated", origin: "session" },
        ];
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const addon = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(addon.queryByRole("button", { name: /Build & Activate/i })).not.toBeNull();
        addon.unmount();
    });

    const loadedWorkflow = { filename: "wf.yaml", type: "workflow", name: "ExceptionTriage", status: "deployed", origin: "session", deployed_version: 0 };
    const platformAgent = { filename: "", type: "agent", name: "OrderInvestigator", status: "deployed", origin: "platform" };
    const addedAgent = (status: string) => ({ filename: "new.yaml", type: "agent", name: "NewAgent", status, origin: "session" });

    test.each<{ name: string; flag: boolean; components: Array<Record<string, unknown>>; currentVersions?: Record<string, number>; planCard: boolean; buildActivate: boolean }>([
        { name: "untouched workflow with a platform dependency", flag: true, components: [loadedWorkflow, platformAgent], planCard: false, buildActivate: false },
        { name: "untouched workflow without dependencies", flag: true, components: [loadedWorkflow], planCard: false, buildActivate: false },
        { name: "untouched, only platform components", flag: true, components: [platformAgent], planCard: false, buildActivate: false },
        { name: "workflow config edited (artifact advanced), platform dependency", flag: true, components: [loadedWorkflow, platformAgent], currentVersions: { "wf.yaml": 1 }, planCard: true, buildActivate: true },
        { name: "workflow config edited (artifact advanced), no dependencies", flag: true, components: [loadedWorkflow], currentVersions: { "wf.yaml": 2 }, planCard: true, buildActivate: true },
        {
            name: "agent re-saved the manifest without the workflow's stamp, or a session seeded before the stamp existed",
            flag: true,
            components: [{ ...loadedWorkflow, deployed_version: undefined }, platformAgent],
            planCard: true,
            buildActivate: true,
        },
        { name: "agent added a draft component", flag: true, components: [loadedWorkflow, platformAgent, addedAgent("draft")], planCard: true, buildActivate: true },
        { name: "agent added a validated component", flag: true, components: [loadedWorkflow, platformAgent, addedAgent("validated")], planCard: true, buildActivate: true },
        {
            name: "edit already deployed and the flag persisted away: the card stays, nothing left to activate",
            flag: false,
            components: [{ ...loadedWorkflow, deployed_version: 1 }, platformAgent],
            currentVersions: { "wf.yaml": 1 },
            planCard: true,
            buildActivate: false,
        },
        { name: "build made in this session (never flagged), undeployed workflow", flag: false, components: [{ ...loadedWorkflow, status: "validated", deployed_version: undefined }], planCard: true, buildActivate: true },
    ])("$name", ({ flag, components, currentVersions, planCard, buildActivate }) => {
        manifestLoadedForEdit = flag;
        manifestComponents = components;
        currentVersionOverrides = currentVersions ?? {};
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const addon = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(addon.queryByText(/Build Plan:/i) !== null).toBe(planCard);
        expect(addon.queryByRole("button", { name: /Build & Activate/i }) !== null).toBe(buildActivate);
        addon.unmount();
    });

    test.each([
        ...(["agent", "a2a_agent", "gateway", "workflow", "connector", "skill"] as const).map(type => ({
            name: `${type} built in a new session`,
            component: { filename: "c.yaml", type, name: "C", status: "validated", origin: "session" },
            currentVersion: 0,
        })),
        ...(["agent", "a2a_agent", "gateway", "workflow", "connector"] as const).map(type => ({
            name: `${type} edited from chat, stamp behind its artifact`,
            component: { filename: "c.yaml", type, name: "C", status: "deployed", origin: "session", deployed_version: 0 },
            currentVersion: 1,
        })),
        ...(["agent", "a2a_agent", "gateway", "workflow", "connector"] as const).map(type => ({
            name: `${type} reloaded, stamp matches its artifact`,
            component: { filename: "c.yaml", type, name: "C", status: "deployed", origin: "session", deployed_version: 1 },
            currentVersion: 1,
        })),
    ])("an unflagged $name still shows its plan card", ({ component, currentVersion }) => {
        manifestLoadedForEdit = false;
        manifestComponents = [component];
        currentVersionOverrides = { "c.yaml": currentVersion };
        render(<BuilderChatPanel {...baseProps()} />);

        const addon = render(<>{capturedRenderAddon!(buildMsg, 0)}</>);
        expect(addon.queryByText(/Build Plan:/i)).not.toBeNull();
        addon.unmount();
    });
});
