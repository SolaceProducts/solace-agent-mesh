// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { agentPropsEq } from "./AgentCardNode";

describe("agentPropsEq", () => {
    const component = {
        name: "ag",
        filename: "ag.yaml",
        type: "agent",
        depends_on: ["connA"],
        connectors: ["connB"],
    };
    const canvasData = { name: "ag", filename: "ag.yaml", type: "agent" };
    const manifest = { name: "m", components: [component] };
    const connA = { name: "connA", filename: "a.yaml" };
    const connB = { name: "connB", filename: "b.yaml" };

    test("returns true when every consumed field is reference-equal", () => {
        const componentData = new Map([
            ["connA", connA],
            ["connB", connB],
        ]);
        const a = makeNodeProps({ component, canvasData, manifest, componentData, isSingleAgent: false });
        const componentData2 = new Map([
            ["connA", connA],
            ["connB", connB],
        ]);
        const b = makeNodeProps({ component, canvasData, manifest, componentData: componentData2, isSingleAgent: false });
        expect(agentPropsEq(a, b)).toBe(true);
    });

    test("returns false when a connector entry's reference changes", () => {
        const componentData = new Map([
            ["connA", connA],
            ["connB", connB],
        ]);
        const a = makeNodeProps({ component, canvasData, manifest, componentData });
        const componentData2 = new Map([
            ["connA", { ...connA }],
            ["connB", connB],
        ]);
        const b = makeNodeProps({ component, canvasData, manifest, componentData: componentData2 });
        expect(agentPropsEq(a, b)).toBe(false);
    });

    test("returns false when canvasData reference changes", () => {
        const componentData = new Map([["connA", connA]]);
        const a = makeNodeProps({ component, canvasData, manifest, componentData });
        const b = makeNodeProps({ component, canvasData: { ...canvasData }, manifest, componentData });
        expect(agentPropsEq(a, b)).toBe(false);
    });

    test("returns false when manifest reference changes", () => {
        const componentData = new Map([["connA", connA]]);
        const a = makeNodeProps({ component, canvasData, manifest, componentData });
        const b = makeNodeProps({ component, canvasData, manifest: { ...manifest }, componentData });
        expect(agentPropsEq(a, b)).toBe(false);
    });

    test("handles components with no connector deps", () => {
        const bare = { name: "ag", filename: "ag.yaml", type: "agent" };
        const a = makeNodeProps({ component: bare, canvasData, manifest, componentData: new Map() });
        const b = makeNodeProps({ component: bare, canvasData, manifest, componentData: new Map() });
        expect(agentPropsEq(a, b)).toBe(true);
    });
});
