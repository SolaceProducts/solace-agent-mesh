// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import type { DecisionResult } from "@/features/activities/decisionResult";
import WorkflowGroup from "@/features/activities/flowChart/nodes/WorkflowGroup";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/WorkflowGroup",
    component: WorkflowGroup,
    parameters: {
        layout: "padded",
    },
    decorators: [
        Story => (
            <div className="flex">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof WorkflowGroup>;

export default meta;
type Story = StoryObj<typeof meta>;

const agentChild = (id: string, label: string) => createActivityLayoutNode({ id, type: "agent", data: { label } });

const collapsedNode = createActivityLayoutNode({
    id: "wf-collapsed",
    type: "group",
    height: 50,
    data: { label: "MyWorkflow", isCollapsed: true },
});

const expandedNode = createActivityLayoutNode({
    id: "wf-expanded",
    type: "group",
    width: 320,
    height: 300,
    children: [agentChild("agent-1", "InnerAgent")],
    data: { label: "MyWorkflow", isExpanded: true },
});

export const Collapsed: Story = {
    args: {
        node: collapsedNode,
        onExpand: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("MyWorkflow")).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: /expand/i })).toBeInTheDocument();
    },
};

export const Expanded: Story = {
    args: { node: expandedNode, onCollapse: fn() },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("MyWorkflow")).toBeInTheDocument();
        expect(canvas.getByText("InnerAgent")).toBeInTheDocument();
    },
};

export const ExpandedShowsCollapseButton: Story = {
    args: { node: expandedNode, onCollapse: fn() },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MyWorkflow");
        expect(canvas.getByRole("button", { name: /collapse/i })).toBeInTheDocument();
    },
};

export const InvokesOnExpand: Story = {
    args: { node: collapsedNode, onExpand: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MyWorkflow");
        await userEvent.click(canvas.getByRole("button", { name: /expand/i }));
        expect(args.onExpand).toHaveBeenCalledTimes(1);
        expect(args.onExpand).toHaveBeenCalledWith(collapsedNode.id);
    },
};

export const InvokesOnCollapse: Story = {
    args: { node: expandedNode, onCollapse: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MyWorkflow");
        await userEvent.click(canvas.getByRole("button", { name: /collapse/i }));
        expect(args.onCollapse).toHaveBeenCalledTimes(1);
        expect(args.onCollapse).toHaveBeenCalledWith(expandedNode.id);
    },
};

export const InvokesOnClickWhenCollapsed: Story = {
    args: { node: collapsedNode, onClick: fn(), onExpand: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const label = await canvas.findByText("MyWorkflow");
        const clickable = label.closest("[role='button']") as HTMLElement;
        await userEvent.click(clickable);
        expect(args.onClick).toHaveBeenCalledTimes(1);
        expect(args.onClick).toHaveBeenCalledWith(collapsedNode);
    },
};

export const InvokesOnChildClick: Story = {
    args: { node: expandedNode, onChildClick: fn(), onCollapse: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const label = await canvas.findByText("InnerAgent");
        const clickable = label.closest("[role='button']") as HTMLElement;
        await userEvent.click(clickable);
        expect(args.onChildClick).toHaveBeenCalledTimes(1);
    },
};

const switchChild = (id: string, label: string) => createActivityLayoutNode({ id, type: "switch", data: { label } });

const parallelChild = (id: string, label: string, iterationIndex: number) => createActivityLayoutNode({ id, type: "agent", data: { label, iterationIndex } });

export const ExpandedRendersSwitchChild: Story = {
    args: {
        node: {
            ...expandedNode,
            id: "wf-switch",
            children: [switchChild("sw-1", "DecisionPoint")],
        },
        onCollapse: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MyWorkflow");
        expect(canvas.getByText("Switch")).toBeInTheDocument();
    },
};

export const ExpandedSkipsEmptyParallelBlock: Story = {
    args: {
        node: {
            ...expandedNode,
            id: "wf-empty-parallel",
            children: [agentChild("before", "BeforeParallel"), createActivityLayoutNode({ id: "empty-block", type: "parallelBlock", width: 0, height: 0 }), agentChild("after", "AfterParallel")],
        },
        onCollapse: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("BeforeParallel");
        expect(canvas.getByText("AfterParallel")).toBeInTheDocument();
        expect(canvasElement.querySelector("[data-parallel-block='empty-block']")).toBeNull();
    },
};

const decisionWorkflow = (decision: DecisionResult, route: string, prunedTargets: string[]) =>
    createActivityLayoutNode({
        ...expandedNode,
        id: "wf-decision",
        children: [
            createActivityLayoutNode({ id: "start", type: "agent", data: { label: "Start", variant: "pill" } }),
            createActivityLayoutNode({ id: "triage-node", type: "decision", data: { label: "triage", nodeId: "triage", dependsOn: [], status: "completed", decision, selectedBranch: route } }),
            createActivityLayoutNode({
                id: "branches",
                type: "parallelBlock",
                children: [route, ...prunedTargets].map((target, index) =>
                    createActivityLayoutNode({
                        id: `branch-${target}`,
                        type: "agent",
                        data: { label: target, nodeId: target, dependsOn: ["triage"], iterationIndex: index, isSkipped: target !== route || undefined },
                    })
                ),
            }),
        ],
    });

export const ExpandedRendersRoutedDecision: Story = {
    args: {
        node: decisionWorkflow(
            {
                answers: [{ key: "category", type: "choice", choice: "billing", confidence: 0.92 }],
                selectedBranch: "billing_agent",
                routedBranch: "billing_agent",
                escalated: false,
                model: "jev-1",
                durationMs: 41,
            },
            "billing_agent",
            ["technical_agent"]
        ),
        onCollapse: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MyWorkflow");
        expect(canvas.getByText("Decision")).toBeInTheDocument();
        expect(canvas.queryByText("triage")).toBeNull();
        expect(canvas.getByText("billing · 92% confidence")).toBeInTheDocument();
        expect(canvas.getAllByText("billing_agent")).toHaveLength(2);
        expect(canvas.queryByText(/escalated/)).not.toBeInTheDocument();
        const skipped = canvas.getByText("technical_agent").closest("[role='button']") as HTMLElement;
        expect(skipped.className).toContain("opacity-50");
        expect(skipped.dataset.skipped).toBe("true");
        expect(within(skipped).getByText("Skipped")).toHaveAttribute("title", "This step did not run. Select it to see why.");
        expect(skipped.className).toContain("border-dashed");
    },
};

export const ExpandedRendersEscalatedDecision: Story = {
    args: {
        node: decisionWorkflow(
            {
                answers: [{ key: "category", type: "choice", choice: "billing", confidence: 0.41 }],
                selectedBranch: "human_review",
                escalated: true,
                escalationReason: "low_confidence",
            },
            "human_review",
            ["billing_agent", "technical_agent"]
        ),
        onCollapse: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MyWorkflow");
        expect(canvas.getByText("billing · 41% confidence")).toBeInTheDocument();
        expect(canvas.getByText("escalated · low confidence")).toBeInTheDocument();
        expect(canvas.getAllByText("human_review")).toHaveLength(2);
    },
};

export const ExpandedRendersEscalationRoutedDecision: Story = {
    args: {
        node: createActivityLayoutNode({
            ...expandedNode,
            id: "wf-decision-escalation-choice",
            children: [
                createActivityLayoutNode({ id: "start", type: "agent", data: { label: "Start", variant: "pill" } }),
                createActivityLayoutNode({
                    id: "triage-node",
                    type: "decision",
                    data: {
                        label: "triage",
                        nodeId: "triage",
                        dependsOn: [],
                        status: "completed",
                        decision: { answers: [{ key: "category", type: "choice", choice: "billing", confidence: 0.42 }], selectedBranch: "classify", escalated: true, escalationReason: "low_confidence" },
                        selectedBranch: "classify",
                        escalationRoute: "general_agent",
                    },
                }),
                createActivityLayoutNode({ id: "classify-node", type: "agent", data: { label: "classify", nodeId: "classify", dependsOn: ["triage"] } }),
                createActivityLayoutNode({
                    id: "routes",
                    type: "parallelBlock",
                    children: ["general_agent", "billing_agent"].map((target, index) =>
                        createActivityLayoutNode({
                            id: `route-${target}`,
                            type: "agent",
                            data: { label: target, nodeId: target, dependsOn: target === "general_agent" ? ["triage", "classify"] : ["classify"], iterationIndex: index, isSkipped: target !== "general_agent" || undefined },
                        })
                    ),
                }),
            ],
        }),
        onCollapse: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MyWorkflow");
        expect(canvas.getByText("escalated · low confidence")).toBeInTheDocument();
        expect(canvas.getByText("after escalation")).toBeInTheDocument();
        expect(canvas.getByTitle("routed after escalation: general_agent")).toBeInTheDocument();
    },
};

export const ExpandedRendersParallelBlockWithBranches: Story = {
    args: {
        node: {
            ...expandedNode,
            id: "wf-parallel",
            children: [
                agentChild("preceding", "Start"),
                createActivityLayoutNode({
                    id: "block",
                    type: "parallelBlock",
                    width: 0,
                    height: 0,
                    children: [parallelChild("b0-n0", "Branch0Node0", 0), parallelChild("b0-n1", "Branch0Node1", 0), parallelChild("b1-n0", "Branch1Node0", 1)],
                }),
                agentChild("following", "End"),
            ],
        },
        onCollapse: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Branch0Node0")).toBeInTheDocument();
        expect(canvas.getByText("Branch0Node1")).toBeInTheDocument();
        expect(canvas.getByText("Branch1Node0")).toBeInTheDocument();
        const block = canvasElement.querySelector("[data-parallel-block='block']") as HTMLElement;
        expect(block).not.toBeNull();
        expect(block.getAttribute("data-preceding-node")).toBe("preceding");
        expect(block.getAttribute("data-following-node")).toBe("following");
    },
};

const dagLevel = (agent: ReturnType<typeof createActivityLayoutNode>) => createActivityLayoutNode({ id: `level-${agent.id}`, type: "parallelBlock", data: { label: "Parallel" }, children: [agent] });

const dagAgent = (id: string, dependsOn: string[], children: ReturnType<typeof createActivityLayoutNode>[] = []) => createActivityLayoutNode({ id, type: "agent", data: { label: `${id}Agent`, nodeId: id, dependsOn, iterationIndex: 0 }, children });

export const ExpandedDrawsSkipLevelEdgeBehindIntermediateCard: Story = {
    args: {
        node: {
            ...expandedNode,
            id: "wf-skip-edge",
            children: [
                createActivityLayoutNode({ id: "start", type: "agent", data: { label: "Start", variant: "pill" } }),
                dagLevel(dagAgent("intake", [])),
                dagLevel(dagAgent("enrich", ["intake"], [createActivityLayoutNode({ id: "enrich-llm", type: "llm", data: { label: "LLM" } })])),
                dagLevel(dagAgent("respond", ["intake", "enrich"])),
            ],
        },
        onCollapse: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("enrichAgent");
        const intermediateCard = canvasElement.querySelector('[data-node-id="enrich"]') as HTMLElement;
        const connectors = await waitFor(() => {
            const svg = canvas.getByTestId("workflowConnectors");
            expect(svg.querySelectorAll("path")).toHaveLength(4);
            return svg;
        });

        const cardRect = intermediateCard.getBoundingClientRect();
        const tallestEdge = Math.max(...Array.from(connectors.querySelectorAll("path")).map(path => path.getBBox().height));
        expect(tallestEdge).toBeGreaterThan(cardRect.height);

        intermediateCard.scrollIntoView({ block: "center" });
        const centre = intermediateCard.getBoundingClientRect();
        connectors.style.pointerEvents = "auto";
        const topmost = document.elementFromPoint(centre.left + centre.width / 2, centre.top + centre.height / 2);
        expect(intermediateCard.contains(topmost)).toBe(true);
    },
};
