// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Children, isValidElement, type ReactElement, type ReactNode } from "react";
import { act, fireEvent, render, within } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import type { MessageFE } from "@/types";

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";

describe("BuilderChatPanel: a reloaded session after a Stop", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;
    let manifest: Record<string, unknown>;
    let canvasEntries: Array<[string, Partial<ComponentCanvasData>]>;
    let chat: { isResponding: boolean; isCancelling: boolean; currentTaskId: string | null; sessionId: string; messages: MessageFE[]; handleSubmit: ReturnType<typeof vi.fn> };
    let panelProps: Record<string, unknown>;
    let prior: MessageFE[];
    let recordedStatus: Record<string, string | "unreadable" | undefined>;
    let statusRequests: string[];
    let reported: boolean[];
    let reportInterrupted: (interrupted: boolean) => void;
    let cachedCanvas: { entries: unknown; map: Map<string, ComponentCanvasData> };
    const stopTestRef: { current: (() => void) | null } = { current: null };
    const taskCtx = { monitoredTasks: {}, loadTaskFromBackend: vi.fn() };
    let stable: { saveManifest: ReturnType<typeof vi.fn>; handlers: Record<string, () => void> };

    const draftAgent = { filename: "tide.yaml", type: "agent", name: "TideAgent", status: "draft", origin: "session" };
    const secondAgent = { filename: "tide2.yaml", type: "agent", name: "SecondAgent", status: "draft", origin: "session" };
    const userRequest = { isUser: true, parts: [{ kind: "text", text: "Build a tide agent" }], metadata: { messageId: "u1" } } as unknown as MessageFE;
    const planMsg = { isUser: false, isComplete: true, taskId: "t1", parts: [{ kind: "text", text: "Here is the plan" }], metadata: { messageId: "a1" } } as unknown as MessageFE;
    const approval = { isUser: true, parts: [{ kind: "text", text: "Build plan approved. Please generate the configs." }], metadata: { messageId: "u2" } } as unknown as MessageFE;

    const buildTurn = (overrides: Record<string, unknown> = {}) =>
        ({
            isUser: false,
            isComplete: true,
            taskId: "t2",
            parts: [{ kind: "text", text: "Task was cancelled." }],
            progressUpdates: [{ type: "status", text: "Generating the TideAgent configuration...", timestamp: 1 }],
            metadata: { messageId: "a2" },
            ...overrides,
        }) as unknown as MessageFE;
    const followUp = [
        { isUser: true, parts: [{ kind: "text", text: "continue" }], taskId: "t3", metadata: { messageId: "u3" } } as unknown as MessageFE,
        buildTurn({ taskId: "t3", metadata: { messageId: "a3" }, parts: [{ kind: "text", text: "Which model should the agent use?" }] }),
    ];

    function canvasData(name: string, overrides: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
        return {
            name,
            filename: `${name}.yaml`,
            type: "agent",
            yamlText: null,
            isLoading: false,
            isGenerating: false,
            isQueued: false,
            isWaitingForInput: false,
            isBuildingDeps: false,
            isBuilderResponding: false,
            dependencyProgress: [],
            ...overrides,
        };
    }

    beforeEach(async () => {
        vi.resetModules();
        vi.clearAllMocks();
        capturedRenderAddon = null;
        manifest = { name: "b", display_name: "Tide Tables", full_validation_passed: false, components: [draftAgent] };
        canvasEntries = [];
        prior = [userRequest, planMsg, approval];
        chat = { isResponding: false, isCancelling: false, currentTaskId: null, sessionId: "s1", messages: [...prior, buildTurn()], handleSubmit: vi.fn() };
        panelProps = {};
        recordedStatus = { t2: "canceled" };
        statusRequests = [];
        reported = [];
        cachedCanvas = { entries: undefined, map: new Map() };
        stable = {
            saveManifest: vi.fn(),
            handlers: { onModeChange: vi.fn(), onBuilderActiveTabChange: vi.fn(), onTestRunningChange: vi.fn(), onLatestRunTaskIdChange: vi.fn(), onSelectTestActivityRun: vi.fn(), onApplyingChangesChange: vi.fn(), onBuildActivate: vi.fn() },
        };
        reportInterrupted = (interrupted: boolean) => void reported.push(interrupted);

        vi.doMock("@/api/client", () => ({
            api: {
                webui: {
                    get: (path: string) => {
                        const taskId = /\/api\/v1\/tasks\/([^/]+)\/status/.exec(path)?.[1] ?? "";
                        statusRequests.push(taskId);
                        const status = recordedStatus[taskId];
                        if (status === "unreadable") return Promise.reject(Object.assign(new Error("404"), { status: 404 }));
                        return Promise.resolve({ task: { id: taskId, ...(status ? { status } : {}) }, isRunning: false });
                    },
                },
            },
        }));
        vi.doMock("./canvas/canvasStyles.css", () => ({}));
        vi.doMock("./hooks/useBuilderManifest", () => ({
            useBuilderManifest: () => ({ manifest, isLoading: false, saveManifest: stable.saveManifest }),
        }));
        vi.doMock("./hooks/useArtifactVersions", () => ({ useArtifactVersions: () => new Map() }));
        vi.doMock("./hooks/useBuilderCanvasData", () => ({
            useBuilderCanvasData: () => {
                if (cachedCanvas.entries !== canvasEntries) cachedCanvas = { entries: canvasEntries, map: new Map(canvasEntries.map(([n, o]) => [n, canvasData(n, o)])) };
                return { componentData: cachedCanvas.map };
            },
        }));
        vi.doMock("./hooks/useTestArtifacts", () => ({ useApplyComplete: () => ({ applyComplete: null, isLoading: false, runNumber: null }) }));
        vi.doMock("@/api/agent-cards", () => ({ useAgentCards: () => ({ isLoading: false }) }));
        vi.doMock("@/api/workflows", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/workflows")>()), useWorkflowsQuery: () => ({ data: undefined }) }));
        vi.doMock("@openfeature/react-sdk", () => ({ useBooleanFlagValue: () => false, useBooleanFlagDetails: () => ({ value: false }) }));
        vi.doMock("./BuildCompleteCTA", () => ({ BuildCompleteCTA: () => null }));
        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({
                agents: [{ name: "Builder" }],
                handleSubmit: chat.handleSubmit,
                sessionId: chat.sessionId,
                isResponding: chat.isResponding,
                isCancelling: chat.isCancelling,
                currentTaskId: chat.currentTaskId,
                messages: chat.messages,
                activeSidePanelTab: "files",
                setActiveSidePanelTab: vi.fn(),
                setTaskIdInSidePanel: vi.fn(),
                openSidePanelTab: vi.fn(),
                displayError: vi.fn(),
                allArtifacts: [],
            }),
            useTaskContext: () => taskCtx,
            useUIModeContext: () => ({ showActivityPanel: true }),
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
            onModeChange: stable.handlers.onModeChange,
            builderActiveTab: "build" as const,
            onBuilderActiveTabChange: stable.handlers.onBuilderActiveTabChange,
            testTargetName: null,
            testRunning: false,
            onTestRunningChange: stable.handlers.onTestRunningChange,
            stopTestRef,
            onLatestRunTaskIdChange: stable.handlers.onLatestRunTaskIdChange,
            selectedTestActivityRun: null,
            onSelectTestActivityRun: stable.handlers.onSelectTestActivityRun,
            isApplyingChanges: false,
            onApplyingChangesChange: stable.handlers.onApplyingChangesChange,
            canBuildActivate: false,
            isBuildActivating: false,
            onBuildActivate: stable.handlers.onBuildActivate,
            onBuildInterruptedChange: reportInterrupted,
            ...panelProps,
        };
    }

    type Panel = ReturnType<typeof render>;
    const refresh = (view: Panel) => view.rerender(<BuilderChatPanel {...baseProps()} />);
    const settle = () =>
        act(async () => {
            await Promise.resolve();
            await Promise.resolve();
        });

    async function reload(): Promise<Panel> {
        const view = render(<BuilderChatPanel {...baseProps()} />);
        await settle();
        refresh(view);
        expect(capturedRenderAddon).toBeTypeOf("function");
        return view;
    }

    async function pressStop(): Promise<Panel> {
        const view = render(<BuilderChatPanel {...baseProps()} />);
        const phase = (over: Partial<typeof chat>) => {
            Object.assign(chat, over);
            refresh(view);
        };
        phase({ isResponding: true, currentTaskId: "t2" });
        phase({ isCancelling: true });
        phase({ isCancelling: false, isResponding: false, currentTaskId: null });
        await settle();
        refresh(view);
        return view;
    }

    function splitAddons(message: MessageFE, index: number) {
        const fragment = capturedRenderAddon!(message, index) as ReactElement<{ children?: ReactNode }> | null;
        const addonKeyed = (key: string) => Children.toArray(fragment?.props.children).find(child => isValidElement(child) && String(child.key).endsWith(`$${key}`)) ?? null;
        return { plan: addonKeyed("plan"), progress: addonKeyed("progress") };
    }

    function showChat() {
        const lastAgentMessage = chat.messages.findLast(m => !m.isUser)!;
        const { plan, progress } = splitAddons(lastAgentMessage, 1);
        const card = render(<TooltipProvider>{plan}</TooltipProvider>);
        const timeline = render(<TooltipProvider>{progress}</TooltipProvider>);
        const resumeIn = (v: Panel) => within(v.container).queryAllByRole("button", { name: /Resume build/i }).length;
        return {
            card,
            cardResume: resumeIn(card),
            timelineResume: resumeIn(timeline),
            cardNote: within(card.container).queryByText(/This build was interrupted before it finished/i) !== null,
            label: within(timeline.container).queryByText("Build interrupted") !== null,
            stoppedStep: within(timeline.container).queryByText("Generating the TideAgent configuration (interrupted)") !== null,
            cardButton: (name: RegExp) => within(card.container).queryByRole("button", { name }) !== null,
        };
    }

    type Row = {
        name: string;
        arrange?: () => void;
        resume: "card" | "timeline" | "none";
        tryAgain?: boolean;
    };

    const rows: Row[] = [
        { name: "nothing was generated yet", resume: "card" },
        {
            name: "one component finished, another not started",
            arrange: () => {
                manifest.components = [{ ...draftAgent, status: "validated" }, secondAgent];
                canvasEntries = [["TideAgent", { yamlText: "ok" }]];
            },
            resume: "card",
        },
        {
            name: "the last config was streaming (partial text)",
            arrange: () => {
                canvasEntries = [["TideAgent", { yamlText: "name: TideAgent\nhalf" }]];
            },
            resume: "card",
        },
        {
            name: "every component finished: nothing left to resume",
            arrange: () => {
                manifest.components = [{ ...draftAgent, status: "validated" }];
            },
            resume: "none",
        },
        {
            name: "a deploy failure stands: Try again owns the card",
            arrange: () => {
                panelProps = { deployFailure: { message: "internal error", canRetry: true } };
            },
            resume: "none",
            tryAgain: true,
        },
        {
            name: "the builder is fixing a deploy failure",
            arrange: () => {
                panelProps = { deployFailure: { message: "internal error", canRetry: false } };
            },
            resume: "none",
        },
        {
            name: "a save or deploy is running",
            arrange: () => {
                panelProps = { isBuildActivating: true };
            },
            resume: "none",
        },
        {
            name: "an Apply Changes edit is running",
            arrange: () => {
                panelProps = { isApplyingChanges: true };
            },
            resume: "none",
        },
    ];

    describe.each(["canceled", "failed", "timeout"])("the gateway recorded the latest task as %s", status => {
        test.each(rows)("$name", async row => {
            recordedStatus = { t2: status };
            row.arrange?.();

            await reload();
            const v = showChat();

            expect(statusRequests).toContain("t2");
            expect(v.cardResume).toBe(row.resume === "card" ? 1 : 0);
            expect(v.cardNote).toBe(row.resume === "card");
            expect(v.cardButton(/Try again/i)).toBe(!!row.tryAgain);
            if (row.resume === "timeline") expect(v.timelineResume).toBe(1);
            if (row.resume === "card") {
                expect(v.timelineResume).toBe(0);
                expect(v.label).toBe(true);
                expect(v.stoppedStep).toBe(true);
            }
        });
    });

    test.each(rows)("a reload offers what the Stop offered in the page it was pressed: $name", async row => {
        row.arrange?.();
        recordedStatus = {};
        const stoppedPage = await pressStop();
        const afterStop = visibleSurfaces();
        stoppedPage.unmount();

        recordedStatus = { t2: "canceled" };
        await reload();
        const afterReload = visibleSurfaces();

        expect(afterReload).toEqual(afterStop);
    });

    function visibleSurfaces() {
        const { card: _card, cardButton: _cardButton, ...visible } = showChat();
        return { ...visible, tryAgain: _cardButton(/Try again/i), buildActivate: _cardButton(/Build & Activate/i) };
    }

    describe("a build that was not cut short is not mistaken for one", () => {
        const unfinished = () => {
            manifest.components = [{ ...draftAgent, status: "validated" }, secondAgent];
            canvasEntries = [["TideAgent", { yamlText: "ok" }]];
        };

        test.each<{ name: string; record: string | undefined }>([
            { name: "the task completed", record: "completed" },
            { name: "the task has no status", record: undefined },
            { name: "the task was rejected", record: "rejected" },
            { name: "the record cannot be read", record: "unreadable" },
        ])("$name: the plan card offers nothing", async ({ record }) => {
            recordedStatus = { t2: record };
            unfinished();

            await reload();
            const v = showChat();

            expect(v.cardResume).toBe(0);
            expect(v.cardNote).toBe(false);
            expect(v.stoppedStep).toBe(false);
        });

        test("a later task that completed is judged by its own record, not the earlier Stop", async () => {
            recordedStatus = { t2: "canceled", t3: "completed" };
            chat.messages = [...prior, buildTurn(), ...followUp];
            unfinished();

            await reload();
            const v = showChat();

            expect(statusRequests).toEqual(["t3"]);
            expect(v.cardResume).toBe(0);
            expect(v.cardNote).toBe(false);
        });

        test("the plan was never approved: a Stop while drafting the plan offers no Resume build", async () => {
            prior = [userRequest, planMsg];
            chat.messages = [...prior, buildTurn()];

            await reload();

            expect(statusRequests).toEqual([]);
            expect(showChat().cardResume).toBe(0);
        });

        test("a task that is still running is not read", async () => {
            chat.isResponding = true;
            chat.currentTaskId = "t2";

            await reload();

            expect(statusRequests).toEqual([]);
            expect(showChat().cardResume).toBe(0);
        });

        test("a cancel still being processed offers nothing until it clears", async () => {
            chat.isCancelling = true;
            const view = await reload();
            expect(statusRequests).toEqual([]);
            expect(showChat().cardResume).toBe(0);

            chat.isCancelling = false;
            refresh(view);
            await settle();
            refresh(view);

            expect(showChat().cardResume).toBe(1);
        });
    });

    test("history that loads after the first render is judged once it arrives", async () => {
        const history = chat.messages;
        chat.messages = [];
        const view = render(<BuilderChatPanel {...baseProps()} />);
        await settle();
        expect(statusRequests).toEqual([]);

        chat.messages = history;
        refresh(view);
        await settle();
        refresh(view);

        expect(statusRequests).toEqual(["t2"]);
        expect(showChat().cardResume).toBe(1);
    });

    test("Resume build after a reload asks the builder to continue, flagged as a resume", async () => {
        await reload();
        const v = showChat();

        fireEvent.click(within(v.card.container).getByRole("button", { name: /Resume build/i }));

        expect(chat.handleSubmit).toHaveBeenCalledTimes(1);
        expect(chat.handleSubmit.mock.calls[0][2]).toBe("Please continue the build from where it stopped.");
        expect(chat.handleSubmit.mock.calls[0][8]).toEqual({ resume_build: true });
    });

    describe("the verdict reported to the canvas agrees with the way forward the chat offers", () => {
        const lastReport = () => reported.at(-1);

        describe.each(["canceled", "failed", "timeout"])("after a reload, the gateway recorded the latest task as %s", status => {
            test.each(rows)("$name", async row => {
                recordedStatus = { t2: status };
                row.arrange?.();

                await reload();
                const v = showChat();

                expect(lastReport()).toBe(v.cardResume + v.timelineResume > 0);
            });
        });

        test.each(rows)("right after the Stop pressed in this page: $name", async row => {
            row.arrange?.();
            recordedStatus = {};

            await pressStop();
            const v = showChat();

            expect(lastReport()).toBe(v.cardResume + v.timelineResume > 0);
        });

        test("a build stopped before any component validated reports an interruption, though no component finished", async () => {
            manifest.components = [draftAgent, secondAgent];
            canvasEntries = [];

            await reload();

            expect(lastReport()).toBe(true);
        });

        test.each<{ name: string; record: string | undefined }>([
            { name: "the task completed", record: "completed" },
            { name: "the task has no status", record: undefined },
            { name: "the task was rejected", record: "rejected" },
            { name: "the record cannot be read", record: "unreadable" },
        ])("$name: nothing is reported as interrupted", async ({ record }) => {
            recordedStatus = { t2: record };

            await reload();

            expect(reported.includes(true)).toBe(false);
        });

        test("a plan awaiting approval reports no interruption, even if its task was cancelled", async () => {
            prior = [userRequest, planMsg];
            chat.messages = [...prior, buildTurn()];

            await reload();

            expect(reported.includes(true)).toBe(false);
        });

        test("a live turn reports no interruption, and a cancel still being processed does not either", async () => {
            chat.isResponding = true;
            chat.currentTaskId = "t2";
            const view = await reload();
            expect(reported.includes(true)).toBe(false);

            chat.isResponding = false;
            chat.isCancelling = true;
            refresh(view);
            expect(reported.includes(true)).toBe(false);
        });

        test("the verdict clears the moment a Resume turn starts, and returns when that turn is stopped", async () => {
            recordedStatus = {};
            const view = await pressStop();
            expect(lastReport()).toBe(true);

            Object.assign(chat, { isResponding: true, currentTaskId: "t4" });
            refresh(view);
            expect(lastReport()).toBe(false);

            Object.assign(chat, { isCancelling: true });
            refresh(view);
            Object.assign(chat, { isCancelling: false, isResponding: false, currentTaskId: null });
            refresh(view);
            await settle();
            refresh(view);
            expect(lastReport()).toBe(true);
        });

        test("the verdict is withdrawn when the panel goes away", async () => {
            const view = await reload();
            expect(lastReport()).toBe(true);

            view.unmount();

            expect(lastReport()).toBe(false);
        });
    });
});
