// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { areWorkflowChildAgentsDone, isBuildInterruptedOnCanvas, isBuildStopped, isComponentDone, isWorkflowChildAgentDone } from "./workflowChildStatus";

function cd(partial: Partial<ComponentCanvasData>): ComponentCanvasData {
    return partial as ComponentCanvasData;
}

function manifestWith(components: NonNullable<BuildManifest["components"]>): BuildManifest {
    return { components } as BuildManifest;
}

describe("isWorkflowChildAgentDone", () => {
    test("an agent absent from the manifest is done — this build does not own it", () => {
        expect(isWorkflowChildAgentDone("Orchestrator", new Map(), manifestWith([{ filename: "wf.yaml", type: "workflow", name: "VehiclePedestrianDetection" }]))).toBe(true);
    });

    test("a platform-origin component is done regardless of status", () => {
        const componentData = new Map([["Orchestrator", cd({ name: "Orchestrator", origin: "platform", status: "draft" })]]);
        expect(isWorkflowChildAgentDone("Orchestrator", componentData, undefined)).toBe(true);
    });

    test("a platform-origin manifest component with no canvas data is done", () => {
        const m = manifestWith([{ filename: "orch.yaml", type: "agent", name: "Orchestrator", origin: "platform", status: "draft" }]);
        expect(isWorkflowChildAgentDone("Orchestrator", new Map(), m)).toBe(true);
    });

    test.each(["validated", "deployed"])("a session agent that is %s is done", status => {
        const componentData = new Map([["Summarizer", cd({ name: "Summarizer", status })]]);
        expect(isWorkflowChildAgentDone("Summarizer", componentData, undefined)).toBe(true);
    });

    test("a session agent still being generated is NOT done", () => {
        const componentData = new Map([["Summarizer", cd({ name: "Summarizer", status: "draft" })]]);
        expect(isWorkflowChildAgentDone("Summarizer", componentData, undefined)).toBe(false);
    });

    test("a manifest component still draft, with no canvas data yet, is NOT done", () => {
        const m = manifestWith([{ filename: "sum.yaml", type: "agent", name: "Summarizer", status: "draft" }]);
        expect(isWorkflowChildAgentDone("Summarizer", new Map(), m)).toBe(false);
    });
});

describe("isComponentDone", () => {
    test.each([
        ["validated", true],
        ["deployed", true],
        ["draft", false],
        ["error", false],
        [undefined, false],
    ])("a session component with status %s is done: %s", (status, done) => {
        expect(isComponentDone({ origin: "session", status })).toBe(done);
    });

    test.each(["draft", "error", undefined])("a platform-origin component with status %s is done, this build never produces it", status => {
        expect(isComponentDone({ origin: "platform", status })).toBe(true);
    });

    test("a component with no origin is a session component", () => {
        expect(isComponentDone({ status: "error" })).toBe(false);
    });
});

describe("isBuildStopped", () => {
    type Entry = Parameters<typeof isBuildStopped>[0] extends Iterable<infer E> ? E : never;
    const IDLE = { isBuilderResponding: false, isGenerating: false, isQueued: false, isWaitingForInput: false };
    const entry = (over: Partial<Entry>): Entry => ({ origin: "session", status: "draft", ...IDLE, ...over });
    const finished = entry({ status: "validated" });
    const unfinished = entry({ status: "draft" });

    test("a finished and an unfinished session component with nothing working is a stopped build", () => {
        expect(isBuildStopped([finished, unfinished])).toBe(true);
    });

    test.each(["draft", "error", undefined])("an unfinished component with status %s counts", status => {
        expect(isBuildStopped([finished, entry({ status })])).toBe(true);
    });

    test("nothing finished yet is not a stopped build", () => {
        expect(isBuildStopped([unfinished, entry({ status: "error" })])).toBe(false);
    });

    test("nothing unfinished is not a stopped build", () => {
        expect(isBuildStopped([finished, entry({ status: "deployed" })])).toBe(false);
    });

    test("an empty build is not a stopped build", () => {
        expect(isBuildStopped([])).toBe(false);
    });

    test.each([
        ["the builder responding", { isBuilderResponding: true }],
        ["a generating component", { isGenerating: true }],
        ["a queued component", { isQueued: true }],
        ["a component waiting for input", { isWaitingForInput: true }],
    ])("%s anywhere in the build means it has not stopped, whichever component carries it", (_label, working) => {
        expect(isBuildStopped([finished, entry(working), unfinished])).toBe(false);
        expect(isBuildStopped([finished, unfinished, entry({ status: "validated", ...working })])).toBe(false);
    });

    test("platform-origin components are neither finished work nor unfinished work, and are never working", () => {
        const livePlatform = entry({ origin: "platform", status: "deployed" });
        expect(isBuildStopped([livePlatform, unfinished])).toBe(false);
        expect(isBuildStopped([entry({ origin: "platform", status: "draft" }), finished])).toBe(false);
        expect(isBuildStopped([finished, unfinished, entry({ origin: "platform", isQueued: true, isGenerating: true, isBuilderResponding: true })])).toBe(true);
    });

    test("an open prompt tied to a platform-origin component still means the build has not stopped", () => {
        expect(isBuildStopped([finished, unfinished, entry({ origin: "platform", isWaitingForInput: true })])).toBe(false);
    });
});

describe("isBuildInterruptedOnCanvas", () => {
    type Entry = Parameters<typeof isBuildStopped>[0] extends Iterable<infer E> ? E : never;
    const IDLE = { isBuilderResponding: false, isGenerating: false, isQueued: false, isWaitingForInput: false };
    const entry = (over: Partial<Entry>): Entry => ({ origin: "session", status: "draft", ...IDLE, ...over });

    const SHAPES: ReadonlyArray<{ name: string; entries: Entry[]; componentVerdict: boolean }> = [
        { name: "no components yet", entries: [], componentVerdict: false },
        { name: "every component still a draft (a plan, or a build stopped before the first validation)", entries: [entry({}), entry({})], componentVerdict: false },
        { name: "one finished and one unfinished component", entries: [entry({ status: "validated" }), entry({})], componentVerdict: true },
        { name: "every component finished", entries: [entry({ status: "validated" }), entry({ status: "deployed" })], componentVerdict: false },
        { name: "a finished and an unfinished component while the builder responds", entries: [entry({ status: "validated", isBuilderResponding: true }), entry({ isBuilderResponding: true })], componentVerdict: false },
    ];

    test.each(SHAPES)("$name: host verdict false reads the component state alone", ({ entries, componentVerdict }) => {
        expect(isBuildInterruptedOnCanvas(false, entries)).toBe(componentVerdict);
    });

    test.each(SHAPES)("$name: host verdict true is interrupted whatever the component state says", ({ entries }) => {
        expect(isBuildInterruptedOnCanvas(true, entries)).toBe(true);
    });
});

describe("isWorkflowChildAgentDone from the manifest alone", () => {
    test.each([
        ["validated", true],
        ["deployed", true],
        ["draft", false],
        ["error", false],
        [undefined, false],
    ])("a manifest component with status %s and no canvas data is done: %s", (status, done) => {
        const m = manifestWith([{ filename: "sum.yaml", type: "agent", name: "Summarizer", status }]);
        expect(isWorkflowChildAgentDone("Summarizer", new Map(), m)).toBe(done);
    });

    test.each(["draft", "error", undefined])("a canvas component with status %s is not done, whatever the manifest says", status => {
        const componentData = new Map([["Summarizer", cd({ name: "Summarizer", status })]]);
        const m = manifestWith([{ filename: "sum.yaml", type: "agent", name: "Summarizer", status: "validated" }]);
        expect(isWorkflowChildAgentDone("Summarizer", componentData, m)).toBe(false);
    });
});

describe("areWorkflowChildAgentsDone", () => {
    test("an empty set is vacuously done (pure tool DAG has no child gate)", () => {
        expect(areWorkflowChildAgentsDone([], new Map(), undefined)).toBe(true);
    });

    test("one unfinished owned agent blocks the whole set", () => {
        const componentData = new Map([
            ["Summarizer", cd({ name: "Summarizer", status: "deployed" })],
            ["Classifier", cd({ name: "Classifier", status: "draft" })],
        ]);
        expect(areWorkflowChildAgentsDone(["Summarizer", "Classifier"], componentData, undefined)).toBe(false);
    });

    test("a mix of a deployed session agent and a platform agent is done", () => {
        const componentData = new Map([["Summarizer", cd({ name: "Summarizer", status: "deployed" })]]);
        expect(areWorkflowChildAgentsDone(["Summarizer", "Orchestrator"], componentData, manifestWith([]))).toBe(true);
    });
});
