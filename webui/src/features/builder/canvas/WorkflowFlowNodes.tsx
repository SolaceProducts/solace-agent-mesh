// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import { BaseEdge, type EdgeProps, getSmoothStepPath, Handle, type Node, type NodeProps, Position } from "@xyflow/react";
import { Workflow } from "lucide-react";

import { TruncatedText } from "@/ui/truncated-text";

import AgentNode from "@/features/workflowVisualization/nodes/AgentNode";
import ConditionPillNode from "@/features/workflowVisualization/nodes/ConditionPillNode";
import DecisionNode from "@/features/workflowVisualization/nodes/DecisionNode";
import EndNode from "@/features/workflowVisualization/nodes/EndNode";
import { EscalationAnswerHandles } from "@/features/workflowVisualization/nodes/EscalationAnswerHandles";
import LoopNode from "@/features/workflowVisualization/nodes/LoopNode";
import MapNode from "@/features/workflowVisualization/nodes/MapNode";
import { renderWorkflowChild } from "@/features/workflowVisualization/nodes/renderWorkflowChild";
import StartNode from "@/features/workflowVisualization/nodes/StartNode";
import SwitchNode from "@/features/workflowVisualization/nodes/SwitchNode";
import ToolNode from "@/features/workflowVisualization/nodes/ToolNode";
import WorkflowRefNode from "@/features/workflowVisualization/nodes/WorkflowRefNode";
import { escalationAnswerLabelProps, type LayoutNode, type NodeProps as WfNodeProps, type WorkflowFlowNodeData } from "@/features/workflowVisualization/utils/types";

import { humanizeComponentName } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";

import { CardShell, typeColor } from "./CardShell";
import { useWorkflowCardPill } from "./useWorkflowCardPill";
import { wfEdgeStyle } from "./wfEdgeStyle";

type WfLayoutNode = WfNodeProps["node"];

interface WfFlowNodeData {
    layoutNode: WfLayoutNode;
    onNodeClick?: (node: unknown) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
    onHighlightNodes?: (nodeIds: string[]) => void;
    knownNodeIds?: Set<string>;
    selectedNodeId?: string;
    highlightedNodeIds?: Set<string>;
    [key: string]: unknown;
}

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

function wfLayoutNodeEq(a: unknown, b: unknown): boolean {
    if (a === b) return true;
    try {
        return JSON.stringify(a) === JSON.stringify(b);
    } catch {
        return false;
    }
}
export function wfNodePropsEq(prev: NodeProps<Node<WfFlowNodeData>>, next: NodeProps<Node<WfFlowNodeData>>): boolean {
    if (prev.selected !== next.selected) return false;
    const a = prev.data as unknown as WfFlowNodeData;
    const b = next.data as unknown as WfFlowNodeData;
    if (a.agentCanvasData !== b.agentCanvasData) return false;
    if (a.workflowCanvasData !== b.workflowCanvasData) return false;
    if (a.workflowComponent !== b.workflowComponent) return false;
    if (a.agentManifestStatus !== b.agentManifestStatus) return false;
    if (a.agentManifestId !== b.agentManifestId) return false;
    if (a.agentComponentName !== b.agentComponentName) return false;
    if (a.hasDepWaitingForInput !== b.hasDepWaitingForInput) return false;
    if (a.allChildrenDone !== b.allChildrenDone) return false;
    const aHi = a.highlightedNodeIds?.has(a.layoutNode?.id) ?? false;
    const bHi = b.highlightedNodeIds?.has(b.layoutNode?.id) ?? false;
    if (aHi !== bHi) return false;
    return wfLayoutNodeEq(a.layoutNode, b.layoutNode);
}

function renderChildren(children: unknown[], data: WfFlowNodeData) {
    return (children as LayoutNode[]).map(child => (
        <div key={child.id} style={{ position: "relative" }}>
            {renderWorkflowChild(child, data as unknown as WorkflowFlowNodeData)}
        </div>
    ));
}

function WfStartFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    const rawData = data as unknown as Record<string, unknown>;
    const wfComponent = rawData.workflowComponent as { id?: string; name: string; status?: string; origin?: string } | undefined;
    const wfCanvasData = rawData.workflowCanvasData as ComponentCanvasData | null | undefined;
    const pendingChildren = rawData.allChildrenDone === false ? { builderResponding: wfCanvasData?.isBuilderResponding === true } : undefined;
    const pill = useWorkflowCardPill(wfCanvasData, { ...wfComponent, type: "workflow" }, { pendingChildren });

    if (wfComponent) {
        return (
            <>
                <Handle type="target" position={Position.Left} id="left" style={HANDLE_STYLE} />
                <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
                <CardShell selected={selected} {...pill} componentName={wfComponent.name} testEligible testButtonAlign="center">
                    <div className="flex items-center justify-between gap-2 px-4 py-3">
                        <div className="flex min-w-0 items-center gap-2">
                            <Workflow className="h-5 w-5 shrink-0" style={{ color: typeColor("workflow") }} />
                            <TruncatedText text={wfComponent.name} className="text-sm font-semibold">
                                {humanizeComponentName(wfComponent.name)}
                            </TruncatedText>
                        </div>
                        <span className="shrink-0 rounded px-2 py-0.5 text-sm font-medium text-[var(--secondary-text-wMain)]">Workflow</span>
                    </div>
                </CardShell>
                <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            </>
        );
    }

    return (
        <>
            <StartNode node={data.layoutNode} isHighlighted={isHighlighted} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
        </>
    );
}
export const WfStartFlowNode = memo(WfStartFlowNodeInner, wfNodePropsEq);

function WfEndFlowNodeInner({ data }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <EndNode node={data.layoutNode} isHighlighted={isHighlighted} />
        </>
    );
}
export const WfEndFlowNode = memo(WfEndFlowNodeInner, wfNodePropsEq);

function WfAgentFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    const rawData = data as unknown as Record<string, unknown>;
    const agentCanvasData = rawData.agentCanvasData as ComponentCanvasData | null | undefined;
    const agentManifestStatus = rawData.agentManifestStatus as string | undefined;
    const agentManifestId = rawData.agentManifestId as string | undefined;
    const agentComponentName = rawData.agentComponentName as string | undefined;
    const hasDepWaitingForInput = rawData.hasDepWaitingForInput as boolean | undefined;
    const pill = useWorkflowCardPill(
        agentCanvasData,
        { id: agentCanvasData?.id ?? agentManifestId, name: agentComponentName, type: "agent", origin: agentCanvasData?.origin, status: agentCanvasData?.status ?? agentManifestStatus },
        { depWaitingForInput: hasDepWaitingForInput }
    );
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <CardShell
                selected={selected}
                {...pill}
                className="rounded [&_.card-surface]:border-0 [&_.card-surface]:bg-transparent [&_.card-surface]:shadow-none"
                componentName={agentComponentName}
                testEligible={!!agentComponentName}
                testButtonAlign="center"
            >
                <AgentNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} />
            </CardShell>
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <Handle type="source" position={Position.Right} id="right" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
        </>
    );
}
export const WfAgentFlowNode = memo(WfAgentFlowNodeInner, wfNodePropsEq);

function WfWorkflowRefFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <WorkflowRefNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <Handle type="source" position={Position.Right} id="right" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
        </>
    );
}
export const WfWorkflowRefFlowNode = memo(WfWorkflowRefFlowNodeInner, wfNodePropsEq);

function WfToolFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <ToolNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <Handle type="source" position={Position.Right} id="right" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
        </>
    );
}
export const WfToolFlowNode = memo(WfToolFlowNodeInner, wfNodePropsEq);

function WfSwitchFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <SwitchNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
        </>
    );
}
export const WfSwitchFlowNode = memo(WfSwitchFlowNodeInner, wfNodePropsEq);

function WfDecisionFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <DecisionNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
            <EscalationAnswerHandles type="target" />
        </>
    );
}
export const WfDecisionFlowNode = memo(WfDecisionFlowNodeInner, wfNodePropsEq);

function WfMapFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <MapNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} onExpand={data.onExpand} onCollapse={data.onCollapse} renderChildren={(children: unknown[]) => renderChildren(children, data)} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
            <EscalationAnswerHandles type="target" />
        </>
    );
}
export const WfMapFlowNode = memo(WfMapFlowNodeInner);

function WfLoopFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <LoopNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} onExpand={data.onExpand} onCollapse={data.onCollapse} renderChildren={(children: unknown[]) => renderChildren(children, data)} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
            <EscalationAnswerHandles type="target" />
        </>
    );
}
export const WfLoopFlowNode = memo(WfLoopFlowNodeInner);

function WfConditionPillFlowNodeInner({ data, selected }: NodeProps<Node<WfFlowNodeData>>) {
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <ConditionPillNode node={data.layoutNode} isSelected={selected} onHighlightNodes={data.onHighlightNodes} knownNodeIds={data.knownNodeIds} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
        </>
    );
}
export const WfConditionPillFlowNode = memo(WfConditionPillFlowNodeInner, wfNodePropsEq);

const PILL_TOP_OFFSET = 14;

const WfEdgeComponent: React.FC<EdgeProps> = ({ id, sourceX, sourceY, targetX, targetY: rawTargetY, sourcePosition, targetPosition, markerEnd, data }) => {
    const edgeStyle = wfEdgeStyle(data);
    const hasTargetPill = !(data as { noTargetPill?: boolean } | undefined)?.noTargetPill;
    const targetY = targetPosition === Position.Top && hasTargetPill ? rawTargetY - PILL_TOP_OFFSET : rawTargetY;

    if (data?.isStraight) {
        const path = `M ${sourceX} ${sourceY} L ${targetX} ${targetY}`;
        return <BaseEdge id={id} path={path} style={edgeStyle} markerEnd={markerEnd} />;
    }

    if (data?.isConditionEdge) {
        const midY = sourceY + (targetY - sourceY) / 2;
        const r = 6;
        const dx = targetX - sourceX;
        const sideSign = Math.sign(dx) || 1;
        const absDx = Math.abs(dx);
        const cornerR = Math.min(r, absDx / 2, Math.abs(midY - sourceY), Math.abs(targetY - midY));
        if (absDx < 0.5) {
            return <BaseEdge id={id} path={`M ${sourceX} ${sourceY} L ${targetX} ${targetY}`} style={edgeStyle} markerEnd={markerEnd} />;
        }
        const path =
            `M ${sourceX} ${sourceY}` +
            ` L ${sourceX} ${midY - cornerR}` +
            ` Q ${sourceX} ${midY} ${sourceX + sideSign * cornerR} ${midY}` +
            ` L ${targetX - sideSign * cornerR} ${midY}` +
            ` Q ${targetX} ${midY} ${targetX} ${midY + cornerR}` +
            ` L ${targetX} ${targetY}`;
        return <BaseEdge id={id} path={path} style={edgeStyle} markerEnd={markerEnd} />;
    }

    const [edgePath, labelX, labelY] = getSmoothStepPath({
        sourceX,
        sourceY,
        targetX,
        targetY,
        sourcePosition,
        targetPosition,
        borderRadius: 24,
        offset: 32,
    });

    const label = data?.escalationAnswer ? escalationAnswerLabelProps(labelX, labelY) : {};
    return <BaseEdge id={id} path={edgePath} style={edgeStyle} markerEnd={markerEnd} {...label} />;
};

export const WfDiagramEdge = memo(WfEdgeComponent);
