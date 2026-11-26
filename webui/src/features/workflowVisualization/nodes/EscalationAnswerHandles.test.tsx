// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { type NodeProps, ReactFlowProvider } from "@xyflow/react";
import type { ComponentType } from "react";
import { describe, expect, test } from "vitest";

import { WfAgentFlowNode, WfDecisionFlowNode, WfLoopFlowNode, WfMapFlowNode, WfToolFlowNode, WfWorkflowRefFlowNode } from "@/features/builder/canvas/WorkflowFlowNodes";
import type { LayoutNode } from "@/features/workflowVisualization/utils/types";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";
import { makeNodeProps } from "@/testing/helpers/nodeProps";

import AgentFlowNode from "./AgentFlowNode";
import DecisionFlowNode from "./DecisionFlowNode";
import LoopFlowNode from "./LoopFlowNode";
import MapFlowNode from "./MapFlowNode";
import ToolFlowNode from "./ToolFlowNode";
import WorkflowRefFlowNode from "./WorkflowRefFlowNode";

const SOURCE_HANDLES = ["answer-out-left", "answer-out-right"];
const TARGET_HANDLES = ["answer-in-left", "answer-in-right"];

const layoutNodes: Record<string, LayoutNode> = {
    agent: createWorkflowLayoutNode({ id: "classify", type: "agent", data: { label: "classify", agentName: "Classifier" } }),
    tool: createWorkflowLayoutNode({ id: "lookup", type: "tool", data: { label: "lookup", toolName: "lookup_tool" } }),
    workflow: createWorkflowLayoutNode({ id: "sub", type: "workflow", data: { label: "sub", workflowName: "SubFlow" } }),
    decision: createWorkflowLayoutNode({ id: "triage", type: "decision", data: { label: "triage", questions: [], routeOn: "department" } }),
    loop: createWorkflowLayoutNode({ id: "retry", type: "loop", data: { label: "retry", condition: "$.done == false", maxIterations: 3, childNodeId: "triage" } }),
    map: createWorkflowLayoutNode({ id: "fan_out", type: "map", data: { label: "fan_out", childNodeId: "triage" } }),
};

function handleIds(Component: ComponentType<NodeProps>, layoutNode: LayoutNode): string[] {
    const { container } = render(
        <ReactFlowProvider>
            <Component {...makeNodeProps({ layoutNode })} />
        </ReactFlowProvider>
    );
    return [...container.querySelectorAll("[data-handleid]")].map(el => el.getAttribute("data-handleid") ?? "");
}

const renderers: Record<string, Record<string, ComponentType<NodeProps>>> = {
    "static diagram": {
        agent: AgentFlowNode as unknown as ComponentType<NodeProps>,
        tool: ToolFlowNode as unknown as ComponentType<NodeProps>,
        workflow: WorkflowRefFlowNode as unknown as ComponentType<NodeProps>,
        decision: DecisionFlowNode as unknown as ComponentType<NodeProps>,
        loop: LoopFlowNode as unknown as ComponentType<NodeProps>,
        map: MapFlowNode as unknown as ComponentType<NodeProps>,
    },
    "Builder canvas": {
        agent: WfAgentFlowNode as unknown as ComponentType<NodeProps>,
        tool: WfToolFlowNode as unknown as ComponentType<NodeProps>,
        workflow: WfWorkflowRefFlowNode as unknown as ComponentType<NodeProps>,
        decision: WfDecisionFlowNode as unknown as ComponentType<NodeProps>,
        loop: WfLoopFlowNode as unknown as ComponentType<NodeProps>,
        map: WfMapFlowNode as unknown as ComponentType<NodeProps>,
    },
};

describe.each(Object.entries(renderers))("escalation answer handles on the %s", (_renderer, components) => {
    test.each(["agent", "tool", "workflow", "decision"])("a %s node, which can be a rerouting escalate target, carries the answer-out handles", type => {
        expect(handleIds(components[type], layoutNodes[type])).toEqual(expect.arrayContaining(SOURCE_HANDLES));
    });

    test.each(["decision", "loop", "map"])("a %s node carries the answer-in handles its return edge enters by", type => {
        expect(handleIds(components[type], layoutNodes[type])).toEqual(expect.arrayContaining(TARGET_HANDLES));
    });

    test.each(["loop", "map"])("a %s node carries the answer-out handles for an escalate target it draws", type => {
        expect(handleIds(components[type], layoutNodes[type])).toEqual(expect.arrayContaining(SOURCE_HANDLES));
    });
});
