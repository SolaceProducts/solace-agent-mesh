// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { connectorPropsEq } from "./ConnectorCardNode";

describe("connectorPropsEq", () => {
    const component = { name: "c", filename: "c.yaml", type: "connector" };
    const canvasData = { name: "c", filename: "c.yaml", type: "connector" };

    test("returns true on identical input", () => {
        const a = makeNodeProps({ component, canvasData, isSingleConnector: false });
        const b = makeNodeProps({ component, canvasData, isSingleConnector: false });
        expect(connectorPropsEq(a, b)).toBe(true);
    });

    test("returns false when isSingleConnector flips", () => {
        const a = makeNodeProps({ component, canvasData, isSingleConnector: false });
        const b = makeNodeProps({ component, canvasData, isSingleConnector: true });
        expect(connectorPropsEq(a, b)).toBe(false);
    });

    test("returns false when component reference changes", () => {
        const a = makeNodeProps({ component, canvasData });
        const b = makeNodeProps({ component: { ...component }, canvasData });
        expect(connectorPropsEq(a, b)).toBe(false);
    });
});
