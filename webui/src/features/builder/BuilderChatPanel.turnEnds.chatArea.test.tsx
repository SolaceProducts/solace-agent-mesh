// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts";

import type { MessageFE } from "@/types";

describe("BuilderChatPanel through the real ChatArea: how a build turn ends", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let StoryProvider: typeof import("@/testing/providers/StoryProvider").StoryProvider;
    let manifest: Record<string, unknown>;
    let chat: Partial<ChatContextValue>;

    const STEP = /Generating the TideAgent configuration/;
    const draftAgent = { filename: "tide.yaml", type: "agent", name: "TideAgent", status: "draft", origin: "session" };

    const user = (id: string, text: string) => ({ role: "user", isUser: true, isComplete: true, parts: [{ kind: "text", text }], metadata: { messageId: id } }) as unknown as MessageFE;
    const planMsg = { role: "agent", isUser: false, isComplete: true, taskId: "t1", parts: [{ kind: "text", text: "Here is the plan" }], metadata: { messageId: "m-plan" } } as unknown as MessageFE;
    const prior = [user("u1", "Build a tide agent"), planMsg, user("u2", "Build plan approved. Please generate the configs.")];

    const buildMessage = (over: Record<string, unknown> = {}) =>
        ({
            role: "agent",
            isUser: false,
            isComplete: true,
            taskId: "t2",
            parts: [{ kind: "text", text: "Working on your build." }],
            progressUpdates: [{ type: "status", text: "Generating the TideAgent configuration...", timestamp: 1 }],
            metadata: { messageId: "m-build" },
            ...over,
        }) as unknown as MessageFE;
    const separate = (over: Record<string, unknown>) => ({ role: "agent", isUser: false, isComplete: true, taskId: "t2", metadata: { messageId: "m-end" }, ...over }) as unknown as MessageFE;

    beforeEach(async () => {
        vi.resetModules();
        manifest = { name: "b", display_name: "Tide Tables", full_validation_passed: false, components: [draftAgent] };
        chat = { agents: [{ name: "Builder" }] as never, sessionId: "s1", isResponding: false, isCancelling: false, currentTaskId: null, messages: [] };

        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({ useBuilderManifest: () => ({ manifest, isLoading: false, saveManifest: vi.fn() }) }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({ useBuilderCanvasData: () => ({ componentData: new Map() }) }));
        vi.doMock("./hooks/useTestArtifacts", () => ({ useApplyComplete: () => ({ applyComplete: null, isLoading: false, runNumber: null }) }));
        vi.doMock("@/api/agent-cards", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/agent-cards")>()), useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));
        vi.doMock("./BuildCompleteCTA", () => ({ BuildCompleteCTA: () => null }));
        vi.doMock("@/features/activities/flowChart/FlowChartPanel", () => ({ default: () => <div /> }));
        vi.doMock("@/features/activities/FlowChartDetails", () => ({ FlowChartDetails: () => <div /> }));
        vi.doMock("@/features/activities/taskVisualizerProcessor", () => ({ processTaskForVisualization: () => null }));
        vi.doMock("./TestModePanel", async importOriginal => {
            const real = await importOriginal<typeof import("./TestModePanel")>();
            return { ...real, TestModePanel: () => <div data-testid="test-mode-panel" /> };
        });

        BuilderChatPanel = (await import("./BuilderChatPanel")).BuilderChatPanel;
        StoryProvider = (await import("@/testing/providers/StoryProvider")).StoryProvider;
    });

    const panelProps = () => ({
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
    });

    const tree = () => (
        <MemoryRouter>
            <StoryProvider chatContextValues={chat} flagValues={{}}>
                <BuilderChatPanel {...panelProps()} />
            </StoryProvider>
        </MemoryRouter>
    );

    function stopTurn(view: ReturnType<typeof render>, messages: MessageFE[]) {
        chat = { ...chat, messages: [...prior, buildMessage({ isComplete: false, parts: [] })], isResponding: true, currentTaskId: "t2" };
        view.rerender(tree());
        chat = { ...chat, isCancelling: true };
        view.rerender(tree());
        chat = { ...chat, isCancelling: false, isResponding: false, currentTaskId: null, messages };
        view.rerender(tree());
    }

    const steps = () => screen.queryAllByText(STEP);
    const timelineLabels = () => screen.queryAllByText("Activity Timeline");
    const resumeButtons = () => screen.queryAllByRole("button", { name: /Resume build/i });

    function expectInterruptedOnce() {
        expect(steps()).toHaveLength(1);
        expect(steps()[0]).toHaveTextContent("Generating the TideAgent configuration (interrupted)");
        expect(timelineLabels()).toHaveLength(1);
        expect(screen.queryAllByRole("button", { name: /Activity Timeline/i })).toHaveLength(0);
        expect(screen.getAllByText("Build interrupted")).toHaveLength(1);
        expect(resumeButtons()).toHaveLength(1);
        expect(screen.getAllByText(/This build was interrupted before it finished/i)).toHaveLength(1);
    }

    test("Stop: the steps stay on the build message, which carries the cancel text", () => {
        const view = render(tree());

        stopTurn(view, [...prior, buildMessage({ parts: [{ kind: "text", text: "Task was cancelled." }] })]);

        expectInterruptedOnce();
        expect(screen.getByText("Task was cancelled.")).toBeInTheDocument();
    });

    test("RPC error: a separate error message with no steps follows the build message", () => {
        chat = { ...chat, messages: [...prior, buildMessage(), separate({ isError: true, parts: [{ kind: "text", text: "Error: Bad request: model: claude-nonexistent-9" }] })] };

        render(tree());

        expectInterruptedOnce();
        expect(screen.getByText(/Error: Bad request/)).toBeInTheDocument();
    });

    test("a Stop that ends on a separate step-less plain message (a question was the last message)", () => {
        const view = render(tree());

        stopTurn(view, [...prior, buildMessage(), separate({ parts: [{ kind: "text", text: "Which model should the agent use?" }] })]);

        expectInterruptedOnce();
    });

    test("RPC error after an earlier task that has its own steps: that task's timeline stays", () => {
        const planWithSteps = { ...planMsg, progressUpdates: [{ type: "status", text: "Drafting the plan...", timestamp: 0 }] } as unknown as MessageFE;
        chat = {
            ...chat,
            messages: [user("u1", "Build a tide agent"), planWithSteps, user("u2", "Build plan approved. Please generate the configs."), buildMessage(), separate({ isError: true, parts: [{ kind: "text", text: "Error: Bad request" }] })],
        };

        render(tree());

        expect(steps()).toHaveLength(1);
        expect(steps()[0]).toHaveTextContent("(interrupted)");
        expect(resumeButtons()).toHaveLength(1);
        expect(screen.getAllByRole("button", { name: /Activity Timeline/i })).toHaveLength(1);
    });

    test("a finished build followed by a separate final message keeps the build message's own timeline chip beside the creation timeline", () => {
        manifest.components = [{ ...draftAgent, status: "deployed" }];
        manifest.full_validation_passed = true;
        chat = { ...chat, messages: [...prior, buildMessage(), separate({ parts: [{ kind: "text", text: "Your agent is ready." }] })] };

        render(tree());

        expect(resumeButtons()).toHaveLength(0);
        expect(screen.queryByText("Build interrupted")).toBeNull();
        expect(screen.getAllByRole("button", { name: /Activity Timeline/i })).toHaveLength(2);
    });

    test("failed task: the error flag is on the build message itself", () => {
        chat = { ...chat, messages: [...prior, buildMessage({ isError: true, parts: [{ kind: "text", text: "Error: the task failed" }] })] };

        render(tree());

        expectInterruptedOnce();
    });

    test("completion: nothing went wrong, so the steps read as done and there is nothing to resume", () => {
        manifest.components = [{ ...draftAgent, status: "deployed" }];
        manifest.full_validation_passed = true;
        chat = { ...chat, messages: [...prior, buildMessage({ parts: [{ kind: "text", text: "Your agent is ready." }] })] };

        render(tree());

        expect(steps()).toHaveLength(1);
        expect(steps()[0]).not.toHaveTextContent("(interrupted)");
        expect(resumeButtons()).toHaveLength(0);
        expect(screen.queryByText("Build interrupted")).toBeNull();
        expect(timelineLabels()).toHaveLength(1);
    });

    test("a turn that ends normally on a question, nothing built: no interruption, steps drawn once", () => {
        chat = { ...chat, messages: [...prior, buildMessage({ parts: [{ kind: "text", text: "Which model should the agent use?" }] })] };

        render(tree());

        expect(steps()).toHaveLength(1);
        expect(steps()[0]).not.toHaveTextContent("(interrupted)");
        expect(resumeButtons()).toHaveLength(0);
        expect(timelineLabels()).toHaveLength(1);
    });
});
