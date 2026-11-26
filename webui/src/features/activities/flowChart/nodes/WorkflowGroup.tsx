// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, type ReactNode, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Maximize2, Minimize2, Workflow } from "lucide-react";

import { clickableNodeProps } from "@/utils/nodeInteraction";

import { Button } from "@/ui";

import { computeDependencyEdges } from "../utils/dependencyEdges";
import { ACTIVITY_NODE_BASE_STYLES, ACTIVITY_NODE_LAYOUT, ACTIVITY_NODE_PROCESSING_CLASS, ACTIVITY_NODE_SELECTED_CLASS, CONNECTOR_LINE_CLASSES, CONNECTOR_SIZES, CONNECTOR_STROKE_CLASSES, CONTAINER_CHILDREN_CLASSES } from "../utils/nodeStyles";
import type { LayoutNode } from "../utils/types";

import AgentNode from "./AgentNode";
import DecisionNode from "./DecisionNode";
import LoopNode from "./LoopNode";
import MapNode from "./MapNode";
import SwitchNode from "./SwitchNode";
import ToolNode from "./ToolNode";

interface WorkflowGroupProps {
    node: LayoutNode;
    isSelected?: boolean;
    onClick?: (node: LayoutNode) => void;
    onChildClick?: (child: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
}

interface BezierPath {
    id: string;
    d: string;
}

function generateBezierPath(sourceRect: DOMRect, targetRect: DOMRect, containerRect: DOMRect, scale: number = 1): string {
    const x1 = (sourceRect.left + sourceRect.width / 2 - containerRect.left) / scale;
    const y1 = (sourceRect.bottom - containerRect.top) / scale;

    const x2 = (targetRect.left + targetRect.width / 2 - containerRect.left) / scale;
    const y2 = (targetRect.top - containerRect.top) / scale;

    const verticalDistance = Math.abs(y2 - y1);

    const targetControlOffset = 40;

    const sourceControlOffset = Math.max(verticalDistance * 1.0, 40);

    const cx1 = x1;
    const cy1 = y1 + sourceControlOffset;

    const cx2 = x2;
    const cy2 = y2 - targetControlOffset;

    return `M ${x1},${y1} C ${cx1},${cy1} ${cx2},${cy2} ${x2},${y2}`;
}

function resolveNodeEl(container: Element, layoutId: string): Element | null {
    const wrapper = container.querySelector(`[data-node-id="${layoutId}"]`);
    if (!wrapper) return null;
    return wrapper.firstElementChild || wrapper;
}

function isParallelBlockEl(el: Element): boolean {
    return (el as HTMLElement).dataset?.parallelBlock !== undefined;
}

function dependencyEdgePaths(container: Element, containerRect: DOMRect, scale: number, edges: { sourceId: string; targetId: string }[]): BezierPath[] {
    const paths: BezierPath[] = [];
    for (const edge of edges) {
        const sourceEl = resolveNodeEl(container, edge.sourceId);
        const targetEl = resolveNodeEl(container, edge.targetId);
        if (!sourceEl || !targetEl) continue;

        paths.push({
            id: `dep-${edge.sourceId}-${edge.targetId}`,
            d: generateBezierPath(sourceEl.getBoundingClientRect(), targetEl.getBoundingClientRect(), containerRect, scale),
        });
    }
    return paths;
}

function blockIncomingPaths(container: Element, containerRect: DOMRect, scale: number, blockEl: Element, blockId: string | undefined, precedingNodeId: string | undefined): BezierPath[] {
    const paths: BezierPath[] = [];
    if (!precedingNodeId) return paths;

    const precedingWrapper = container.querySelector(`[data-node-id="${precedingNodeId}"]`);
    if (!precedingWrapper) return paths;

    const sourceWrappers = isParallelBlockEl(precedingWrapper) ? Array.from(precedingWrapper.querySelectorAll('[data-branch-end="true"]')) : [precedingWrapper];
    const branchStartNodes = Array.from(blockEl.querySelectorAll('[data-branch-start="true"]'));

    for (const [sourceIndex, sourceWrapper] of sourceWrappers.entries()) {
        const sourceEl = sourceWrapper.firstElementChild || sourceWrapper;
        const sourceRect = sourceEl.getBoundingClientRect();

        for (const [index, branchStartEl] of branchStartNodes.entries()) {
            const targetEl = branchStartEl.firstElementChild || branchStartEl;
            paths.push({
                id: `${blockId}-start-${sourceIndex}-${index}`,
                d: generateBezierPath(sourceRect, targetEl.getBoundingClientRect(), containerRect, scale),
            });
        }
    }
    return paths;
}

function blockOutgoingPaths(container: Element, containerRect: DOMRect, scale: number, blockEl: Element, blockId: string | undefined, followingNodeId: string | undefined): BezierPath[] {
    const paths: BezierPath[] = [];
    if (!followingNodeId) return paths;

    const followingWrapper = container.querySelector(`[data-node-id="${followingNodeId}"]`);
    if (!followingWrapper || isParallelBlockEl(followingWrapper)) return paths;

    const followingEl = followingWrapper.firstElementChild || followingWrapper;
    const followingRect = followingEl.getBoundingClientRect();

    for (const [index, branchEndEl] of Array.from(blockEl.querySelectorAll('[data-branch-end="true"]')).entries()) {
        const sourceEl = branchEndEl.firstElementChild || branchEndEl;
        paths.push({
            id: `${blockId}-end-${index}`,
            d: generateBezierPath(sourceEl.getBoundingClientRect(), followingRect, containerRect, scale),
        });
    }
    return paths;
}

function heuristicBlockPaths(container: Element, containerRect: DOMRect, scale: number): BezierPath[] {
    const paths: BezierPath[] = [];
    for (const blockEl of Array.from(container.querySelectorAll<HTMLElement>("[data-parallel-block]"))) {
        const { parallelBlock: blockId, precedingNode, followingNode } = blockEl.dataset;
        paths.push(...blockIncomingPaths(container, containerRect, scale, blockEl, blockId, precedingNode), ...blockOutgoingPaths(container, containerRect, scale, blockEl, blockId, followingNode));
    }
    return paths;
}

const WorkflowGroup = ({ node, isSelected, onClick, onChildClick, onExpand, onCollapse }: WorkflowGroupProps) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const [bezierPaths, setBezierPaths] = useState<BezierPath[]>([]);
    const [resizeCounter, setResizeCounter] = useState(0);

    const isCollapsed = node.data.isCollapsed;
    const isExpanded = node.data.isExpanded;
    const isProcessing = node.data.hasProcessingChildren;
    const haloClass = isProcessing ? ACTIVITY_NODE_PROCESSING_CLASS : "";

    const dependencyEdges = useMemo(() => computeDependencyEdges(node), [node]);

    const WORKFLOW_WIDTH = ACTIVITY_NODE_LAYOUT.CONTAINER_WIDTH;
    const HEADER_HEIGHT = ACTIVITY_NODE_LAYOUT.HEADER_HEIGHT;

    const calculateBezierPaths = useCallback(() => {
        if (!containerRef.current || isCollapsed) {
            setBezierPaths([]);
            return;
        }

        const container = containerRef.current;
        const containerRect = container.getBoundingClientRect();

        const scale = containerRect.width / container.offsetWidth;

        setBezierPaths(dependencyEdges ? dependencyEdgePaths(container, containerRect, scale, dependencyEdges) : heuristicBlockPaths(container, containerRect, scale));
    }, [isCollapsed, dependencyEdges]);

    useLayoutEffect(() => {
        calculateBezierPaths();
    }, [node.children, isCollapsed, resizeCounter, calculateBezierPaths]);

    useEffect(() => {
        if (!containerRef.current || isCollapsed) return;

        const resizeObserver = new ResizeObserver(() => {
            setResizeCounter(c => c + 1);
        });

        resizeObserver.observe(containerRef.current);
        const nodes = containerRef.current.querySelectorAll("[data-node-id]");
        nodes.forEach(node => resizeObserver.observe(node));

        return () => resizeObserver.disconnect();
    }, [node.children, isCollapsed]);

    useEffect(() => {
        if (!containerRef.current || isCollapsed) return;

        let transformedParent: Element | null = containerRef.current.parentElement;
        while (transformedParent && !transformedParent.hasAttribute("style")) {
            transformedParent = transformedParent.parentElement;
        }

        if (!transformedParent) return;

        const mutationObserver = new MutationObserver(mutations => {
            const hasStyleChange = mutations.some(m => m.attributeName === "style");
            if (hasStyleChange) {
                setResizeCounter(c => c + 1);
            }
        });

        let current: Element | null = transformedParent;
        while (current && current !== document.body) {
            mutationObserver.observe(current, { attributes: true, attributeFilter: ["style"] });
            current = current.parentElement;
        }

        return () => mutationObserver.disconnect();
    }, [isCollapsed]);

    const renderChild = useCallback(
        (child: LayoutNode, precedingNodeId?: string, followingNodeId?: string): ReactNode => {
            const childProps = {
                node: child,
                onClick: onChildClick,
                onExpand,
                onCollapse,
            };

            switch (child.type) {
                case "agent":
                    return (
                        <div key={child.id} data-node-id={child.id}>
                            <AgentNode {...childProps} onChildClick={onChildClick} />
                        </div>
                    );
                case "tool":
                    return (
                        <div key={child.id} data-node-id={child.id}>
                            <ToolNode {...childProps} />
                        </div>
                    );
                case "switch":
                    return (
                        <div key={child.id} data-node-id={child.id}>
                            <SwitchNode {...childProps} />
                        </div>
                    );
                case "decision":
                    return (
                        <div key={child.id} data-node-id={child.id}>
                            <DecisionNode {...childProps} />
                        </div>
                    );
                case "loop":
                    return (
                        <div key={child.id} data-node-id={child.id}>
                            <LoopNode {...childProps} onChildClick={onChildClick} />
                        </div>
                    );
                case "map":
                    return (
                        <div key={child.id} data-node-id={child.id}>
                            <MapNode {...childProps} onChildClick={onChildClick} />
                        </div>
                    );
                case "group":
                    return (
                        <div key={child.id} data-node-id={child.id}>
                            <WorkflowGroup {...childProps} onChildClick={onChildClick} />
                        </div>
                    );
                case "parallelBlock": {
                    if (child.children.length === 0) {
                        return null;
                    }
                    const branches = new Map<number, LayoutNode[]>();
                    for (const parallelChild of child.children) {
                        const branchIdx = parallelChild.data.iterationIndex ?? 0;
                        if (!branches.has(branchIdx)) {
                            branches.set(branchIdx, []);
                        }
                        branches.get(branchIdx)!.push(parallelChild);
                    }

                    const sortedBranches = Array.from(branches.entries()).sort((a, b) => a[0] - b[0]);

                    return (
                        <div key={child.id} data-node-id={child.id} data-parallel-block={child.id} data-preceding-node={precedingNodeId} data-following-node={followingNodeId} className="my-12 flex flex-row items-start gap-4">
                            {sortedBranches.map(([branchIdx, branchChildren]) => (
                                <div key={`branch-${branchIdx}`} className="flex flex-col items-center gap-2">
                                    {branchChildren.map((branchChild, nodeIdx) => (
                                        <Fragment key={branchChild.id}>
                                            <div data-node-id={branchChild.id} data-branch-start={nodeIdx === 0 ? "true" : undefined} data-branch-end={nodeIdx === branchChildren.length - 1 ? "true" : undefined}>
                                                {renderChild(branchChild)}
                                            </div>
                                            {!dependencyEdges && nodeIdx < branchChildren.length - 1 && <div className={`${CONNECTOR_SIZES.BRANCH} ${CONNECTOR_LINE_CLASSES}`} />}
                                        </Fragment>
                                    ))}
                                </div>
                            ))}
                        </div>
                    );
                }
                default:
                    return null;
            }
        },
        [onChildClick, onExpand, onCollapse, dependencyEdges]
    );

    if (isCollapsed) {
        return (
            <div {...clickableNodeProps(() => onClick?.(node))} className={`${ACTIVITY_NODE_BASE_STYLES.RECTANGULAR} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""} ${haloClass}`} style={{ width: `${WORKFLOW_WIDTH}px` }}>
                <div className="flex min-w-0 items-center gap-2">
                    <Workflow className="h-4 w-4 flex-shrink-0 text-(--brand-wMain)" />
                    <div className="truncate text-sm font-semibold">{node.data.label}</div>
                </div>
                {onExpand && (
                    <Button
                        onClick={e => {
                            e.stopPropagation();
                            onExpand(node.id);
                        }}
                        variant="ghost"
                        size="icon"
                        className="h-8 w-8"
                        tooltip="Expand"
                    >
                        <Maximize2 className="h-4 w-4" />
                    </Button>
                )}
            </div>
        );
    }

    return (
        <div className={`flex flex-col px-4 ${haloClass}`} style={{ minWidth: `${WORKFLOW_WIDTH + 72}px` }}>
            <div {...clickableNodeProps(() => onClick?.(node))} className={`${ACTIVITY_NODE_BASE_STYLES.CONTAINER_HEADER} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""} z-20`} style={{ width: `${WORKFLOW_WIDTH}px` }}>
                <div className="flex items-center justify-between gap-4 px-4 py-2">
                    <div className="flex min-w-0 items-center gap-2">
                        <Workflow className="h-4 w-4 flex-shrink-0 text-(--brand-wMain)" />
                        <div className="truncate text-sm font-semibold">{node.data.label}</div>
                    </div>

                    {isExpanded && onCollapse && (
                        <div className="opacity-0 transition-opacity group-hover:opacity-100">
                            <Button
                                onClick={e => {
                                    e.stopPropagation();
                                    onCollapse(node.id);
                                }}
                                variant="ghost"
                                size="icon"
                                className="h-8 w-8"
                                tooltip="Collapse"
                            >
                                <Minimize2 className="h-4 w-4" />
                            </Button>
                        </div>
                    )}
                </div>
            </div>

            <div ref={containerRef} className={`relative border-2 ${CONTAINER_CHILDREN_CLASSES}`} style={{ marginTop: `-${HEADER_HEIGHT / 2}px`, paddingTop: `${HEADER_HEIGHT / 2 + 16}px` }}>
                {bezierPaths.length > 0 && (
                    <svg data-testid="workflowConnectors" className="pointer-events-none absolute inset-0 z-0" style={{ width: "100%", height: "100%", overflow: "visible" }}>
                        {bezierPaths.map(path => (
                            <path key={path.id} d={path.d} strokeWidth={2} fill="none" className={CONNECTOR_STROKE_CLASSES} />
                        ))}
                    </svg>
                )}

                <div className="relative z-10 flex flex-col items-center px-3 pb-4">
                    {node.children.map((child, index) => {
                        const precedingNode = index > 0 ? node.children[index - 1] : null;
                        const precedingNodeId = precedingNode?.id;
                        const followingNode = index < node.children.length - 1 ? node.children[index + 1] : null;
                        const followingNodeId = followingNode?.id;

                        return (
                            <Fragment key={child.id}>
                                {renderChild(child, precedingNodeId, followingNodeId)}
                                {!dependencyEdges && index < node.children.length - 1 && child.type !== "parallelBlock" && node.children[index + 1].type !== "parallelBlock" && (
                                    <div className={`my-0 ${CONNECTOR_SIZES.MAIN} ${CONNECTOR_LINE_CLASSES}`} />
                                )}
                            </Fragment>
                        );
                    })}
                </div>
            </div>
        </div>
    );
};

export default WorkflowGroup;
