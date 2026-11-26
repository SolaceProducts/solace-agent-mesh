// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ReactFlowProvider } from "@xyflow/react";
import { describe, expect, test, vi } from "vitest";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import type { LayoutNode, WorkflowFlowNodeData } from "../utils/types";
import WorkflowNodeRenderer from "../WorkflowNodeRenderer";

import { renderWorkflowChild } from "./renderWorkflowChild";
import ToolFlowNode from "./ToolFlowNode";
import ToolNode from "./ToolNode";

const toolNode: LayoutNode = createWorkflowLayoutNode({
    id: "make_chart",
    type: "tool",
    data: { toolName: "create_pptx", label: "make_chart" },
});

describe("ToolNode", () => {
    test("renders the tool name and badge", () => {
        render(<ToolNode node={toolNode} />);
        expect(screen.getByText("create_pptx")).toBeInTheDocument();
        expect(screen.getByText("Tool")).toBeInTheDocument();
    });

    test("shows only the tool part of a toolset-bundled name", () => {
        const node = createWorkflowLayoutNode({
            id: "get_weather_tool",
            type: "tool",
            data: { toolName: "WeatherToolset__get_current_weather", label: "get_weather_tool" },
        });
        render(<ToolNode node={node} />);
        expect(screen.getByText("get_current_weather")).toBeInTheDocument();
        expect(screen.queryByText("WeatherToolset__get_current_weather")).not.toBeInTheDocument();
    });

    test("shows connector / tool for a connector-bound tool node", () => {
        const node = createWorkflowLayoutNode({
            id: "fetch_pets",
            type: "tool",
            data: { toolName: "find_pets_by_status", connectorName: "PetStoreAPI", label: "fetch_pets" },
        });
        render(<ToolNode node={node} />);
        expect(screen.getByText("PetStoreAPI / find_pets_by_status")).toBeInTheDocument();
        expect(screen.queryByText("fetch_pets")).not.toBeInTheDocument();
    });

    test("shows just the connector when the connector's tool is not named", () => {
        const node = createWorkflowLayoutNode({ id: "send", type: "tool", data: { connectorName: "SlackConn", label: "send" } });
        render(<ToolNode node={node} />);
        expect(screen.getByText("SlackConn")).toBeInTheDocument();
    });

    test("falls back to the node label when no tool name is set", () => {
        const node = createWorkflowLayoutNode({ id: "fallback", type: "tool", data: { label: "fallback" } });
        render(<ToolNode node={node} />);
        expect(screen.getByText("fallback")).toBeInTheDocument();
    });

    test.each([
        ["a number", 123],
        ["a list", ["create_pptx"]],
        ["a map", { name: "create_pptx" }],
        ["null", null],
    ])("falls back to the node label when the tool name is %s", (_label, toolName) => {
        const node = createWorkflowLayoutNode({
            id: "malformed",
            type: "tool",
            data: { toolName: toolName as unknown as string, label: "malformed" },
        });
        render(<ToolNode node={node} />);
        expect(screen.getByText("malformed")).toBeInTheDocument();
    });

    test("renders the full tool name in a truncating element so it is readable on hover", () => {
        render(<ToolNode node={toolNode} />);
        const name = screen.getByText("create_pptx");
        expect(name).toBeInTheDocument();
        expect(name).toHaveClass("truncate");
    });

    test("invokes onClick with the node and stops propagation", async () => {
        const user = userEvent.setup();
        const onClick = vi.fn();
        const onParentClick = vi.fn();
        render(
            <div onClick={onParentClick}>
                <ToolNode node={toolNode} onClick={onClick} />
            </div>
        );
        await user.click(screen.getByText("create_pptx"));
        expect(onClick).toHaveBeenCalledWith(toolNode);
        expect(onParentClick).not.toHaveBeenCalled();
    });
});

describe("ToolFlowNode", () => {
    test("renders the wrapped ToolNode", () => {
        const data: WorkflowFlowNodeData = { layoutNode: toolNode };
        render(
            <ReactFlowProvider>
                <ToolFlowNode id="make_chart" type="tool" data={data} selected={false} dragging={false} draggable={false} selectable={false} deletable={false} zIndex={0} isConnectable positionAbsoluteX={0} positionAbsoluteY={0} />
            </ReactFlowProvider>
        );
        expect(screen.getByText("create_pptx")).toBeInTheDocument();
    });
});

describe("tool node renderers", () => {
    test("WorkflowNodeRenderer renders a tool node", () => {
        const nodeRefs = { current: new Map<string, HTMLDivElement>() };
        render(<WorkflowNodeRenderer nodes={[toolNode]} nodeRefs={nodeRefs} />);
        expect(screen.getByText("create_pptx")).toBeInTheDocument();
    });

    test("renderWorkflowChild renders a tool node", () => {
        const data: WorkflowFlowNodeData = { layoutNode: toolNode };
        render(<>{renderWorkflowChild(toolNode, data)}</>);
        expect(screen.getByText("create_pptx")).toBeInTheDocument();
    });
});
