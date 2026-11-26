// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { LayoutNode } from "../utils/types";

import ToolNode from "./ToolNode";

function toolNode(data: Partial<LayoutNode["data"]>): LayoutNode {
    return { id: "tool_0", type: "tool", data: { label: "deploy", nodeId: "deploy", ...data }, x: 0, y: 0, width: 0, height: 0, children: [] };
}

describe("ToolNode", () => {
    test("marks a failed tool node as an error", () => {
        render(<ToolNode node={toolNode({ status: "error" })} />);

        expect(screen.getByTestId("tool-node-deploy")).toHaveAttribute("data-status", "error");
    });

    test("marks a skipped tool node as skipped", () => {
        render(<ToolNode node={toolNode({ status: "skipped", isSkipped: true })} />);

        const node = screen.getByTestId("tool-node-deploy");
        expect(node).toHaveAttribute("data-skipped", "true");
        expect(node).toHaveAttribute("data-status", "skipped");
    });
});
