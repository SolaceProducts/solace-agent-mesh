// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen, within } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { TooltipProvider } from "@/ui/tooltip";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { isApprovedBuildInterrupted } from "../buildInterruption";
import { buildCandidateEntry, type BuildContext, deriveGeneratingFilenames } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";
import { InlineCreationProgress } from "../InlineCreationProgress";
import { isBuildInterruptedOnCanvas } from "../workflowChildStatus";

import { computeCanvasLayout } from "./canvasLayout";
import { CanvasViewProvider } from "./canvasViewContext";
import { WfStartFlowNode } from "./WorkflowFlowNodes";

type ChildState = "draftNoYaml" | "draftYaml" | "errorYaml" | "errorNoYaml" | "noStatusYaml";

const CHILD_STATES: Record<ChildState, { status: string | undefined; yaml: boolean }> = {
    draftNoYaml: { status: "draft", yaml: false },
    draftYaml: { status: "draft", yaml: true },
    errorYaml: { status: "error", yaml: true },
    errorNoYaml: { status: "error", yaml: false },
    noStatusYaml: { status: undefined, yaml: true },
};

const LIVE_WORKFLOW_ID = "01a160d0-2474-7426-830a-ae0397fcdc9a";
const LIVE_WORKFLOW_CARD = "workflow_01a160d0_2474_7426_830a_ae0397fcdc9a";

interface WorkflowShape {
    label: string;
    origin: string;
    status: string;
    liveOnMesh: boolean;
    quietPill: string;
    finishedWork: boolean;
}

const SHAPES: WorkflowShape[] = [
    { label: "session workflow validated", origin: "session", status: "validated", liveOnMesh: false, quietPill: "Ready", finishedWork: true },
    { label: "session workflow deployed", origin: "session", status: "deployed", liveOnMesh: false, quietPill: "Active", finishedWork: true },
    { label: "platform workflow validated", origin: "platform", status: "validated", liveOnMesh: false, quietPill: "Active", finishedWork: false },
    { label: "platform workflow deployed", origin: "platform", status: "deployed", liveOnMesh: false, quietPill: "Active", finishedWork: false },
    { label: "session workflow draft", origin: "session", status: "draft", liveOnMesh: false, quietPill: "Draft", finishedWork: false },
    { label: "session workflow draft but live on the mesh", origin: "session", status: "draft", liveOnMesh: true, quietPill: "Active", finishedWork: false },
];

interface SessionOptions {
    shape: WorkflowShape;
    child: ChildState;
    otherChildFinished: boolean;
    isResponding: boolean;
    pendingInputComponentName?: string | null;
}

function buildSession({ shape, child, otherChildFinished, isResponding, pendingInputComponentName = null }: SessionOptions) {
    const nodes = [...(otherChildFinished ? [{ id: "n0", type: "agent" as const, agent_name: "Done1" }] : []), { id: "n1", type: "agent" as const, agent_name: "Child" }];
    const components: NonNullable<BuildManifest["components"]> = [{ filename: "W.yaml", type: "workflow", name: "W", ...(shape.liveOnMesh ? { id: LIVE_WORKFLOW_ID } : {}), origin: shape.origin, status: shape.status, nodes }];
    if (otherChildFinished) components.push({ filename: "Done1.yaml", type: "agent", name: "Done1", origin: "session", status: "validated" });
    components.push({ filename: "Child.yaml", type: "agent", name: "Child", origin: "session", status: CHILD_STATES[child].status });
    const manifest: BuildManifest = { name: "W", full_validation_passed: true, components };

    const withYaml = new Set(["W.yaml", ...(otherChildFinished ? ["Done1.yaml"] : []), ...(CHILD_STATES[child].yaml ? ["Child.yaml"] : [])]);
    const generatingFilenames = deriveGeneratingFilenames([], manifest, isResponding, true);
    const ctx: BuildContext = {
        resolvedConfigs: new Map([...withYaml].map(filename => [filename, "name: written"])),
        loadingSet: new Set(),
        generatingFilenames,
        isResponding,
        buildFinalizing: false,
        hasAnyGenerating: generatingFilenames.size > 0,
        changedSinceValidation: new Set(),
        pendingInputComponentName,
        createdNames: new Set(),
        a2aFallbackByName: new Map(),
    };
    const componentData = new Map(components.map(c => [c.name, buildCandidateEntry(c, ctx, undefined)]));
    return { manifest, componentData };
}

type Session = ReturnType<typeof buildSession>;

function renderedStartCard(session: Session, shape: WorkflowShape) {
    const { nodes } = computeCanvasLayout(session.manifest, session.componentData);
    const startData = nodes.find(n => n.id === "wf:W:__start__")!.data;
    const interrupted = isBuildInterruptedOnCanvas(false, session.componentData.values());
    const { container, unmount } = render(
        <CanvasViewProvider builderActiveTab="build" testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={false} buildInterrupted={interrupted} liveAgentCards={shape.liveOnMesh ? [{ name: LIVE_WORKFLOW_CARD }] : undefined}>
            <ReactFlowProvider>
                <WfStartFlowNode {...(makeNodeProps(startData) as React.ComponentProps<typeof WfStartFlowNode>)} />
            </ReactFlowProvider>
        </CanvasViewProvider>
    );
    const pills = (["Paused", "Active", "Ready", "Creating", "Editing", "Draft"] as const).filter(text => within(container).queryByText(text) !== null);
    expect(pills.length).toBe(1);
    const pill = pills[0];
    const spins = container.querySelector(".animate-spin") !== null;
    unmount();
    return { pill, spins, interrupted };
}

function planCardOffersResume(manifest: BuildManifest, planApproved: boolean, turnEndedAbnormally: boolean): boolean {
    return isApprovedBuildInterrupted({ planApproved, isApplyingChanges: false, isResponding: false, isCancelling: false, turnEndedAbnormally, hasDeployFailure: false, isBuildActivating: false, manifest });
}

function timelineOffersResumeAfterAutoCollapse(session: Session): boolean {
    vi.useFakeTimers();
    const { unmount } = render(
        <TooltipProvider>
            <InlineCreationProgress manifest={session.manifest} componentData={session.componentData} isResponding={false} isInterrupted={false} onResume={() => {}} />
        </TooltipProvider>
    );
    act(() => {
        vi.advanceTimersByTime(2000);
    });
    const offered = screen.queryByRole("button", { name: /Resume build/i }) !== null;
    unmount();
    return offered;
}

function timelineOffersResumeUnprompted(session: Session, isResponding: boolean): boolean {
    const { unmount } = render(
        <TooltipProvider>
            <InlineCreationProgress manifest={session.manifest} componentData={session.componentData} isResponding={isResponding} isInterrupted={false} onResume={() => {}} />
        </TooltipProvider>
    );
    const offered = screen.queryByRole("button", { name: /Resume build/i }) !== null;
    unmount();
    return offered;
}

const PLAN_CARD_ON_STOP: Record<ChildState, boolean> = {
    draftNoYaml: true,
    draftYaml: true,
    noStatusYaml: true,
    errorYaml: false,
    errorNoYaml: false,
};

function expectedIdlePill(shape: WorkflowShape, otherChildFinished: boolean): string {
    return shape.finishedWork || otherChildFinished ? "Paused" : shape.quietPill;
}

afterEach(() => {
    vi.useRealTimers();
});

const CASES = SHAPES.flatMap(shape => [true, false].flatMap(otherChildFinished => (Object.keys(CHILD_STATES) as ChildState[]).map(child => ({ shape, otherChildFinished, child, expectedPill: expectedIdlePill(shape, otherChildFinished) }))));

test("the table covers both Paused and quiet start cards", () => {
    expect(CASES.length).toBe(60);
    expect(CASES.filter(c => c.expectedPill === "Paused").length).toBe(40);
    expect(new Set(CASES.filter(c => c.expectedPill !== "Paused").map(c => c.expectedPill))).toEqual(new Set(["Active", "Draft"]));
});

describe.each(CASES)("$shape.label, another child finished: $otherChildFinished, unfinished child: $child", ({ shape, otherChildFinished, child, expectedPill }) => {
    const idle = { shape, child, otherChildFinished, isResponding: false };

    test("the idle start card reads the pill the canvas evidence supports, and never spins", () => {
        const card = renderedStartCard(buildSession(idle), shape);
        expect(card.pill).toBe(expectedPill);
        expect(card.spins).toBe(false);
    });

    test("when the card reads Paused, a Resume is reachable after a normal turn end, still on screen after the timeline's auto-collapse window", () => {
        const session = buildSession(idle);
        expect(renderedStartCard(session, shape).pill === "Paused").toBe(expectedPill === "Paused");
        if (expectedPill === "Paused") {
            expect(timelineOffersResumeUnprompted(session, false)).toBe(true);
            expect(timelineOffersResumeAfterAutoCollapse(session)).toBe(true);
        }
    });

    test("the plan card offers Resume only on explicit evidence", () => {
        const { manifest } = buildSession(idle);
        expect(planCardOffersResume(manifest, true, false)).toBe(false);
        expect(planCardOffersResume(manifest, true, true)).toBe(PLAN_CARD_ON_STOP[child] || (shape.origin === "session" && shape.status === "draft"));
    });

    test("a live turn spins the start card and shows no Resume", () => {
        const session = buildSession({ ...idle, isResponding: true });
        const card = renderedStartCard(session, shape);
        expect(card.interrupted).toBe(false);
        expect(card.pill).not.toBe("Paused");
        expect(card.spins).toBe(true);
        expect(timelineOffersResumeUnprompted(session, true)).toBe(false);
    });

    test("a live turn waiting on a connector prompt still spins the start card and never reads Paused", () => {
        const card = renderedStartCard(buildSession({ ...idle, isResponding: true, pendingInputComponentName: "Child" }), shape);
        expect(card.pill).not.toBe("Paused");
        expect(card.spins).toBe(true);
    });

    test("a restored session with an open connector prompt and an idle builder never reads Paused and offers no Resume above the prompt", () => {
        const session = buildSession({ ...idle, pendingInputComponentName: "Child" });
        expect(renderedStartCard(session, shape).pill).not.toBe("Paused");
        expect(timelineOffersResumeUnprompted(session, false)).toBe(false);
    });
});

describe("states with nothing of this build finished", () => {
    test("a platform workflow whose only child failed stays Active and offers no Resume anywhere, because nothing was built and the child card shows the Error", () => {
        const shape = SHAPES.find(sh => sh.label === "platform workflow deployed")!;
        const session = buildSession({ shape, child: "errorYaml", otherChildFinished: false, isResponding: false });
        expect(renderedStartCard(session, shape).pill).toBe("Active");
        expect(planCardOffersResume(session.manifest, true, true)).toBe(false);
        expect(timelineOffersResumeUnprompted(session, false)).toBe(false);
    });

    test("a live workflow gaining a newly planned, unbuilt child before approval reads Active and offers no Resume anywhere", () => {
        const shape = SHAPES.find(sh => sh.label === "platform workflow deployed")!;
        const session = buildSession({ shape, child: "draftNoYaml", otherChildFinished: false, isResponding: false });
        expect(renderedStartCard(session, shape).pill).toBe("Active");
        expect(renderedStartCard(session, shape).spins).toBe(false);
        expect(planCardOffersResume(session.manifest, false, true)).toBe(false);
        expect(timelineOffersResumeUnprompted(session, false)).toBe(false);
    });
});
