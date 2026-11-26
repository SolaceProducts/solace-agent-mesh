// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";
import { isBuildInterruptedOnCanvas } from "../workflowChildStatus";

import { deriveGroupStatus, groupComponents, type GroupStatus, pausedWhenInterrupted } from "./canvasGroups";

describe("groupComponents — a2a_agent standalone pass", () => {
    test("emits a standalone group for a remote A2A agent", () => {
        const manifest = {
            components: [{ name: "WeatherProxy", filename: "weather_proxy.json", type: "a2a_agent", origin: "session" }],
        } as unknown as BuildManifest;

        const groups = groupComponents(manifest);
        expect(groups).toHaveLength(1);
        expect(groups[0]).toMatchObject({ id: "a2a:WeatherProxy", type: "a2a_agent", anchor: "WeatherProxy" });
        expect([...groups[0].members]).toEqual(["WeatherProxy"]);
    });

    test("does not pull a remote A2A agent into an unrelated agent group", () => {
        const manifest = {
            components: [
                { name: "ChatAgent", filename: "chat.yaml", type: "agent", origin: "session" },
                { name: "WeatherProxy", filename: "weather_proxy.json", type: "a2a_agent", origin: "session" },
            ],
        } as unknown as BuildManifest;

        const groups = groupComponents(manifest);
        const a2a = groups.find(g => g.type === "a2a_agent");
        const agent = groups.find(g => g.type === "agent");
        expect(a2a?.anchor).toBe("WeatherProxy");
        expect(agent?.members.has("WeatherProxy")).toBe(false);
    });

    test("skips platform-origin remote agents (rendered within referencing groups, not as tabs)", () => {
        const manifest = {
            components: [{ name: "ExistingProxy", filename: "", type: "a2a_agent", origin: "platform" }],
        } as unknown as BuildManifest;

        expect(groupComponents(manifest)).toHaveLength(0);
    });
});

describe("groupComponents — standalone skills", () => {
    test("emits a standalone group for a skill-only build (no host agent)", () => {
        const manifest = {
            components: [{ name: "WeatherLookup", filename: "weather_skill.yaml", type: "skill", origin: "session" }],
        } as unknown as BuildManifest;

        const groups = groupComponents(manifest);
        expect(groups).toHaveLength(1);
        expect(groups[0]).toMatchObject({ id: "sk:WeatherLookup", type: "skill", anchor: "WeatherLookup" });
        expect([...groups[0].members]).toEqual(["WeatherLookup"]);
    });

    test("a skill referenced by an agent is claimed into the agent group, not a standalone tab", () => {
        const manifest = {
            components: [
                { name: "ChatAgent", filename: "chat.yaml", type: "agent", origin: "session", depends_on: ["WeatherLookup"] },
                { name: "WeatherLookup", filename: "weather_skill.yaml", type: "skill", origin: "session" },
            ],
        } as unknown as BuildManifest;

        const groups = groupComponents(manifest);
        expect(groups).toHaveLength(1);
        const agent = groups.find(g => g.type === "agent");
        expect(agent?.members.has("WeatherLookup")).toBe(true);
        expect(groups.some(g => g.type === "skill")).toBe(false);
    });

    test("skips platform-origin skills (shipped within referencing agents, not as tabs)", () => {
        const manifest = {
            components: [{ name: "ExistingSkill", filename: "", type: "skill", origin: "platform" }],
        } as unknown as BuildManifest;

        expect(groupComponents(manifest)).toHaveLength(0);
    });
});

describe("deriveGroupStatus — finalizing gate", () => {
    const cd = (over: Partial<ComponentCanvasData> = {}): ComponentCanvasData => ({
        name: "A",
        filename: "a.yaml",
        type: "agent",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...over,
    });

    const manifest = { components: [{ name: "A", filename: "a.yaml", type: "agent", status: "validated", origin: "session" }] } as unknown as BuildManifest;
    const group = groupComponents(manifest)[0];

    test("a finalizing validated member reads 'creating', not 'ready' — the tab dot must not flash done mid-build", () => {
        const data = new Map<string, ComponentCanvasData>([["A", cd({ status: "validated", isBuildFinalizing: true })]]);
        expect(deriveGroupStatus(group, manifest, data)).toBe("creating");
    });

    test("a blocked member reads 'waiting' on its tab, never 'ready' beside validated members", () => {
        const blockedManifest = {
            components: [
                { name: "A", filename: "a.yaml", type: "agent", status: "validated", origin: "session" },
                { name: "C", filename: "c.json", type: "connector", status: "blocked", origin: "session", blocked_reason: "the spec URL" },
            ],
        } as unknown as BuildManifest;
        const blockedGroup = { members: new Set(["A", "C"]) } as unknown as ReturnType<typeof groupComponents>[number];
        const data = new Map<string, ComponentCanvasData>([
            ["A", cd({ status: "validated" })],
            ["C", cd({ name: "C", filename: "c.json", type: "connector", status: "blocked", isWaitingForInput: true })],
        ]);
        expect(deriveGroupStatus(blockedGroup, blockedManifest, data)).toBe("waiting");
    });

    test("a settled validated member (not finalizing) reads 'ready'", () => {
        const data = new Map<string, ComponentCanvasData>([["A", cd({ status: "validated", isBuildFinalizing: false })]]);
        expect(deriveGroupStatus(group, manifest, data)).toBe("ready");
    });

    const draftManifest = { components: [{ name: "A", filename: "a.yaml", type: "agent", status: "draft", origin: "session" }] } as unknown as BuildManifest;
    const draftGroup = groupComponents(draftManifest)[0];

    test("a finalizing DRAFT member reads 'creating' (not 'draft') — the backward-flip fix", () => {
        const data = new Map<string, ComponentCanvasData>([["A", cd({ status: "draft", isBuildFinalizing: true })]]);
        expect(deriveGroupStatus(draftGroup, draftManifest, data)).toBe("creating");
    });

    test("a settled DRAFT member (not finalizing, not queued) reads 'draft'", () => {
        const data = new Map<string, ComponentCanvasData>([["A", cd({ status: "draft", isBuildFinalizing: false })]]);
        expect(deriveGroupStatus(draftGroup, draftManifest, data)).toBe("draft");
    });
});

type ShapeComponent = {
    name: string;
    type?: "agent" | "workflow" | "connector";
    status?: string;
    origin?: "session" | "platform";
    dependsOn?: string[];
    flags?: Partial<ComponentCanvasData>;
};

type TabShape = {
    name: string;
    components: ShapeComponent[];
    withoutComponentData?: boolean;
    hostInterrupted?: boolean;
    expectedStatusByAnchor: Record<string, GroupStatus>;
};

const RESPONDING: Partial<ComponentCanvasData> = { isBuilderResponding: true };

const TAB_SHAPES: TabShape[] = [
    {
        name: "a plan awaiting approval (every component draft, builder idle)",
        components: [
            { name: "map_router", type: "workflow", status: "draft" },
            { name: "loop_router", type: "workflow", status: "draft" },
            { name: "worker_agent", status: "draft" },
        ],
        expectedStatusByAnchor: { map_router: "draft", loop_router: "draft", worker_agent: "draft" },
    },
    {
        name: "a plan awaiting approval with no status stamped on the components",
        components: [{ name: "map_router", type: "workflow" }, { name: "worker_agent" }],
        expectedStatusByAnchor: { map_router: "draft", worker_agent: "draft" },
    },
    {
        name: "the planning turn still responding, nothing generating yet",
        components: [
            { name: "a", status: "draft", flags: RESPONDING },
            { name: "b", status: "draft", flags: RESPONDING },
        ],
        expectedStatusByAnchor: { a: "draft", b: "draft" },
    },
    {
        name: "one group generating, the others queued",
        components: [
            { name: "a", status: "draft", flags: { isGenerating: true } },
            { name: "b", status: "draft", flags: { isQueued: true } },
        ],
        expectedStatusByAnchor: { a: "creating", b: "queued" },
    },
    {
        name: "one group generating, one finished, one not started",
        components: [
            { name: "a", status: "draft", flags: { isGenerating: true, isBuilderResponding: true } },
            { name: "b", status: "validated" },
            { name: "c", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "creating", b: "ready", c: "draft" },
    },
    {
        name: "stopped mid-build: one group finished, the next never started",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "draft" },
            { name: "c", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "ready", b: "paused", c: "paused" },
    },
    {
        name: "stopped mid-build: a workflow group half finished",
        components: [
            { name: "wf", type: "workflow", status: "draft", dependsOn: ["ag"] },
            { name: "ag", status: "validated" },
            { name: "other", status: "draft" },
        ],
        expectedStatusByAnchor: { wf: "paused", other: "paused" },
    },
    {
        name: "stopped mid-build with a deployed group beside an unfinished one",
        components: [
            { name: "a", status: "deployed" },
            { name: "b", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "ready", b: "paused" },
    },
    {
        name: "a validated group while the builder is still responding reads ready and the other draft, not paused",
        components: [
            { name: "a", status: "validated", flags: RESPONDING },
            { name: "b", status: "draft", flags: RESPONDING },
        ],
        expectedStatusByAnchor: { a: "ready", b: "draft" },
    },
    {
        name: "a failed group beside a finished one",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "error" },
        ],
        expectedStatusByAnchor: { a: "ready", b: "error" },
    },
    {
        name: "a failed group beside a group that never started and nothing finished",
        components: [
            { name: "a", status: "error" },
            { name: "b", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "error", b: "draft" },
    },
    {
        name: "stopped mid-build with a failure, a finished group and an unstarted one",
        components: [
            { name: "a", status: "error" },
            { name: "b", status: "validated" },
            { name: "c", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "error", b: "ready", c: "paused" },
    },
    {
        name: "a plan awaiting approval reads Draft: the host has no interruption verdict for it",
        components: [
            { name: "a", status: "draft" },
            { name: "b", status: "draft" },
            { name: "c", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "draft", b: "draft", c: "draft" },
    },
    {
        name: "stopped before any component validated: the host verdict pauses every unfinished tab",
        components: [
            { name: "a", status: "draft" },
            { name: "b", status: "draft" },
            { name: "c", status: "draft" },
        ],
        hostInterrupted: true,
        expectedStatusByAnchor: { a: "paused", b: "paused", c: "paused" },
    },
    {
        name: "stopped before any component validated, no status stamped on the components",
        components: [{ name: "a", type: "workflow" }, { name: "b" }],
        hostInterrupted: true,
        expectedStatusByAnchor: { a: "paused", b: "paused" },
    },
    {
        name: "stopped before the first validation with a failure beside a draft: the failure stays an error",
        components: [
            { name: "a", status: "error" },
            { name: "b", status: "draft" },
        ],
        hostInterrupted: true,
        expectedStatusByAnchor: { a: "error", b: "paused" },
    },
    {
        name: "the host verdict leaves a finished group ready and pauses the unfinished one",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "draft" },
        ],
        hostInterrupted: true,
        expectedStatusByAnchor: { a: "ready", b: "paused" },
    },
    {
        name: "the host verdict with no component data yet pauses the unfinished tab and leaves a finished one ready",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "draft" },
        ],
        withoutComponentData: true,
        hostInterrupted: true,
        expectedStatusByAnchor: { a: "ready", b: "paused" },
    },
    {
        name: "a connector prompt open beside a host verdict still reads waiting on its own tab",
        components: [
            { name: "a", status: "draft", flags: { isWaitingForInput: true } },
            { name: "b", status: "draft" },
        ],
        hostInterrupted: true,
        expectedStatusByAnchor: { a: "waiting", b: "paused" },
    },
    {
        name: "all components finished: a stale host verdict pauses nothing",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "deployed" },
        ],
        hostInterrupted: true,
        expectedStatusByAnchor: { a: "ready", b: "ready" },
    },
    {
        name: "manifest statuses with no component data yet: no stop verdict, so the unfinished group reads Draft",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "draft" },
        ],
        withoutComponentData: true,
        expectedStatusByAnchor: { a: "ready", b: "draft" },
    },
    {
        name: "every component validated",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "validated" },
        ],
        expectedStatusByAnchor: { a: "ready", b: "ready" },
    },
    {
        name: "a platform component deployed beside a session group that never started is no finished work",
        components: [
            { name: "live", status: "deployed", origin: "platform" },
            { name: "a", status: "draft" },
            { name: "b", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "draft", b: "draft" },
    },
    {
        name: "a platform connector deployed under a finished group beside an unstarted one",
        components: [
            { name: "live", type: "connector", status: "deployed", origin: "platform" },
            { name: "a", status: "validated", dependsOn: ["live"] },
            { name: "b", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "ready", b: "paused" },
    },
    {
        name: "a connector prompt open on one group stops the others reading paused",
        components: [
            { name: "a", status: "validated" },
            { name: "b", status: "draft", flags: { isWaitingForInput: true } },
            { name: "c", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "ready", b: "waiting", c: "draft" },
    },
    {
        name: "a validated member still finalizing while the builder responds, under a draft group",
        components: [
            { name: "a", status: "validated", flags: { isBuildFinalizing: true, isBuilderResponding: true } },
            { name: "b", status: "draft" },
        ],
        expectedStatusByAnchor: { a: "creating", b: "draft" },
    },
];

function shapeInputs(shape: TabShape) {
    const manifest = {
        components: shape.components.map(c => ({
            name: c.name,
            filename: `${c.name}.yaml`,
            type: c.type ?? "agent",
            origin: c.origin ?? "session",
            ...(c.status ? { status: c.status } : {}),
            ...(c.dependsOn ? { depends_on: c.dependsOn } : {}),
        })),
    } as unknown as BuildManifest;
    const data = new Map<string, ComponentCanvasData>(
        (shape.withoutComponentData ? [] : shape.components).map(c => [
            c.name,
            {
                name: c.name,
                filename: `${c.name}.yaml`,
                type: c.type ?? "agent",
                origin: c.origin ?? "session",
                status: c.status,
                yamlText: null,
                isLoading: false,
                isGenerating: false,
                isQueued: false,
                isWaitingForInput: false,
                isBuildingDeps: false,
                isBuilderResponding: false,
                dependencyProgress: [],
                ...c.flags,
            } as ComponentCanvasData,
        ])
    );
    return { manifest, data };
}

describe("the tab dot reads the same interruption verdict as the cards", () => {
    test.each(TAB_SHAPES)("$name", shape => {
        const { manifest, data } = shapeInputs(shape);
        const groups = groupComponents(manifest, data);
        const interrupted = isBuildInterruptedOnCanvas(shape.hostInterrupted ?? false, data.values());

        const actual = Object.fromEntries(groups.map(g => [g.anchor, pausedWhenInterrupted(deriveGroupStatus(g, manifest, data), interrupted)]));

        expect(actual).toEqual(shape.expectedStatusByAnchor);
    });
});

describe("pausedWhenInterrupted", () => {
    const STATUSES: GroupStatus[] = ["ready", "creating", "queued", "paused", "draft", "waiting", "error"];

    test.each(STATUSES)("an interrupted build turns only draft into paused: %s", status => {
        expect(pausedWhenInterrupted(status, true)).toBe(status === "draft" ? "paused" : status);
    });

    test.each(STATUSES)("a build that is not interrupted changes nothing: %s", status => {
        expect(pausedWhenInterrupted(status, false)).toBe(status);
    });
});
