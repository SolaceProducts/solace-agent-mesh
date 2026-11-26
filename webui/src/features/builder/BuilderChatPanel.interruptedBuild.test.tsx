// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Children, isValidElement, type ReactElement, type ReactNode } from "react";
import { act, fireEvent, render, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import type { MessageFE } from "@/types";

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";

describe("BuilderChatPanel: a build that was cut short", () => {
    let BuilderChatPanel: typeof import("./BuilderChatPanel").BuilderChatPanel;
    let capturedRenderAddon: ((m: MessageFE, i: number) => ReactNode) | null;
    let manifest: Record<string, unknown>;
    let canvasEntries: Array<[string, Partial<ComponentCanvasData>]>;
    let chat: { isResponding: boolean; isCancelling: boolean; currentTaskId: string | null; sessionId: string; messages: MessageFE[]; handleSubmit: ReturnType<typeof vi.fn> };
    let panelProps: Record<string, unknown>;
    let prior: MessageFE[];
    const stopTestRef: { current: (() => void) | null } = { current: null };
    let ctxFns: Record<string, () => void>;
    let cachedCanvas: { entries: unknown; map: Map<string, ComponentCanvasData> };
    let capturedHideId: string | undefined;
    const taskCtx = { monitoredTasks: {}, loadTaskFromBackend: vi.fn() };
    let stable: { saveManifest: ReturnType<typeof vi.fn>; handlers: Record<string, () => void> };

    const GENERATING = "Generating the TideAgent configuration...";
    const STOPPED_STEP = "Generating the TideAgent configuration (interrupted)";
    const draftAgent = { filename: "tide.yaml", type: "agent", name: "TideAgent", status: "draft", origin: "session" };
    const secondAgent = { filename: "tide2.yaml", type: "agent", name: "SecondAgent", status: "draft", origin: "session" };

    const userRequest = { isUser: true, parts: [{ kind: "text", text: "Build a tide agent" }], metadata: { messageId: "u1" } } as unknown as MessageFE;
    const approval = { isUser: true, parts: [{ kind: "text", text: "Build plan approved. Please generate the configs." }], metadata: { messageId: "u2" } } as unknown as MessageFE;
    const planMsg = { isUser: false, isComplete: true, taskId: "t1", parts: [{ kind: "text", text: "Here is the plan" }], metadata: { messageId: "a1" } } as unknown as MessageFE;

    function buildTurn(overrides: Record<string, unknown> = {}): MessageFE {
        return {
            isUser: false,
            isComplete: true,
            taskId: "t2",
            parts: [{ kind: "text", text: "Working on your build." }],
            progressUpdates: [{ type: "status", text: GENERATING, timestamp: 1 }],
            metadata: { messageId: "a2" },
            ...overrides,
        } as unknown as MessageFE;
    }

    const endMessage = (overrides: Record<string, unknown>) => ({ isUser: false, isComplete: true, taskId: "t2", metadata: { messageId: "a3" }, ...overrides }) as unknown as MessageFE;
    const cancelledTurn = () => buildTurn({ parts: [{ kind: "text", text: "Task was cancelled." }] });
    const errorMessage = () => endMessage({ isError: true, parts: [{ kind: "text", text: "Error: Bad request: model: claude-nonexistent-9" }] });
    const questionTurn = () => buildTurn({ parts: [{ kind: "text", text: "Which model should the agent use?" }] });

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
        chat = { isResponding: false, isCancelling: false, currentTaskId: null, sessionId: "s1", messages: [...prior, cancelledTurn()], handleSubmit: vi.fn() };
        panelProps = {};
        capturedHideId = undefined;
        cachedCanvas = { entries: undefined, map: new Map() };
        ctxFns = { setActiveSidePanelTab: vi.fn(), setTaskIdInSidePanel: vi.fn(), openSidePanelTab: vi.fn(), displayError: vi.fn() };
        stable = {
            saveManifest: vi.fn(),
            handlers: { onModeChange: vi.fn(), onBuilderActiveTabChange: vi.fn(), onTestRunningChange: vi.fn(), onLatestRunTaskIdChange: vi.fn(), onSelectTestActivityRun: vi.fn(), onApplyingChangesChange: vi.fn(), onBuildActivate: vi.fn() },
        };

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
                ...ctxFns,
                allArtifacts: [],
            }),
            useTaskContext: () => taskCtx,
            useUIModeContext: () => ({ showActivityPanel: true }),
        }));

        vi.doMock("@/features/chat/ChatArea", () => ({
            ChatArea: (props: { renderMessageAddon?: (m: MessageFE, i: number) => ReactNode; hideInlineActivityForMessageId?: string }) => {
                capturedRenderAddon = props.renderMessageAddon ?? null;
                capturedHideId = props.hideInlineActivityForMessageId;
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
            stopTestRef: stopTestRef,
            onLatestRunTaskIdChange: stable.handlers.onLatestRunTaskIdChange,
            selectedTestActivityRun: null,
            onSelectTestActivityRun: stable.handlers.onSelectTestActivityRun,
            isApplyingChanges: false,
            onApplyingChangesChange: stable.handlers.onApplyingChangesChange,
            canBuildActivate: false,
            isBuildActivating: false,
            onBuildActivate: stable.handlers.onBuildActivate,
            ...panelProps,
        };
    }

    type Panel = ReturnType<typeof render>;
    const refresh = (view: Panel) => view.rerender(<BuilderChatPanel {...baseProps()} />);

    function pressStopAndFinish(view: Panel) {
        chat.isResponding = true;
        chat.currentTaskId = "t2";
        refresh(view);
        chat.isCancelling = true;
        refresh(view);
        chat.isCancelling = false;
        chat.isResponding = false;
        chat.currentTaskId = null;
        refresh(view);
    }

    type Ending = "stopped" | "errored" | "failed-task" | "completed" | "running";

    function mountPanel(ending: Ending): Panel {
        const view = render(<BuilderChatPanel {...baseProps()} />);
        if (ending === "stopped") pressStopAndFinish(view);
        if (ending === "errored") chat.messages = [...prior, buildTurn(), errorMessage()];
        if (ending === "failed-task") chat.messages = [...prior, buildTurn({ isError: true })];
        if (ending === "running") {
            chat.isResponding = true;
            chat.currentTaskId = "t2";
            chat.messages = [...prior, buildTurn({ isComplete: false, parts: [] })];
        }
        if (ending === "completed") chat.messages = [...prior, questionTurn()];
        refresh(view);
        expect(capturedRenderAddon).toBeTypeOf("function");
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
        const timeline = progress ? render(<TooltipProvider>{progress}</TooltipProvider>) : null;
        const resumeIn = (v: Panel | null) => (v ? within(v.container).queryAllByRole("button", { name: /Resume build/i }).length : 0);
        const has = (v: Panel | null, text: string | RegExp) => !!v && within(v.container).queryByText(text) !== null;
        const cardButton = (name: RegExp) => within(card.container).queryByRole("button", { name }) !== null;
        return {
            card,
            timeline,
            cardResumeCount: resumeIn(card),
            timelineResumeCount: resumeIn(timeline),
            cardNote: has(card, /This build was interrupted before it finished/i),
            label: has(timeline, "Build interrupted"),
            stoppedStep: has(timeline, STOPPED_STEP),
            runningStep: has(timeline, GENERATING),
            anyStep: has(timeline, /Generating the TideAgent configuration/),
            completionCheck: !!timeline?.container.querySelector(".lucide-circle-check-big"),
            cardButton,
        };
    }

    type Row = {
        name: string;
        ending: Ending;
        arrange?: () => void;
        resume: "card" | "timeline" | "none";
        cutShort: boolean;
        label: boolean;
        buildActivate?: boolean;
        tryAgain?: boolean;
    };

    const stoppedRow = (name: string, arrange?: () => void, extra: Partial<Row> = {}): Row => ({ name, ending: "stopped", arrange, resume: "card", cutShort: true, label: true, ...extra });
    const noneRow = (name: string, ending: Ending, arrange?: () => void, extra: Partial<Row> = {}): Row => ({ name, ending, arrange, resume: "none", cutShort: false, label: false, ...extra });

    const flow = { filename: "flow.yaml", type: "workflow", name: "Flow", status: "validated", origin: "session", nodes: [{ id: "n1", type: "agent", agent_name: "TideAgent" }] };
    const flowEntry: [string, Partial<ComponentCanvasData>] = ["Flow", { type: "workflow", status: "validated", origin: "session", yamlText: "ok" }];
    const childEntry = (status: string, yamlText: string): [string, Partial<ComponentCanvasData>] => ["TideAgent", { status, origin: "session", yamlText }];

    function workflowRows(): Row[] {
        const withChild =
            (status: string, yamlText: string, extra: () => void = () => {}) =>
            () => {
                manifest.components = [flow, { ...draftAgent, status }];
                canvasEntries = [flowEntry, childEntry(status, yamlText)];
                extra();
            };
        return [
            {
                name: "workflow: stopped with its only child errored: Resume build on the timeline alone (the plan card treats error as finished)",
                ending: "stopped",
                arrange: withChild("error", "failed"),
                resume: "timeline",
                cutShort: false,
                label: false,
            },
            { name: "workflow: stopped with its only child draft: Resume build on the plan card alone", ending: "stopped", arrange: withChild("draft", "written"), resume: "card", cutShort: true, label: true },
            { name: "workflow: turn ended normally with its only child errored: Resume build on the timeline alone", ending: "completed", arrange: withChild("error", "failed"), resume: "timeline", cutShort: false, label: false },
            {
                name: "workflow: turn ended normally with its only child a draft that kept its YAML: Resume build on the timeline alone, labelled interrupted",
                ending: "completed",
                arrange: withChild("draft", "written"),
                resume: "timeline",
                cutShort: false,
                label: true,
            },
            {
                name: "workflow: turn ended normally with a deploy failure standing and its child errored: Resume build on the timeline (Try again only re-saves what exists), Try again on the card",
                ending: "completed",
                arrange: withChild("error", "failed", () => (panelProps = { deployFailure: { message: "internal error", canRetry: true } })),
                resume: "timeline",
                cutShort: false,
                label: false,
                tryAgain: true,
            },
            {
                name: "workflow: turn ended normally with a deploy failure the builder is fixing and its child errored: Resume build on the timeline",
                ending: "completed",
                arrange: withChild("error", "failed", () => (panelProps = { deployFailure: { message: "internal error", canRetry: false } })),
                resume: "timeline",
                cutShort: false,
                label: false,
            },
            {
                name: "workflow: stopped with a deploy failure standing and its child draft: Resume build on the timeline, Try again on the card",
                ending: "stopped",
                arrange: withChild("draft", "written", () => (panelProps = { deployFailure: { message: "internal error", canRetry: true } })),
                resume: "timeline",
                cutShort: false,
                label: true,
                tryAgain: true,
            },
            {
                name: "workflow: turn ended normally while a save or deploy runs and its child is draft: Resume build on the timeline",
                ending: "completed",
                arrange: withChild("draft", "written", () => (panelProps = { isBuildActivating: true })),
                resume: "timeline",
                cutShort: false,
                label: true,
            },
            {
                name: "workflow: stopped while a save or deploy runs and its child is draft: Resume build on the timeline",
                ending: "stopped",
                arrange: withChild("draft", "written", () => (panelProps = { isBuildActivating: true })),
                resume: "timeline",
                cutShort: false,
                label: true,
            },
            {
                name: "workflow: platform workflow, child draft, stopped: Resume build on the plan card",
                ending: "stopped",
                arrange: () => {
                    manifest.components = [{ ...flow, origin: "platform" }, { ...draftAgent }];
                    canvasEntries = [["Flow", { type: "workflow", status: "validated", origin: "platform", yamlText: "ok" }], childEntry("draft", "written")];
                },
                resume: "card",
                cutShort: true,
                label: true,
            },
            {
                name: "workflow: platform workflow, child errored, turn ended normally: nothing of this build finished, no Resume build",
                ending: "completed",
                arrange: () => {
                    manifest.components = [
                        { ...flow, origin: "platform" },
                        { ...draftAgent, status: "error" },
                    ];
                    canvasEntries = [["Flow", { type: "workflow", status: "validated", origin: "platform", yamlText: "ok" }], childEntry("error", "failed")];
                },
                resume: "none",
                cutShort: false,
                label: false,
            },
            {
                name: "workflow: session workflow and child both draft, stopped: Resume build on the plan card",
                ending: "stopped",
                arrange: () => {
                    manifest.components = [{ ...flow, status: "draft" }, { ...draftAgent }];
                    canvasEntries = [["Flow", { type: "workflow", status: "draft", origin: "session", yamlText: "ok" }], childEntry("draft", "written")];
                },
                resume: "card",
                cutShort: true,
                label: true,
            },
            {
                name: "workflow: turn ended normally with an open connector prompt and another component validated: no Resume build above the prompt",
                ending: "completed",
                arrange: () => {
                    manifest.components = [flow, { ...draftAgent }, { filename: "k.json", type: "connector", name: "K", status: "draft", origin: "session" }, { ...secondAgent, status: "validated" }];
                    canvasEntries = [
                        flowEntry,
                        ["TideAgent", { status: "draft", origin: "session" }],
                        ["K", { type: "connector", status: "draft", origin: "session", isWaitingForInput: true }],
                        ["SecondAgent", { status: "validated", origin: "session", yamlText: "ok" }],
                    ];
                },
                resume: "none",
                cutShort: false,
                label: false,
            },
        ];
    }

    const rows: Row[] = [
        stoppedRow("stopped before any config was written"),
        stoppedRow("stopped while the last config was streaming (partial text, every row reads as done)", () => {
            canvasEntries = [["TideAgent", { yamlText: "name: TideAgent\nhalf" }]];
        }),
        stoppedRow("stopped between two components (one validated, one not started)", () => {
            manifest.components = [{ ...draftAgent, status: "validated" }, secondAgent];
            canvasEntries = [["TideAgent", { yamlText: "ok" }]];
        }),
        stoppedRow("stopped with a finished component and a pending one (the timeline fallback would also fire): one Resume build, on the plan card", () => {
            manifest.components = [draftAgent, secondAgent];
            canvasEntries = [["TideAgent", { yamlText: "ok" }]];
        }),
        stoppedRow(
            "stopped while a stale validation stamp still offers Build & Activate",
            () => {
                manifest.components = [{ ...draftAgent, status: "validated" }, secondAgent];
                manifest.full_validation_passed = true;
                canvasEntries = [["TideAgent", { yamlText: "ok" }]];
                panelProps = { canBuildActivate: true };
            },
            { buildActivate: true }
        ),
        { name: "errored before any config was written (an RPC error: a separate message with no steps)", ending: "errored", resume: "card", cutShort: true, label: true },
        { name: "errored before any config was written (a failed task: the error is on the build message itself)", ending: "failed-task", resume: "card", cutShort: true, label: true },
        noneRow("completed ending in a question, nothing built yet (a normal turn is never flagged)", "completed"),
        {
            name: "revisited mid-build: a finished component, another pending, nothing responding, no Stop or error on this page load (the timeline fallback only, no plan-card surface)",
            ending: "completed",
            arrange: () => {
                manifest.components = [draftAgent, secondAgent];
                canvasEntries = [["TideAgent", { yamlText: "ok" }]];
            },
            resume: "timeline",
            cutShort: false,
            label: true,
        },
        noneRow("completed and built (every component deployed)", "completed", () => {
            manifest.components = [{ ...draftAgent, status: "deployed" }];
            manifest.full_validation_passed = true;
        }),
        noneRow(
            "completed and buildable (validated, Build & Activate offered)",
            "completed",
            () => {
                manifest.components = [{ ...draftAgent, status: "validated" }];
                manifest.full_validation_passed = true;
                panelProps = { canBuildActivate: true };
            },
            { buildActivate: true }
        ),
        noneRow("still running", "running"),
        noneRow("stopped after every component finished (nothing left to resume)", "stopped", () => {
            manifest.components = [{ ...draftAgent, status: "validated" }];
        }),
        noneRow(
            "stopped with a deploy failure standing (Try again)",
            "stopped",
            () => {
                panelProps = { deployFailure: { message: "internal error", canRetry: true } };
            },
            { tryAgain: true }
        ),
        noneRow("stopped while the builder is fixing a deploy failure", "stopped", () => {
            panelProps = { deployFailure: { message: "internal error", canRetry: false } };
        }),
        noneRow("stopped while a save or deploy is running", "stopped", () => {
            panelProps = { isBuildActivating: true };
        }),
        noneRow("stopped during an Apply Changes edit", "stopped", () => {
            panelProps = { isApplyingChanges: true };
        }),
        ...workflowRows(),
    ];

    test.each(rows)("$name", row => {
        row.arrange?.();
        mountPanel(row.ending);

        const v = showChat();

        expect(v.cardResumeCount).toBe(row.resume === "card" ? 1 : 0);
        expect(v.timelineResumeCount).toBe(row.resume === "timeline" ? 1 : 0);
        expect(v.cardNote).toBe(row.resume === "card");
        expect(v.label).toBe(row.label);
        expect(v.stoppedStep).toBe(row.cutShort);
        if (row.cutShort) expect(v.completionCheck).toBe(false);
        expect(v.cardButton(/Build & Activate/i)).toBe(!!row.buildActivate);
        expect(v.cardButton(/Try again/i)).toBe(!!row.tryAgain);
        if (row.ending !== "running" && !row.name.includes("Apply Changes")) expect(v.timeline ? within(v.timeline.container).queryAllByText(/Activity Timeline/).length : 0).toBeGreaterThan(0);
    });

    test("the timeline's Resume build is withdrawn as soon as the builder responds, even while the entries have not caught up", () => {
        manifest.components = [draftAgent, secondAgent];
        canvasEntries = [["TideAgent", { status: "draft", yamlText: "ok" }]];
        chat.messages = [...prior, questionTurn()];
        const view = render(<BuilderChatPanel {...baseProps()} />);
        refresh(view);
        expect(showChat().timelineResumeCount).toBe(1);

        chat.isResponding = true;
        refresh(view);

        expect(showChat().timelineResumeCount).toBe(0);
    });

    test("a save or deploy starting after mount, or a deploy failure arriving, leaves the single Resume build on the timeline", () => {
        manifest.components = [flow, { ...draftAgent, status: "draft" }];
        canvasEntries = [flowEntry, childEntry("draft", "written")];
        const view = mountPanel("completed");
        expect(showChat().timelineResumeCount).toBe(1);

        panelProps = { isBuildActivating: true, deployFailure: { message: "internal error", canRetry: true } };
        refresh(view);

        const v = showChat();
        expect(v.cardResumeCount).toBe(0);
        expect(v.timelineResumeCount).toBe(1);
    });

    test("a build that finished normally keeps its steps on the message that streamed them, not on a later one", () => {
        manifest.components = [{ ...draftAgent, status: "deployed" }];
        chat.messages = [...prior, buildTurn(), endMessage({ parts: [{ kind: "text", text: "Your agent is ready." }] })];
        const view = render(<BuilderChatPanel {...baseProps()} />);
        refresh(view);

        const v = showChat();

        expect(v.anyStep).toBe(false);
    });

    test("a running turn keeps its live line, not a stopped step", () => {
        mountPanel("running");

        const v = showChat();

        expect(v.runningStep).toBe(true);
        expect(v.stoppedStep).toBe(false);
    });

    test("a finished build reads Approved and keeps its completion check", () => {
        manifest.components = [{ ...draftAgent, status: "deployed" }];
        manifest.full_validation_passed = true;
        canvasEntries = [["TideAgent", { yamlText: "ok", status: "deployed" }]];
        mountPanel("completed");

        const v = showChat();

        expect(within(v.card.container).getByLabelText("Approved")).toBeInTheDocument();
        expect(v.completionCheck).toBe(true);
    });

    describe("what the Stop is remembered for", () => {
        test("another session is not marked after a Stop in this one", () => {
            const view = mountPanel("stopped");
            chat.sessionId = "s2";
            refresh(view);

            const v = showChat();

            expect(v.cardResumeCount + v.timelineResumeCount).toBe(0);
            expect(v.cardNote).toBe(false);
        });

        test("a cancel that failed, with the turn then ending on its own, is not a Stop", () => {
            const view = render(<BuilderChatPanel {...baseProps()} />);
            chat.isResponding = true;
            chat.currentTaskId = "t2";
            refresh(view);
            chat.isCancelling = true;
            refresh(view);
            chat.isCancelling = false;
            refresh(view);
            chat.isResponding = false;
            chat.messages = [...prior, questionTurn()];
            refresh(view);

            const v = showChat();

            expect(v.cardResumeCount + v.timelineResumeCount).toBe(0);
        });

        test("no Resume build while the cancel is still being processed, and it appears once that clears", () => {
            const view = render(<BuilderChatPanel {...baseProps()} />);
            chat.isResponding = true;
            chat.currentTaskId = "t2";
            refresh(view);
            chat.isCancelling = true;
            refresh(view);
            chat.isResponding = false;
            chat.currentTaskId = null;
            chat.messages = [...prior, buildTurn(), errorMessage()];
            refresh(view);

            expect(showChat().cardResumeCount).toBe(0);

            chat.isCancelling = false;
            refresh(view);

            expect(showChat().cardResumeCount).toBe(1);
        });

        test("no second Resume build while the first submission is starting its task", () => {
            const view = mountPanel("stopped");
            chat.isResponding = true;
            refresh(view);

            const v = showChat();

            expect(v.cardResumeCount).toBe(0);
            expect(v.cardNote).toBe(false);
        });

        test("the timeline's own Resume build is withheld while a cancel is being processed", () => {
            manifest.components = [draftAgent, secondAgent];
            canvasEntries = [["TideAgent", { yamlText: "ok" }]];
            chat.messages = [...prior, questionTurn()];
            chat.isCancelling = true;
            const view = render(<BuilderChatPanel {...baseProps()} />);
            refresh(view);

            expect(showChat().timelineResumeCount).toBe(0);

            chat.isCancelling = false;
            refresh(view);

            expect(showChat().timelineResumeCount).toBe(1);
        });

        test("a Stop stays after a resume submission that never started a task", () => {
            const view = mountPanel("stopped");
            chat.isResponding = true;
            refresh(view);
            chat.isResponding = false;
            refresh(view);

            expect(showChat().cardResumeCount).toBe(1);
        });

        test("a later turn that completes normally is not marked", () => {
            const view = mountPanel("stopped");
            chat.isResponding = true;
            chat.currentTaskId = "t3";
            chat.messages = [...chat.messages, { isUser: true, parts: [{ kind: "text", text: "resume" }], metadata: { messageId: "u3" } } as unknown as MessageFE];
            refresh(view);
            chat.isResponding = false;
            chat.messages = [...chat.messages, buildTurn({ taskId: "t3", metadata: { messageId: "a3" }, parts: [{ kind: "text", text: "Which model?" }] })];
            refresh(view);

            const v = showChat();

            expect(v.cardResumeCount + v.timelineResumeCount).toBe(0);
        });
    });

    describe("the manifest briefly reads as missing after approval", () => {
        test("keeps the interrupted state from the last manifest it saw", () => {
            const view = mountPanel("stopped");
            manifest = null as unknown as Record<string, unknown>;
            refresh(view);

            expect(showChat().cardResumeCount).toBe(1);
        });
    });

    describe("the timeline is collapsed onto its anchor after an earlier build finished", () => {
        afterEach(() => {
            vi.useRealTimers();
            sessionStorage.clear();
            localStorage.removeItem("sam_builder_skip_activate_confirm");
        });

        test("a later interrupted turn does not move the hidden timeline off the anchor", () => {
            vi.useFakeTimers();
            localStorage.setItem("sam_builder_skip_activate_confirm", "true");
            chat.messages = [userRequest, planMsg];
            const view = render(<BuilderChatPanel {...baseProps()} />);
            const unapproved = render(<TooltipProvider>{capturedRenderAddon!(planMsg, 0)}</TooltipProvider>);
            fireEvent.click(within(unapproved.container).getByRole("button", { name: /Build & Activate/i }));
            unapproved.unmount();
            manifest.components = [{ ...draftAgent, status: "deployed" }];
            manifest.full_validation_passed = true;
            canvasEntries = [["TideAgent", { yamlText: "ok", status: "deployed" }]];
            chat.messages = [...prior, buildTurn({ parts: [{ kind: "text", text: "Your agent is ready." }] })];
            refresh(view);
            const done = showChat();
            act(() => {
                vi.advanceTimersByTime(2000);
            });
            done.card.unmount();
            done.timeline?.unmount();
            refresh(view);

            manifest.components = [{ ...draftAgent, status: "deployed" }, secondAgent];
            const followUp = { isUser: true, parts: [{ kind: "text", text: "Add a second agent" }], metadata: { messageId: "u3" } } as unknown as MessageFE;
            chat.messages = [...chat.messages, followUp, buildTurn({ taskId: "t3", metadata: { messageId: "a3" } }), endMessage({ taskId: "t3", metadata: { messageId: "a4" }, isError: true, parts: [{ kind: "text", text: "Error: Bad request" }] })];
            refresh(view);

            expect(capturedHideId).toBe("a1");
        });
    });

    describe("the panel's memoised renderer follows what it depends on", () => {
        test("an error message arriving after the turn ended, with nothing else changing, brings the way forward and the borrowed steps", () => {
            const view = mountPanel("completed");
            expect(showChat().cardResumeCount).toBe(0);

            chat.messages = [...prior, buildTurn(), errorMessage()];
            refresh(view);

            const v = showChat();
            expect(v.cardResumeCount).toBe(1);
            expect(v.stoppedStep).toBe(true);
            expect(capturedHideId).toBe("a2");
        });

        test("a new submit handler is the one Resume build calls", () => {
            const view = mountPanel("stopped");
            const next = vi.fn();
            chat.handleSubmit = next;
            refresh(view);

            fireEvent.click(within(showChat().card.container).getByRole("button", { name: /Resume build/i }));

            expect(next).toHaveBeenCalledTimes(1);
        });
    });

    describe("Resume build", () => {
        test("asks the builder to continue, flagged as a resume", () => {
            mountPanel("stopped");
            const v = showChat();

            fireEvent.click(within(v.card.container).getByRole("button", { name: /Resume build/i }));

            expect(chat.handleSubmit).toHaveBeenCalledTimes(1);
            const args = chat.handleSubmit.mock.calls[0];
            expect(args[2]).toBe("Please continue the build from where it stopped.");
            expect(args[8]).toEqual({ resume_build: true });
        });
    });

    describe("before the plan was approved", () => {
        beforeEach(() => {
            prior = [userRequest, planMsg];
            chat.messages = [...prior, cancelledTurn()];
        });

        test.each<[string, Ending]>([
            ["Stop while drafting the plan", "stopped"],
            ["an LLM error while drafting the plan", "errored"],
        ])("%s leaves Keep Editing and Build & Activate, and no Resume build", (_name, ending) => {
            mountPanel(ending);

            const v = showChat();
            const keepEditing = v.cardButton(/Keep Editing/i) || (!!v.timeline && within(v.timeline.container).queryByRole("button", { name: /Keep Editing/i }) !== null);

            expect(keepEditing).toBe(true);
            expect(v.cardResumeCount + v.timelineResumeCount).toBe(0);
            expect(v.label).toBe(false);
        });
    });
});
