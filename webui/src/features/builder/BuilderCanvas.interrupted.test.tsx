// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { BuilderCanvas } from "./BuilderCanvas";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { isBuildInterruptedOnCanvas } from "./workflowChildStatus";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}

beforeEach(() => {
    vi.stubGlobal("ResizeObserver", ResizeObserverStub);
});

type Comp = { name: string; type: "agent" | "connector" | "gateway" | "skill"; status?: string };

function manifestOf(comps: Comp[]): BuildManifest {
    return { name: "build", components: comps.map(c => ({ filename: `${c.name}.yaml`, type: c.type, name: c.name, origin: "session", ...(c.status ? { status: c.status } : {}) })) };
}

function dataOf(comps: Comp[], responding: boolean): Map<string, ComponentCanvasData> {
    return new Map(
        comps.map(c => [
            c.name,
            {
                name: c.name,
                filename: `${c.name}.yaml`,
                type: c.type,
                status: c.status,
                origin: "session",
                yamlText: null,
                isLoading: false,
                isGenerating: false,
                isQueued: false,
                isWaitingForInput: false,
                isBuildingDeps: false,
                isBuilderResponding: responding,
                dependencyProgress: [],
            } as ComponentCanvasData,
        ])
    );
}

function renderCanvas(comps: Comp[], { host, responding = false }: { host: boolean; responding?: boolean }) {
    const componentData = dataOf(comps, responding);
    const props: Parameters<typeof BuilderCanvas>[0] = {
        manifest: manifestOf(comps),
        componentData,
        selectedNodeId: null,
        onNodeSelect: () => {},
        onNodeDoubleClick: () => {},
        isEmpty: false,
        isLoading: false,
        buildInterrupted: isBuildInterruptedOnCanvas(host, componentData.values()),
    };
    return render(
        <StoryProvider chatContextValues={{ sessionId: "s1" }}>
            <div style={{ width: 900, height: 700 }}>
                <BuilderCanvas {...props} />
            </div>
        </StoryProvider>
    );
}

const statusOfTab = (tab: HTMLElement) => /, (\w[\w ]*)$/.exec(tab.getAttribute("aria-label") ?? "")?.[1];

interface Shape {
    name: string;
    comps: Comp[];
    host: boolean;
    responding?: boolean;
    expected: Record<string, { tab: string; pill: string }>;
}

const AGENTS: Comp[] = [
    { name: "AlphaAgent", type: "agent", status: "draft" },
    { name: "BetaAgent", type: "agent", status: "draft" },
];
const EVERY_KIND: Comp[] = [
    { name: "AgentOne", type: "agent", status: "draft" },
    { name: "ConnOne", type: "connector", status: "draft" },
    { name: "GwOne", type: "gateway", status: "draft" },
    { name: "SkillOne", type: "skill", status: "draft" },
];

const SHAPES: Shape[] = [
    {
        name: "stopped before anything validated, host verdict: every tab and card is Paused",
        comps: AGENTS,
        host: true,
        expected: { AlphaAgent: { tab: "Paused", pill: "Paused" }, BetaAgent: { tab: "Paused", pill: "Paused" } },
    },
    {
        name: "a plan awaiting approval: Draft on every tab and card",
        comps: AGENTS,
        host: false,
        expected: { AlphaAgent: { tab: "Draft", pill: "Draft" }, BetaAgent: { tab: "Draft", pill: "Draft" } },
    },
    {
        name: "the builder responding with no host verdict: Draft, no flicker to Paused",
        comps: AGENTS,
        host: false,
        responding: true,
        expected: { AlphaAgent: { tab: "Draft", pill: "Draft" }, BetaAgent: { tab: "Draft", pill: "Draft" } },
    },
    {
        name: "stopped before anything validated with every standalone kind",
        comps: EVERY_KIND,
        host: true,
        expected: {
            AgentOne: { tab: "Paused", pill: "Paused" },
            ConnOne: { tab: "Paused", pill: "Paused" },
            GwOne: { tab: "Paused", pill: "Paused" },
            SkillOne: { tab: "Paused", pill: "Paused" },
        },
    },
    {
        name: "stopped mid-build (component state alone): a finished tab stays Ready, the unfinished ones are Paused",
        comps: [
            { name: "DoneAgent", type: "agent", status: "validated" },
            { name: "TodoAgent", type: "agent", status: "draft" },
            { name: "TodoConn", type: "connector", status: "draft" },
        ],
        host: false,
        expected: { DoneAgent: { tab: "Ready", pill: "Ready" }, TodoAgent: { tab: "Paused", pill: "Paused" }, TodoConn: { tab: "Paused", pill: "Paused" } },
    },
    {
        name: "an edit session, no Stop: an existing deployed component beside a newly added draft reads like any stopped build on the component tier",
        comps: [
            { name: "LiveAgent", type: "agent", status: "deployed" },
            { name: "NewAgent", type: "agent", status: "draft" },
        ],
        host: false,
        expected: { LiveAgent: { tab: "Ready", pill: "Active" }, NewAgent: { tab: "Paused", pill: "Paused" } },
    },
    {
        name: "stopped mid-build with a failure: the failed tab stays Error, the unfinished one is Paused",
        comps: [
            { name: "BrokenAgent", type: "agent", status: "error" },
            { name: "DoneAgent", type: "agent", status: "validated" },
            { name: "TodoAgent", type: "agent", status: "draft" },
        ],
        host: false,
        expected: { BrokenAgent: { tab: "Error", pill: "Error" }, DoneAgent: { tab: "Ready", pill: "Ready" }, TodoAgent: { tab: "Paused", pill: "Paused" } },
    },
    {
        name: "every component finished: nothing is Paused",
        comps: [
            { name: "DoneAgent", type: "agent", status: "validated" },
            { name: "DoneConn", type: "connector", status: "deployed" },
        ],
        host: false,
        expected: { DoneAgent: { tab: "Ready", pill: "Ready" }, DoneConn: { tab: "Ready", pill: "Active" } },
    },
];

describe("BuilderCanvas: tabs and cards read one interruption verdict", () => {
    test.each(SHAPES)("$name", async shape => {
        const user = userEvent.setup();
        renderCanvas(shape.comps, shape);

        const tabs = await screen.findAllByRole("tab", { name: /,/ });
        expect(tabs).toHaveLength(shape.comps.length);
        for (const comp of shape.comps) {
            const tab = tabs.find(t => t.getAttribute("aria-label")?.startsWith(`${comp.name},`));
            expect(tab, `a tab for ${comp.name}`).toBeDefined();
            expect(statusOfTab(tab!)).toBe(shape.expected[comp.name].tab);

            await user.click(tab!);
            const card = await waitFor(() => {
                const found = document.querySelector<HTMLElement>(`.react-flow__node[data-id="${comp.name}"]`);
                expect(found).not.toBeNull();
                return found!;
            });
            expect(within(card).getByText(shape.expected[comp.name].pill)).toBeInTheDocument();
        }
    });
});
