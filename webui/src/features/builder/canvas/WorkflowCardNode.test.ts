// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { workflowPropsEq } from "./WorkflowCardNode";

describe("workflowPropsEq", () => {
    const component = { name: "wf", filename: "wf.yaml", type: "workflow" };
    const canvasData = { name: "wf", filename: "wf.yaml", type: "workflow" };

    test("returns true when component and canvasData are reference-equal", () => {
        const a = makeNodeProps({ component, canvasData });
        const b = makeNodeProps({ component, canvasData });
        expect(workflowPropsEq(a, b)).toBe(true);
    });

    test("returns false when canvasData reference changes", () => {
        const a = makeNodeProps({ component, canvasData });
        const b = makeNodeProps({ component, canvasData: { ...canvasData } });
        expect(workflowPropsEq(a, b)).toBe(false);
    });

    test("returns false when selection toggles", () => {
        const a = makeNodeProps({ component, canvasData }, false);
        const b = makeNodeProps({ component, canvasData }, true);
        expect(workflowPropsEq(a, b)).toBe(false);
    });
});
