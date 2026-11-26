// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { gatewayPropsEq } from "./GatewayCardNode";

describe("gatewayPropsEq", () => {
    const component = { name: "g", filename: "g.yaml", type: "gateway" };
    const canvasData = { name: "g", filename: "g.yaml", type: "gateway" };

    test("returns true on identical input", () => {
        const a = makeNodeProps({ component, canvasData, isSingleGateway: true, compact: false });
        const b = makeNodeProps({ component, canvasData, isSingleGateway: true, compact: false });
        expect(gatewayPropsEq(a, b)).toBe(true);
    });

    test("returns false when compact flag changes", () => {
        const a = makeNodeProps({ component, canvasData, compact: false });
        const b = makeNodeProps({ component, canvasData, compact: true });
        expect(gatewayPropsEq(a, b)).toBe(false);
    });
});
