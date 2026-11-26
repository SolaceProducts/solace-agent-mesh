// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentType } from "react";
import { render, screen } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { describe, expect, test } from "vitest";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuilderActiveTab } from "../useBuilderPageState";

import { AgentCardNode } from "./AgentCardNode";
import { CanvasViewProvider } from "./canvasViewContext";
import { ConnectorCardNode } from "./ConnectorCardNode";
import { GatewayCardNode } from "./GatewayCardNode";
import { RemoteAgentCardNode } from "./RemoteAgentCardNode";
import { SkillCardNode } from "./SkillCardNode";
import { WorkflowCardNode } from "./WorkflowCardNode";

const DRAFT_ICON = ".lucide-file-text";

type Kind = "agent" | "connector" | "gateway" | "gateway (compact)" | "skill" | "a2a_agent" | "workflow";

const COMPONENT_TYPE: Record<Kind, string> = {
    agent: "agent",
    connector: "connector",
    gateway: "gateway",
    "gateway (compact)": "gateway",
    skill: "skill",
    a2a_agent: "a2a_agent",
    workflow: "workflow",
};

const CARDS: Record<Kind, ComponentType<never>> = {
    agent: AgentCardNode,
    connector: ConnectorCardNode,
    gateway: GatewayCardNode,
    "gateway (compact)": GatewayCardNode,
    skill: SkillCardNode,
    a2a_agent: RemoteAgentCardNode,
    workflow: WorkflowCardNode,
};

function canvasData(kind: Kind, over: Partial<ComponentCanvasData>): ComponentCanvasData {
    return {
        name: "Thing",
        filename: "thing.yaml",
        type: COMPONENT_TYPE[kind] as ComponentCanvasData["type"],
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

interface Shape {
    name: string;
    status: string | undefined;
    origin?: string;
    flags?: Partial<ComponentCanvasData>;
    interrupted: boolean;
    tab?: BuilderActiveTab;
    pill: string | null;
    absent?: string[];
    skip?: Kind[];
}

function pillFor(kind: Kind, pill: string): string {
    return kind === "skill" && (pill === "Ready" || pill === "Active") ? "Available" : pill;
}

const SHAPES: Shape[] = [
    { name: "an unfinished draft in an interrupted build", status: "draft", interrupted: true, pill: "Paused", absent: ["Draft"] },
    { name: "a component with no status stamped in an interrupted build", status: undefined, interrupted: true, pill: "Paused", absent: ["Draft"] },
    { name: "a queued component in an interrupted build reads Paused, not Creating", status: "draft", flags: { isQueued: true }, interrupted: true, pill: "Paused", absent: ["Draft", "Creating"] },
    { name: "a queued component in a live turn reads Creating", status: "draft", flags: { isQueued: true }, interrupted: false, pill: "Creating", absent: ["Paused", "Draft"] },
    { name: "the same draft when the build is not interrupted (a plan, or a live turn)", status: "draft", interrupted: false, pill: "Draft", absent: ["Paused"] },
    { name: "a validated component in an interrupted build", status: "validated", interrupted: true, pill: "Ready", absent: ["Paused"] },
    {
        name: "a validated component edited since the validation stamp reads Changed since validation, not Ready",
        status: "validated",
        flags: { changedSinceValidation: true },
        interrupted: false,
        pill: "Changed since validation",
        absent: ["Ready", "Available", "Draft"],
    },
    { name: "a deployed component edited since the validation stamp stays Active", status: "deployed", flags: { changedSinceValidation: true }, interrupted: false, pill: "Active", absent: ["Changed since validation"] },
    { name: "a deployed component in an interrupted build", status: "deployed", interrupted: true, pill: "Active", absent: ["Paused"] },
    { name: "a failed component in an interrupted build", status: "error", interrupted: true, pill: "Error", absent: ["Paused"] },
    { name: "a platform-origin draft in an interrupted build is live, not unfinished", status: "draft", origin: "platform", interrupted: true, pill: "Active", absent: ["Paused", "Draft"], skip: ["agent"] },
    { name: "a component generating right now", status: "draft", flags: { isGenerating: true }, interrupted: false, pill: "Creating", absent: ["Paused", "Draft"] },
    { name: "a component waiting on a connector prompt in an interrupted build", status: "draft", flags: { isWaitingForInput: true }, interrupted: true, pill: "Waiting for Input...", absent: ["Paused", "Draft"] },
    { name: "Test mode shows no status chrome even in an interrupted build", status: "draft", interrupted: true, tab: "test", pill: null, absent: ["Paused", "Draft"] },
];

function renderCard(kind: Kind, shape: Shape) {
    const component = { name: "Thing", filename: "thing.yaml", type: COMPONENT_TYPE[kind], status: shape.status, origin: shape.origin ?? "session" };
    const data = canvasData(kind, { status: shape.status, origin: shape.origin ?? "session", ...shape.flags });
    const Card = CARDS[kind] as ComponentType<Record<string, unknown>>;
    const props = makeNodeProps({ component, canvasData: data, compact: kind === "gateway (compact)" });
    return render(
        <CanvasViewProvider builderActiveTab={shape.tab ?? "build"} testTargetName={null} onSelectTestTarget={() => {}} isApplyingChanges={false} buildInterrupted={shape.interrupted}>
            <ReactFlowProvider>
                <Card {...(props as unknown as Record<string, unknown>)} />
            </ReactFlowProvider>
        </CanvasViewProvider>
    );
}

const KINDS = Object.keys(CARDS) as Kind[];
const CASES = KINDS.flatMap(kind => SHAPES.filter(shape => !shape.skip?.includes(kind)).map(shape => ({ kind, shape, label: `${kind}: ${shape.name}` })));

describe("standalone cards read the canvas-wide interruption verdict", () => {
    test("the table covers every card kind, and every shape on every kind but the documented skips", () => {
        expect(KINDS).toHaveLength(7);
        expect(CASES).toHaveLength(KINDS.length * SHAPES.length - SHAPES.reduce((n, s) => n + (s.skip?.length ?? 0), 0));
    });

    test.each(CASES)("$label", ({ kind, shape }) => {
        renderCard(kind, shape);

        if (shape.pill) expect(screen.getByText(pillFor(kind, shape.pill))).toBeInTheDocument();
        for (const text of shape.absent ?? []) expect(screen.queryByText(text)).toBeNull();
        if (shape.pill === "Paused") {
            expect(document.querySelector(".animate-spin")).toBeNull();
            expect(document.querySelector(DRAFT_ICON)).toBeNull();
        }
        if (shape.pill === "Draft") expect(document.querySelector(DRAFT_ICON)).not.toBeNull();
    });

    test.each(KINDS)("%s: outside the canvas (no provider, as in a preview) an unfinished card reads Draft, never Paused", kind => {
        const component = { name: "Thing", filename: "thing.yaml", type: COMPONENT_TYPE[kind], status: "draft", origin: "session" };
        const props = makeNodeProps({ component, canvasData: canvasData(kind, { status: "draft" }), compact: kind === "gateway (compact)" });
        const Card = CARDS[kind] as ComponentType<Record<string, unknown>>;

        render(
            <ReactFlowProvider>
                <Card {...(props as unknown as Record<string, unknown>)} />
            </ReactFlowProvider>
        );

        expect(screen.getByText("Draft")).toBeInTheDocument();
        expect(screen.queryByText("Paused")).toBeNull();
    });
});
