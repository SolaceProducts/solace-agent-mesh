// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps } from "react";
import { render, screen, within } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { describe, expect, test } from "vitest";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";
import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { buildCandidateEntry, type BuildContext, type ComponentCanvasData, deriveGeneratingFilenames } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";
import type { BuilderActiveTab } from "../useBuilderPageState";
import { isBuildInterruptedOnCanvas } from "../workflowChildStatus";

import { computeCanvasLayout } from "./canvasLayout";
import { CanvasViewProvider } from "./canvasViewContext";
import { WfAgentFlowNode, WfStartFlowNode } from "./WorkflowFlowNodes";

function canvasData(over: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
    return {
        name: "UiOrders",
        filename: "ui-orders.yaml",
        type: "agent",
        status: "deployed",
        origin: "session",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...over,
    };
}

const LIVE_ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";
const LIVE_AGENT_CARD = "agent_01a160d0_2474_7426_830a_ae0397fcdc9a";
const LIVE_WORKFLOW_CARD = "workflow_01a160d0_2474_7426_830a_ae0397fcdc9a";

interface RenderOpts {
    tab?: BuilderActiveTab;
    liveAgentCards?: ReadonlyArray<{ name?: string; displayName?: string }>;
    extra?: Record<string, unknown>;
    buildInterrupted?: boolean;
}

function renderInCanvas(ui: React.ReactNode, { tab = "build", liveAgentCards, buildInterrupted = false }: RenderOpts = {}) {
    return render(
        <CanvasViewProvider builderActiveTab={tab} testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={false} buildInterrupted={buildInterrupted} liveAgentCards={liveAgentCards}>
            <ReactFlowProvider>{ui}</ReactFlowProvider>
        </CanvasViewProvider>
    );
}

function agentNode(data: Partial<ComponentCanvasData>, opts: RenderOpts = {}) {
    const props = makeNodeProps({
        layoutNode: createWorkflowLayoutNode({ id: "orders", type: "agent", data: { label: "UiOrders", agentName: "UiOrders" } }),
        agentCanvasData: canvasData(data),
        agentManifestStatus: data.status ?? "deployed",
        agentComponentName: "UiOrders",
        hasDepWaitingForInput: false,
        ...opts.extra,
    }) as ComponentProps<typeof WfAgentFlowNode>;
    return renderInCanvas(<WfAgentFlowNode {...props} />, opts);
}

function startNode(data: Partial<ComponentCanvasData>, opts: RenderOpts = {}) {
    const props = makeNodeProps({
        layoutNode: createWorkflowLayoutNode({ id: "__start__", type: "start", data: { label: "Start" } }),
        workflowComponent: { name: "OrderFlow", status: data.status ?? "deployed", origin: data.origin ?? "session" },
        workflowCanvasData: canvasData({ name: "OrderFlow", type: "workflow", ...data }),
        allChildrenDone: true,
        ...opts.extra,
    }) as ComponentProps<typeof WfStartFlowNode>;
    return renderInCanvas(<WfStartFlowNode {...props} />, opts);
}

const NODES = [
    ["agent", agentNode],
    ["start", startNode],
] as const;

describe.each(NODES)("workflow-inline %s card status", (_kind, renderNode) => {
    test("an already-deployed card being regenerated reads Editing, not Creating", () => {
        renderNode({ isGenerating: true });
        expect(screen.getByText("Editing")).toBeInTheDocument();
        expect(screen.queryByText("Creating")).toBeNull();
    });

    test("a brand-new card being generated still reads Creating", () => {
        renderNode({ isGenerating: true, status: "draft" });
        expect(screen.getByText("Creating")).toBeInTheDocument();
        expect(screen.queryByText("Editing")).toBeNull();
    });

    test("a settled deployed card reads Active", () => {
        renderNode({});
        expect(screen.getByText("Active")).toBeInTheDocument();
    });

    test("a validated, undeployed card reads Ready", () => {
        renderNode({ status: "validated" });
        expect(screen.getByText("Ready")).toBeInTheDocument();
        expect(screen.queryByText("Active")).toBeNull();
    });

    test("an unbuilt card reads Draft", () => {
        renderNode({ status: "draft" });
        expect(screen.getByText("Draft")).toBeInTheDocument();
    });

    test("a queued card reads Creating", () => {
        renderNode({ status: "draft", isQueued: true });
        expect(screen.getByText("Creating")).toBeInTheDocument();
        expect(screen.queryByText("Draft")).toBeNull();
    });

    test("a card whose dependencies are being built reads Creating", () => {
        renderNode({ status: "draft", isBuildingDeps: true });
        expect(screen.getByText("Creating")).toBeInTheDocument();
        expect(screen.queryByText("Draft")).toBeNull();
    });

    test("a failed card reads Error", () => {
        renderNode({ status: "error" });
        expect(screen.getByText("Error")).toBeInTheDocument();
    });

    test("a card waiting for input reads the waiting text", () => {
        renderNode({ isWaitingForInput: true });
        expect(screen.getByText("Waiting for Input...")).toBeInTheDocument();
        expect(screen.queryByText("Active")).toBeNull();
    });

    test("an interrupted build reads Paused on an unbuilt card, not Draft", () => {
        renderNode({ status: "draft" }, { buildInterrupted: true });
        expect(screen.getByText("Paused")).toBeInTheDocument();
        expect(screen.queryByText("Draft")).toBeNull();
    });

    test("an interrupted build reads Paused on a queued card, not Creating", () => {
        renderNode({ status: "draft", isQueued: true }, { buildInterrupted: true });
        expect(screen.getByText("Paused")).toBeInTheDocument();
        expect(screen.queryByText("Creating")).toBeNull();
    });

    test("a platform-origin card reads Active whatever its manifest status", () => {
        renderNode({ status: "draft", origin: "platform" });
        expect(screen.getByText("Active")).toBeInTheDocument();
        expect(screen.queryByText("Draft")).toBeNull();
    });

    test("a card live on the mesh reads Active even when its manifest status lags", () => {
        renderNode({ status: "validated", id: LIVE_ID }, { liveAgentCards: [{ name: renderNode === agentNode ? LIVE_AGENT_CARD : LIVE_WORKFLOW_CARD }] });
        expect(screen.getByText("Active")).toBeInTheDocument();
        expect(screen.queryByText("Ready")).toBeNull();
    });

    test("a card sharing its name with a live component another session deployed does not read Active", () => {
        renderNode({ status: "validated" }, { liveAgentCards: [{ name: renderNode === agentNode ? LIVE_AGENT_CARD : LIVE_WORKFLOW_CARD, displayName: renderNode === agentNode ? "UiOrders" : "OrderFlow" }] });
        expect(screen.queryByText("Active")).toBeNull();
        expect(screen.getByText("Ready")).toBeInTheDocument();
    });

    test("a failed update of a card live on the mesh reads Active, as the standalone cards do", () => {
        renderNode({ status: "error", id: LIVE_ID }, { liveAgentCards: [{ name: renderNode === agentNode ? LIVE_AGENT_CARD : LIVE_WORKFLOW_CARD }] });
        expect(screen.getByText("Active")).toBeInTheDocument();
        expect(screen.queryByText("Error")).toBeNull();
    });

    describe("Test mode", () => {
        test("shows no status pill on a deployed card", () => {
            renderNode({}, { tab: "test" });
            expect(screen.queryByText("Active")).toBeNull();
            expect(screen.queryByText("Ready")).toBeNull();
        });

        test("shows no status pill on a failed card, as the standalone cards do", () => {
            renderNode({ status: "error" }, { tab: "test" });
            expect(screen.queryByText("Error")).toBeNull();
        });

        test("shows no pill on a regenerating card", () => {
            renderNode({ isGenerating: true }, { tab: "test" });
            expect(screen.queryByText("Editing")).toBeNull();
            expect(screen.queryByText("Creating")).toBeNull();
        });
    });
});

describe("workflow-inline agent card dependency wait", () => {
    test("a connector dependency waiting for input reads the waiting text", () => {
        agentNode({}, { extra: { hasDepWaitingForInput: true } });
        expect(screen.getByText("Waiting for Input...")).toBeInTheDocument();
        expect(screen.queryByText("Active")).toBeNull();
    });

    test("the dependency wait wins over Paused", () => {
        agentNode({ status: "draft" }, { buildInterrupted: true, extra: { hasDepWaitingForInput: true } });
        expect(screen.getByText("Waiting for Input...")).toBeInTheDocument();
        expect(screen.queryByText("Paused")).toBeNull();
    });

    test("Test mode ignores a dependency wait", () => {
        agentNode({}, { tab: "test", extra: { hasDepWaitingForInput: true } });
        expect(screen.queryByText("Waiting for Input...")).toBeNull();
    });

    test("an agent with no canvas data that is live on the mesh reads Active even when its manifest status is validated", () => {
        const props = makeNodeProps({
            layoutNode: createWorkflowLayoutNode({ id: "stock", type: "agent", data: { label: "UiStockAPI", agentName: "UiStockAPI" } }),
            agentCanvasData: null,
            agentManifestStatus: "validated",
            agentManifestId: LIVE_ID,
            agentComponentName: "UiStockAPI",
        }) as ComponentProps<typeof WfAgentFlowNode>;
        renderInCanvas(<WfAgentFlowNode {...props} />, { liveAgentCards: [{ name: LIVE_AGENT_CARD }] });
        expect(screen.getByText("Active")).toBeInTheDocument();
        expect(screen.queryByText("Ready")).toBeNull();
    });

    test("an agent with no canvas data reads Active from its deployed manifest status", () => {
        const props = makeNodeProps({
            layoutNode: createWorkflowLayoutNode({ id: "stock", type: "agent", data: { label: "UiStockAPI", agentName: "UiStockAPI" } }),
            agentCanvasData: null,
            agentManifestStatus: "deployed",
            agentComponentName: "UiStockAPI",
        }) as ComponentProps<typeof WfAgentFlowNode>;
        renderInCanvas(<WfAgentFlowNode {...props} />);
        expect(screen.getByText("Active")).toBeInTheDocument();
    });
});

describe("workflow-inline start card children", () => {
    test("a deployed workflow whose child agent is being rebuilt reads Editing", () => {
        startNode({ isBuilderResponding: true }, { extra: { allChildrenDone: false } });
        expect(screen.getByText("Editing")).toBeInTheDocument();
    });

    test("a validated, undeployed workflow whose child agent is being built reads Creating", () => {
        startNode({ status: "validated", isBuilderResponding: true }, { extra: { allChildrenDone: false } });
        expect(screen.getByText("Creating")).toBeInTheDocument();
        expect(screen.queryByText("Editing")).toBeNull();
    });

    test("an idle builder with a child agent still pending and no sign of a stopped build reads Active, not Editing or Paused", () => {
        startNode({}, { extra: { allChildrenDone: false } });
        expect(screen.getByText("Active")).toBeInTheDocument();
        expect(screen.queryByText("Editing")).toBeNull();
        expect(screen.queryByText("Paused")).toBeNull();
    });

    test("an idle builder with a child agent still pending in an interrupted build reads Paused, not Editing", () => {
        startNode({}, { buildInterrupted: true, extra: { allChildrenDone: false } });
        expect(screen.getByText("Paused")).toBeInTheDocument();
        expect(screen.queryByText("Editing")).toBeNull();
    });

    test("a start card with no canvas entry and a child pending never claims the builder is working", () => {
        const props = makeNodeProps({
            layoutNode: createWorkflowLayoutNode({ id: "__start__", type: "start", data: { label: "Start" } }),
            workflowComponent: { name: "OrderFlow", status: "deployed", origin: "session" },
            workflowCanvasData: null,
            allChildrenDone: false,
        }) as ComponentProps<typeof WfStartFlowNode>;
        const { container } = renderInCanvas(<WfStartFlowNode {...props} />);
        expect(container.querySelector(".animate-spin")).toBeNull();
        expect(screen.queryByText("Editing")).toBeNull();
    });

    test("a workflow with no allChildrenDone signal reads Active, not Editing", () => {
        startNode({ isBuilderResponding: true }, { extra: { allChildrenDone: undefined } });
        expect(screen.getByText("Active")).toBeInTheDocument();
        expect(screen.queryByText("Editing")).toBeNull();
    });

    test("without workflow metadata the node renders the plain Start pill and no status pill", () => {
        const props = makeNodeProps({
            layoutNode: createWorkflowLayoutNode({ id: "__start__", type: "start", data: { label: "Start" } }),
        }) as ComponentProps<typeof WfStartFlowNode>;
        renderInCanvas(<WfStartFlowNode {...props} />);
        expect(screen.getByText("Start")).toBeInTheDocument();
        expect(screen.queryByText("Active")).toBeNull();
        expect(screen.queryByText("Draft")).toBeNull();
    });
});

describe("workflow start card beside its child agents after a stopped build", () => {
    type ChildKind = "validated" | "draft" | "noConfig";

    function renderChild(kind: ChildKind, buildInterrupted: boolean) {
        const status = kind === "validated" ? "validated" : "draft";
        const props = makeNodeProps({
            layoutNode: createWorkflowLayoutNode({ id: "n0", type: "agent", data: { label: "Child", agentName: "Child" } }),
            agentCanvasData: kind === "noConfig" ? null : canvasData({ name: "Child", status }),
            agentManifestStatus: status,
            agentComponentName: "Child",
            hasDepWaitingForInput: false,
        }) as ComponentProps<typeof WfAgentFlowNode>;
        return renderInCanvas(<WfAgentFlowNode {...props} />, { buildInterrupted }).container;
    }

    function renderStart(status: string, allChildrenDone: boolean, buildInterrupted: boolean, opts: RenderOpts & { origin?: string; responding?: boolean } = {}) {
        const props = makeNodeProps({
            layoutNode: createWorkflowLayoutNode({ id: "__start__", type: "start", data: { label: "Start" } }),
            workflowComponent: { name: "OrderFlow", status, origin: opts.origin ?? "session" },
            workflowCanvasData: canvasData({ name: "OrderFlow", type: "workflow", status, origin: opts.origin ?? "session", isBuilderResponding: opts.responding ?? false }),
            allChildrenDone,
        }) as ComponentProps<typeof WfStartFlowNode>;
        return renderInCanvas(<WfStartFlowNode {...props} />, { ...opts, buildInterrupted }).container;
    }

    interface Row {
        start: string;
        child: ChildKind;
        childrenDone: boolean;
        interrupted: boolean;
        responding: boolean;
        startPill: string;
        startSpins: boolean;
        childPill: string;
    }

    const MATRIX: readonly Row[] = [
        { start: "validated", child: "validated", childrenDone: true, interrupted: false, responding: false, startPill: "Ready", startSpins: false, childPill: "Ready" },
        { start: "validated", child: "validated", childrenDone: true, interrupted: true, responding: false, startPill: "Ready", startSpins: false, childPill: "Ready" },
        { start: "validated", child: "draft", childrenDone: false, interrupted: false, responding: true, startPill: "Creating", startSpins: true, childPill: "Draft" },
        { start: "validated", child: "draft", childrenDone: false, interrupted: true, responding: false, startPill: "Paused", startSpins: false, childPill: "Paused" },
        { start: "validated", child: "noConfig", childrenDone: false, interrupted: false, responding: true, startPill: "Creating", startSpins: true, childPill: "Draft" },
        { start: "validated", child: "noConfig", childrenDone: false, interrupted: true, responding: false, startPill: "Paused", startSpins: false, childPill: "Paused" },
        { start: "draft", child: "validated", childrenDone: true, interrupted: false, responding: false, startPill: "Draft", startSpins: false, childPill: "Ready" },
        { start: "draft", child: "validated", childrenDone: true, interrupted: true, responding: false, startPill: "Paused", startSpins: false, childPill: "Ready" },
        { start: "draft", child: "draft", childrenDone: false, interrupted: false, responding: true, startPill: "Draft", startSpins: false, childPill: "Draft" },
        { start: "draft", child: "draft", childrenDone: false, interrupted: true, responding: false, startPill: "Paused", startSpins: false, childPill: "Paused" },
        { start: "draft", child: "noConfig", childrenDone: false, interrupted: false, responding: true, startPill: "Draft", startSpins: false, childPill: "Draft" },
        { start: "draft", child: "noConfig", childrenDone: false, interrupted: true, responding: false, startPill: "Paused", startSpins: false, childPill: "Paused" },
        { start: "validated", child: "draft", childrenDone: false, interrupted: false, responding: false, startPill: "Ready", startSpins: false, childPill: "Draft" },
        { start: "validated", child: "noConfig", childrenDone: false, interrupted: false, responding: false, startPill: "Ready", startSpins: false, childPill: "Draft" },
    ];

    test.each(MATRIX)(
        "start $start, child $child, build interrupted $interrupted, builder responding $responding: start reads $startPill, child reads $childPill",
        ({ start, child, childrenDone, interrupted, responding, startPill, startSpins, childPill }) => {
            const startContainer = renderStart(start, childrenDone, interrupted, { responding });
            const agentContainer = renderChild(child, interrupted);
            expect(within(startContainer).getByText(startPill)).toBeInTheDocument();
            expect(within(agentContainer).getByText(childPill)).toBeInTheDocument();
            expect(startContainer.querySelector(".animate-spin") !== null).toBe(startSpins);
        }
    );

    test("a deployed workflow whose child agents are paused reads Paused, not Editing", () => {
        const start = renderStart("deployed", false, true);
        expect(within(start).getByText("Paused")).toBeInTheDocument();
        expect(within(start).queryByText("Editing")).toBeNull();
        expect(start.querySelector(".animate-spin")).toBeNull();
    });

    test("a platform-origin start card with paused child agents reads Paused", () => {
        const start = renderStart("draft", false, true, { origin: "platform" });
        expect(within(start).getByText("Paused")).toBeInTheDocument();
        expect(start.querySelector(".animate-spin")).toBeNull();
    });

    test("Test mode shows no pill on the start card even when its children are paused", () => {
        const start = renderStart("validated", false, true, { tab: "test" });
        expect(within(start).queryByText("Paused")).toBeNull();
        expect(start.querySelector(".animate-spin")).toBeNull();
    });
});

describe("workflow start card derived from a real manifest", () => {
    const WF_NODES = [
        { id: "n0", type: "agent" as const, agent_name: "Done1" },
        { id: "n1", type: "agent" as const, agent_name: "Todo1" },
    ];
    const MANIFEST: BuildManifest = {
        name: "PausedFlow",
        components: [
            { filename: "PausedFlow.yaml", type: "workflow", name: "PausedFlow", origin: "session", status: "validated", nodes: WF_NODES },
            { filename: "Done1.yaml", type: "agent", name: "Done1", origin: "session", status: "validated" },
            { filename: "Todo1.yaml", type: "agent", name: "Todo1", origin: "session", status: "draft" },
        ],
    };

    function pills(todo: Partial<ComponentCanvasData>, everyEntry: Partial<ComponentCanvasData> = {}) {
        const componentData = new Map<string, ComponentCanvasData>([
            ["PausedFlow", canvasData({ name: "PausedFlow", type: "workflow", status: "validated", yamlText: "ok", workflowConfig: { nodes: WF_NODES } as ComponentCanvasData["workflowConfig"], ...everyEntry })],
            ["Done1", canvasData({ name: "Done1", status: "validated", yamlText: "ok", ...everyEntry })],
            ["Todo1", canvasData({ name: "Todo1", status: "draft", ...todo, ...everyEntry })],
        ]);
        const { nodes } = computeCanvasLayout(MANIFEST, componentData);
        const startProps = makeNodeProps(nodes.find(n => n.id === "wf:PausedFlow:__start__")!.data) as ComponentProps<typeof WfStartFlowNode>;
        const todoProps = makeNodeProps(nodes.find(n => n.id === "wf:PausedFlow:n1")!.data) as ComponentProps<typeof WfAgentFlowNode>;
        const buildInterrupted = isBuildInterruptedOnCanvas(false, componentData.values());
        return { start: renderInCanvas(<WfStartFlowNode {...startProps} />, { buildInterrupted }).container, todo: renderInCanvas(<WfAgentFlowNode {...todoProps} />, { buildInterrupted }).container };
    }

    test("a stopped build with an agent that was never generated pauses the start card and the agent", () => {
        const { start, todo } = pills({});
        expect(within(start).getByText("Paused")).toBeInTheDocument();
        expect(within(todo).getByText("Paused")).toBeInTheDocument();
        expect(start.querySelector(".animate-spin")).toBeNull();
    });

    test("a build still generating the agent keeps the start card on Creating", () => {
        const { start, todo } = pills({ isGenerating: true }, { isBuilderResponding: true });
        expect(within(start).getByText("Creating")).toBeInTheDocument();
        expect(within(todo).getByText("Creating")).toBeInTheDocument();
        expect(start.querySelector(".animate-spin")).not.toBeNull();
    });
});

describe("workflow start card across turn states and unfinished-child states", () => {
    type ChildState = "draftNoYaml" | "draftYaml" | "validated" | "deployed" | "errorYaml" | "errorNoYaml" | "noStatusYaml";
    type WorkflowStatus = "validated" | "deployed";
    type Turn = "stopped" | "live";

    const WF_NODES = [
        { id: "n0", type: "agent" as const, agent_name: "Done1" },
        { id: "n1", type: "agent" as const, agent_name: "Child" },
    ];
    const CHILD_STATES: Record<ChildState, { status: string | undefined; yaml: boolean }> = {
        draftNoYaml: { status: "draft", yaml: false },
        draftYaml: { status: "draft", yaml: true },
        validated: { status: "validated", yaml: true },
        deployed: { status: "deployed", yaml: true },
        errorYaml: { status: "error", yaml: true },
        errorNoYaml: { status: "error", yaml: false },
        noStatusYaml: { status: undefined, yaml: true },
    };

    function buildManifest(wfStatus: WorkflowStatus, child: ChildState): BuildManifest {
        return {
            name: "TableFlow",
            full_validation_passed: true,
            components: [
                { filename: "TableFlow.yaml", type: "workflow", name: "TableFlow", origin: "session", status: wfStatus, nodes: WF_NODES },
                { filename: "Done1.yaml", type: "agent", name: "Done1", origin: "session", status: "validated" },
                { filename: "Child.yaml", type: "agent", name: "Child", origin: "session", status: CHILD_STATES[child].status },
            ],
        };
    }

    function componentDataAsBuilderProduces(manifest: BuildManifest, withYaml: Set<string>, isResponding: boolean) {
        const generatingFilenames = deriveGeneratingFilenames([], manifest, isResponding, true);
        const ctx: BuildContext = {
            resolvedConfigs: new Map([...withYaml].map(filename => [filename, "name: written"])),
            loadingSet: new Set(),
            generatingFilenames,
            isResponding,
            buildFinalizing: isResponding && manifest.full_validation_passed !== true,
            hasAnyGenerating: generatingFilenames.size > 0,
            changedSinceValidation: new Set(),
            pendingInputComponentName: null,
            createdNames: new Set(manifest.components!.filter(c => withYaml.has(c.filename)).map(c => c.name)),
            a2aFallbackByName: new Map(),
        };
        return new Map(manifest.components!.map(c => [c.name, buildCandidateEntry(c, ctx, undefined)]));
    }

    function renderPair(wfStatus: WorkflowStatus, child: ChildState, turn: Turn) {
        const manifest = buildManifest(wfStatus, child);
        const withYaml = new Set(["TableFlow.yaml", "Done1.yaml", ...(CHILD_STATES[child].yaml ? ["Child.yaml"] : [])]);
        const componentData = componentDataAsBuilderProduces(manifest, withYaml, turn === "live");
        const { nodes } = computeCanvasLayout(manifest, componentData);
        const startData = nodes.find(n => n.id === "wf:TableFlow:__start__")!.data;
        const childData = nodes.find(n => n.id === "wf:TableFlow:n1")!.data;
        const interrupted = isBuildInterruptedOnCanvas(false, componentData.values());
        return {
            interrupted,
            start: renderInCanvas(<WfStartFlowNode {...(makeNodeProps(startData) as ComponentProps<typeof WfStartFlowNode>)} />, { buildInterrupted: interrupted }).container,
            child: renderInCanvas(<WfAgentFlowNode {...(makeNodeProps(childData) as ComponentProps<typeof WfAgentFlowNode>)} />, { buildInterrupted: interrupted }).container,
        };
    }

    interface Row {
        child: ChildState;
        turn: Turn;
        interrupted: boolean;
        validatedStartPill: string;
        deployedStartPill: string;
        startSpins: boolean;
        childPill: string;
    }

    const TABLE: readonly Row[] = [
        { child: "draftNoYaml", turn: "stopped", interrupted: true, validatedStartPill: "Paused", deployedStartPill: "Paused", startSpins: false, childPill: "Paused" },
        { child: "draftYaml", turn: "stopped", interrupted: true, validatedStartPill: "Paused", deployedStartPill: "Paused", startSpins: false, childPill: "Paused" },
        { child: "errorYaml", turn: "stopped", interrupted: true, validatedStartPill: "Paused", deployedStartPill: "Paused", startSpins: false, childPill: "Error" },
        { child: "errorNoYaml", turn: "stopped", interrupted: true, validatedStartPill: "Paused", deployedStartPill: "Paused", startSpins: false, childPill: "Error" },
        { child: "noStatusYaml", turn: "stopped", interrupted: true, validatedStartPill: "Paused", deployedStartPill: "Paused", startSpins: false, childPill: "Paused" },
        { child: "validated", turn: "stopped", interrupted: false, validatedStartPill: "Ready", deployedStartPill: "Active", startSpins: false, childPill: "Ready" },
        { child: "deployed", turn: "stopped", interrupted: false, validatedStartPill: "Ready", deployedStartPill: "Active", startSpins: false, childPill: "Active" },
        { child: "draftNoYaml", turn: "live", interrupted: false, validatedStartPill: "Creating", deployedStartPill: "Editing", startSpins: true, childPill: "Creating" },
        { child: "draftYaml", turn: "live", interrupted: false, validatedStartPill: "Creating", deployedStartPill: "Editing", startSpins: true, childPill: "Creating" },
        { child: "errorYaml", turn: "live", interrupted: false, validatedStartPill: "Creating", deployedStartPill: "Editing", startSpins: true, childPill: "Error" },
        { child: "errorNoYaml", turn: "live", interrupted: false, validatedStartPill: "Creating", deployedStartPill: "Editing", startSpins: true, childPill: "Error" },
        { child: "noStatusYaml", turn: "live", interrupted: false, validatedStartPill: "Creating", deployedStartPill: "Editing", startSpins: true, childPill: "Draft" },
        { child: "validated", turn: "live", interrupted: false, validatedStartPill: "Ready", deployedStartPill: "Active", startSpins: false, childPill: "Ready" },
        { child: "deployed", turn: "live", interrupted: false, validatedStartPill: "Ready", deployedStartPill: "Active", startSpins: false, childPill: "Active" },
    ];

    const CASES = TABLE.flatMap(row => (["validated", "deployed"] as const).map(wfStatus => ({ ...row, wfStatus, startPill: wfStatus === "validated" ? row.validatedStartPill : row.deployedStartPill })));

    test.each(CASES)("workflow $wfStatus, child $child, turn $turn: start reads $startPill (spins $startSpins), child reads $childPill", ({ child, turn, wfStatus, interrupted, startPill, startSpins, childPill }) => {
        const out = renderPair(wfStatus, child, turn);
        expect(out.interrupted).toBe(interrupted);
        expect(within(out.start).getByText(startPill)).toBeInTheDocument();
        expect(out.start.querySelector(".animate-spin") !== null).toBe(startSpins);
        expect(within(out.child).getByText(childPill)).toBeInTheDocument();
    });
    describe("a workflow the interrupted-build scan cannot count as finished work (idle builder, so nothing shows the build stopped)", () => {
        const SOLO_NODES = [{ id: "n1", type: "agent" as const, agent_name: "Child" }];

        function renderSoloStart(wf: { origin: string; status: string; id?: string }, turn: Turn, opts: RenderOpts = {}) {
            const manifest: BuildManifest = {
                name: "TableFlow",
                full_validation_passed: true,
                components: [
                    { filename: "TableFlow.yaml", type: "workflow", name: "TableFlow", id: wf.id, origin: wf.origin, status: wf.status, nodes: SOLO_NODES },
                    { filename: "Child.yaml", type: "agent", name: "Child", origin: "session", status: "error" },
                ],
            };
            const componentData = componentDataAsBuilderProduces(manifest, new Set(["TableFlow.yaml", "Child.yaml"]), turn === "live");
            const { nodes } = computeCanvasLayout(manifest, componentData);
            const startData = nodes.find(n => n.id === "wf:TableFlow:__start__")!.data;
            const interrupted = isBuildInterruptedOnCanvas(false, componentData.values());
            return { interrupted, start: renderInCanvas(<WfStartFlowNode {...(makeNodeProps(startData) as ComponentProps<typeof WfStartFlowNode>)} />, { ...opts, buildInterrupted: interrupted }).container };
        }

        test("a platform-origin workflow with an errored child reads Active with no spinner", () => {
            const { interrupted, start } = renderSoloStart({ origin: "platform", status: "deployed" }, "stopped");
            expect(interrupted).toBe(false);
            expect(within(start).getByText("Active")).toBeInTheDocument();
            expect(within(start).queryByText("Paused")).toBeNull();
            expect(start.querySelector(".animate-spin")).toBeNull();
        });

        test("the same workflow reads Editing with a spinner while the builder is responding", () => {
            const { start } = renderSoloStart({ origin: "platform", status: "deployed" }, "live");
            expect(within(start).getByText("Editing")).toBeInTheDocument();
            expect(start.querySelector(".animate-spin")).not.toBeNull();
        });

        test("a draft workflow that is live on the mesh with an errored child reads Active with no spinner", () => {
            const { interrupted, start } = renderSoloStart({ origin: "session", status: "draft", id: LIVE_ID }, "stopped", { liveAgentCards: [{ name: LIVE_WORKFLOW_CARD }] });
            expect(interrupted).toBe(false);
            expect(within(start).getByText("Active")).toBeInTheDocument();
            expect(within(start).queryByText("Paused")).toBeNull();
            expect(start.querySelector(".animate-spin")).toBeNull();
        });
    });
});
