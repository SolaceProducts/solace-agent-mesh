// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

import type { TestReport } from "./TestReportCard";

describe("BuilderChatPanel — Apply Changes flow", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let submitMock: ReturnType<typeof vi.fn>;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;
    let capturedOnApply: ((r: TestReport) => void | Promise<boolean>) | null;
    let saveManifestMock: ReturnType<typeof vi.fn>;
    let mockComponents: { filename: string; type: string; name: string; origin: string; status?: string; id?: string }[];
    let mockAgents: { name: string; displayName?: string }[];
    let mockApplyComplete: { summary: string; changesApplied: { component: string; area: string; change: string }[] } | null;
    let mockApplyCompleteVersion: number | null;
    let mockComponentData: Map<string, { yamlText?: string }>;
    let mockPlatformWorkflows: { name: string }[] | undefined;
    let capturedResolveName: ((target: { type: string; name: string }) => string) | null;

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;
        capturedOnApply = null;
        submitMock = vi.fn();
        saveManifestMock = vi.fn().mockResolvedValue({ ok: true });
        mockComponents = [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session" }];
        mockAgents = [{ name: "Builder" }];
        mockApplyComplete = null;
        mockApplyCompleteVersion = null;
        mockComponentData = new Map();
        mockPlatformWorkflows = undefined;
        capturedResolveName = null;

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest: { name: "b", components: mockComponents }, isLoading: false, saveManifest: saveManifestMock }),
        }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({ useBuilderCanvasData: () => ({ componentData: mockComponentData }) }));
        vi.doMock("./hooks/useTestArtifacts", () => ({ useApplyComplete: () => ({ applyComplete: mockApplyComplete, isLoading: false, runNumber: mockApplyCompleteVersion }) }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: mockPlatformWorkflows }) }));
        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagValue: () => false, useBooleanFlagDetails: () => ({ value: false }) }));

        vi.doMock("./ApplyChangesDialog", () => ({
            ApplyChangesDialog: () => null,
            shouldSkipApplyChangesConfirm: () => true,
        }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                agents: mockAgents,
                handleSubmit: submitMock,
                sessionId: "test-session-id",
                isResponding: false,
                messages: [],
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
            return {
                ...real,
                TestModePanel: (props: { onApplyChangesToBuild?: (r: TestReport) => void | Promise<boolean>; resolveAddressableName?: (target: { type: string; name: string }) => string }) => {
                    capturedOnApply = props.onApplyChangesToBuild ?? null;
                    capturedResolveName = props.resolveAddressableName ?? null;
                    return <div data-testid="test-mode-panel" />;
                },
            };
        });

        const mod = await import("./BuilderChatPanel");
        BuilderChatPanel = mod.BuilderChatPanel;
    });

    const tabChange = vi.fn();
    function baseProps() {
        return {
            mode: "fullscreen" as const,
            onModeChange: vi.fn(),
            builderActiveTab: "build" as const,
            onBuilderActiveTabChange: tabChange,
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

    const report: TestReport = {
        componentName: "TextSummarizer",
        componentKind: "agent",
        sampleInput: "in",
        expectedOutput: "out",
        actualOutput: "actual",
        recommendations: [
            { area: "system_prompt", finding: "not enforcing JSON output", suggestion: "add a rule", priority: "high" },
            { area: "tools", finding: "missing validation", suggestion: "validate", priority: "medium" },
        ],
    };

    test("Apply Changes sends a hidden message carrying compact card metadata", async () => {
        tabChange.mockClear();
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedOnApply).toBeTypeOf("function");

        await capturedOnApply!(report);

        expect(tabChange).toHaveBeenCalledWith("build");
        expect(submitMock).toHaveBeenCalledTimes(1);
        const call = submitMock.mock.calls[0];
        expect(call[2]).toContain("Apply these changes");
        const extra = call[call.length - 1] as { hidden?: boolean; applyChangesCard?: { componentName: string; items: unknown[] } };
        expect(extra.hidden).toBe(true);
        expect(extra.applyChangesCard?.componentName).toBe("TextSummarizer");
        expect(extra.applyChangesCard?.items).toHaveLength(2);
    });

    test("renderMessageAddon renders the compact card for a hidden apply message", () => {
        tabChange.mockClear();
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const msg = {
            isUser: true,
            parts: [{ kind: "text", text: "Apply these changes..." }],
            metadata: {
                messageId: "u1",
                applyChangesCard: { componentName: "TextSummarizer", componentKind: "agent", items: [{ priority: "high", area: "system_prompt", finding: "not enforcing JSON output" }] },
            },
        } as unknown as MessageFE;

        render(<>{capturedRenderAddon!(msg, 0)}</>);
        screen.getByText("TextSummarizer");
        screen.getByText("not enforcing JSON output");
        fireEvent.click(screen.getByText("View test report"));
        expect(tabChange).toHaveBeenCalledWith("test");
    });

    test("renderMessageAddon delegates non-apply messages to the base renderer", () => {
        render(<BuilderChatPanel {...baseProps()} />);
        expect(capturedRenderAddon).toBeTypeOf("function");

        const plainUser = { isUser: true, parts: [{ kind: "text", text: "hi" }], metadata: { messageId: "u2" } } as unknown as MessageFE;
        expect(capturedRenderAddon!(plainUser, 0)).toBeNull();
    });

    test("the apply-complete signal does not mark a component deployed", async () => {
        mockComponents = [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "validated" }];
        mockApplyComplete = { summary: "Edited the prompt.", changesApplied: [{ component: "MyAgent", area: "system_prompt", change: "tightened the output rule" }] };
        mockApplyCompleteVersion = 1;

        render(<BuilderChatPanel {...baseProps()} />);

        await waitFor(() => expect(screen.getByTestId("chat-area")).toBeInTheDocument());
        expect(saveManifestMock).not.toHaveBeenCalled();
    });

    describe("live-agent flip", () => {
        const OWN_ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";

        test("flips a validated component whose own instance is live", async () => {
            mockComponents = [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "validated", id: OWN_ID }];
            mockAgents = [{ name: "Builder" }, { name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }];

            render(<BuilderChatPanel {...baseProps()} />);

            await waitFor(() => expect(saveManifestMock).toHaveBeenCalledTimes(1));
            const updater = saveManifestMock.mock.calls[0][0] as (m: { components: typeof mockComponents }) => { components: typeof mockComponents };
            expect(updater({ components: mockComponents }).components[0].status).toBe("deployed");
        });

        test("flips only the component whose own instance is live, not a validated sibling", async () => {
            mockComponents = [
                { filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "validated", id: OWN_ID },
                { filename: "other.yaml", type: "agent", name: "Sibling", origin: "session", status: "validated", id: "01a160ce-7f96-7e82-80dd-7e47b3032891" },
            ];
            mockAgents = [{ name: "Builder" }, { name: "agent_01a160d0_2474_7426_830a_ae0397fcdc9a" }];

            render(<BuilderChatPanel {...baseProps()} />);

            await waitFor(() => expect(saveManifestMock).toHaveBeenCalledTimes(1));
            const updater = saveManifestMock.mock.calls[0][0] as (m: { components: typeof mockComponents }) => { components: typeof mockComponents };
            expect(updater({ components: mockComponents }).components.map(c => c.status)).toEqual(["deployed", "validated"]);
        });

        test.each([
            ["with a recorded id", { id: OWN_ID }],
            ["without a recorded id", {}],
        ])("does not flip a validated component because a plain-named card equal to its name is live, %s", async (_label, identity) => {
            mockComponents = [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "validated", ...identity }];
            mockAgents = [{ name: "Builder" }, { name: "MyAgent" }];

            render(<BuilderChatPanel {...baseProps()} />);

            await waitFor(() => expect(screen.getByTestId("chat-area")).toBeInTheDocument());
            expect(saveManifestMock).not.toHaveBeenCalled();
        });

        test("does not flip a component because a same-named agent another session deployed is live", async () => {
            mockComponents = [{ filename: "agent.yaml", type: "agent", name: "MyAgent", origin: "session", status: "validated" }];
            mockAgents = [{ name: "Builder" }, { name: "agent_01a160ce_7f96_7e82_80dd_7e47b3032891", displayName: "MyAgent" }];

            render(<BuilderChatPanel {...baseProps()} />);

            await waitFor(() => expect(screen.getByTestId("chat-area")).toBeInTheDocument());
            expect(saveManifestMock).not.toHaveBeenCalled();
        });
    });

    describe("Test Mode target name", () => {
        const legacyShapeYaml = `
apps:
  - name: mule_detection_workflow
    app_config:
      agent_name: MuleDetectionWorkflow
      agent_type: workflow
      nodes:
        - id: score
          type: agent
          agent_name: MuleRiskScorer
`;
        const target = { type: "workflow", name: "Mule Detection" };

        beforeEach(() => {
            mockComponents = [{ filename: "wf.yaml", type: "workflow", name: target.name, origin: "session", status: "deployed" }];
            mockComponentData = new Map([[target.name, { yamlText: legacyShapeYaml }]]);
        });

        test("invokes a workflow the platform still stores under its legacy name by that name", () => {
            mockPlatformWorkflows = [{ name: "mule_detection_workflow" }];
            render(<BuilderChatPanel {...baseProps()} />);
            expect(capturedResolveName?.(target)).toBe("mule_detection_workflow");
        });

        test("invokes a re-saved workflow by its agent_name", () => {
            mockPlatformWorkflows = [{ name: "MuleDetectionWorkflow" }];
            render(<BuilderChatPanel {...baseProps()} />);
            expect(capturedResolveName?.(target)).toBe("MuleDetectionWorkflow");
        });
    });
});
