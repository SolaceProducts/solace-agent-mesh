// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, type ReactNode, useCallback } from "react";
import { Bot, Maximize2, Minimize2 } from "lucide-react";

import { cn } from "@/utils/cnTailwind";
import { clickableNodeProps } from "@/utils/nodeInteraction";

import { Button } from "@/ui";

import {
    ACTIVITY_NODE_BASE_STYLES,
    ACTIVITY_NODE_ERROR_CLASS,
    ACTIVITY_NODE_LAYOUT,
    ACTIVITY_NODE_PROCESSING_CLASS,
    ACTIVITY_NODE_SELECTED_CLASS,
    CONNECTOR_LINE_CLASSES,
    CONNECTOR_SIZES,
    CONTAINER_CHILDREN_CLASSES,
    PARALLEL_WRAP_COLUMNS,
} from "../utils/nodeStyles";
import type { LayoutNode } from "../utils/types";

import DecisionNode from "./DecisionNode";
import LLMNode from "./LLMNode";
import LoopNode from "./LoopNode";
import SwitchNode from "./SwitchNode";
import ToolNode from "./ToolNode";
import WorkflowGroup from "./WorkflowGroup";

interface AgentNodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    onClick?: (node: LayoutNode) => void;
    onChildClick?: (child: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
}

type ChildHandlers = Pick<AgentNodeProps, "onChildClick" | "onExpand" | "onCollapse">;
type RenderChild = (child: LayoutNode) => ReactNode;

const AgentHeader = ({ label }: { label: string }) => (
    <div className="flex min-w-0 items-center gap-2">
        <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full bg-(--accent-n2-w10)">
            <Bot className="h-4 w-4 text-(--brand-wMain)" />
        </div>
        <div className="truncate text-sm font-semibold">{label}</div>
    </div>
);

const Connector = () => <div className={`my-0 ${CONNECTOR_SIZES.MAIN} ${CONNECTOR_LINE_CLASSES}`} />;

const ConnectedChildren = ({ nodes, renderChild }: { nodes: LayoutNode[]; renderChild: RenderChild }) => (
    <>
        {nodes.map((child, index) => (
            <Fragment key={child.id}>
                {renderChild(child)}
                {index < nodes.length - 1 && <Connector />}
            </Fragment>
        ))}
    </>
);

const BranchGrid = ({ branches, renderChild }: { branches: LayoutNode[][]; renderChild: RenderChild }) => (
    <div className="grid gap-4" style={{ gridAutoFlow: "column", gridAutoColumns: "1fr" }}>
        {branches.map(branch => (
            <div key={branch.map(child => child.id).join(",")} className="flex flex-col items-center">
                <ConnectedChildren nodes={branch} renderChild={renderChild} />
            </div>
        ))}
    </div>
);

const renderActivityChild = (child: LayoutNode, { onChildClick, onExpand, onCollapse }: ChildHandlers): ReactNode => {
    const childProps = {
        node: child,
        onClick: onChildClick,
        onExpand,
        onCollapse,
    };

    switch (child.type) {
        case "agent":
            return <AgentNode key={child.id} {...childProps} onChildClick={onChildClick} />;
        case "llm":
            return <LLMNode key={child.id} {...childProps} />;
        case "tool":
            return <ToolNode key={child.id} {...childProps} />;
        case "switch":
            return <SwitchNode key={child.id} {...childProps} />;
        case "decision":
            return <DecisionNode key={child.id} {...childProps} />;
        case "loop":
            return <LoopNode key={child.id} {...childProps} />;
        case "group":
            return <WorkflowGroup key={child.id} {...childProps} onChildClick={onChildClick} />;
        case "parallelBlock": {
            if (child.children.length === 0) {
                return null;
            }
            const rows: LayoutNode[][] = [];
            for (let i = 0; i < child.children.length; i += PARALLEL_WRAP_COLUMNS) {
                rows.push(child.children.slice(i, i + PARALLEL_WRAP_COLUMNS));
            }
            return (
                <div key={child.id} className={`flex flex-col items-center gap-4 p-4 ${CONTAINER_CHILDREN_CLASSES}`}>
                    {rows.map((row, rowIndex) => (
                        <div key={rowIndex} className="flex flex-row items-start gap-4">
                            {row.map(parallelChild => renderActivityChild(parallelChild, { onChildClick, onExpand, onCollapse }))}
                        </div>
                    ))}
                </div>
            );
        }
        default:
            return null;
    }
};

interface PillAgentNodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    onClick?: (node: LayoutNode) => void;
    renderChild: RenderChild;
}

const PillAgentNode = ({ node, isSelected, onClick, renderChild }: PillAgentNodeProps) => {
    const opacityClass = node.data.isSkipped ? "opacity-50" : "";
    const hasParallelBranches = node.parallelBranches && node.parallelBranches.length > 0;
    const hasChildren = node.children && node.children.length > 0;
    const isError = node.data.status === "error";

    const pillColorClasses = isError ? "border-(--error-wMain) bg-(--error-w10) text-(--error-wMain)" : "border-(--info-wMain) bg-(--info-w10) text-(--info-wMain)";

    if (!hasParallelBranches && !hasChildren) {
        return (
            <div
                className={cn(ACTIVITY_NODE_BASE_STYLES.PILL, pillColorClasses, isSelected && ACTIVITY_NODE_SELECTED_CLASS)}
                style={{
                    width: `${node.width}px`,
                    minWidth: "80px",
                    textAlign: "center",
                }}
                title={node.data.description}
            >
                <div className="flex items-center justify-center">
                    <div className="text-sm font-bold">{node.data.label}</div>
                </div>
            </div>
        );
    }

    const pillLabel = (
        <div
            {...clickableNodeProps(() => onClick?.(node))}
            className={`${ACTIVITY_NODE_BASE_STYLES.PILL} ${pillColorClasses} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""}`}
            style={{
                minWidth: "80px",
                textAlign: "center",
            }}
            title={node.data.description}
        >
            <div className="flex items-center justify-center">
                <div className="text-sm font-bold">{node.data.label}</div>
            </div>
        </div>
    );

    if (hasChildren && !hasParallelBranches) {
        return (
            <div className={`flex flex-col items-center ${opacityClass}`}>
                {pillLabel}
                <Connector />
                <ConnectedChildren nodes={node.children} renderChild={renderChild} />
            </div>
        );
    }

    return (
        <div className={`flex flex-col items-center ${opacityClass}`}>
            {pillLabel}
            <Connector />
            <div className={`rounded-md border-2 border-(--secondary-w20) bg-(--background-w20) p-4`}>
                <BranchGrid branches={node.parallelBranches!} renderChild={renderChild} />
            </div>
        </div>
    );
};

interface CollapsedAgentNodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    haloClass: string;
    onClick?: (node: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
}

const CollapsedAgentNode = ({ node, isSelected, haloClass, onClick, onExpand }: CollapsedAgentNodeProps) => {
    const opacityClass = node.data.isSkipped ? "opacity-50" : "";
    const borderStyleClass = node.data.isSkipped ? "border-dashed" : "border-solid";
    const errorClass = node.data.status === "error" ? ACTIVITY_NODE_ERROR_CLASS : "";

    return (
        <div
            {...clickableNodeProps(() => onClick?.(node))}
            data-skipped={node.data.isSkipped || undefined}
            data-status={node.data.status}
            className={`${ACTIVITY_NODE_BASE_STYLES.RECTANGULAR} ${opacityClass} ${borderStyleClass} ${errorClass} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""} ${haloClass}`}
            style={{ minWidth: `${ACTIVITY_NODE_LAYOUT.CONTAINER_WIDTH}px`, minHeight: `${ACTIVITY_NODE_LAYOUT.LEAF_NODE_MIN_HEIGHT}px` }}
        >
            <AgentHeader label={node.data.label} />
            {node.data.isSkipped && (
                <span className="text-xs text-(--secondary-text-wMain)" title="This step did not run. Select it to see why.">
                    Skipped
                </span>
            )}
            {node.data.isCollapsed && onExpand && (
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
};

const AgentNode = ({ node, isSelected, onClick, onChildClick, onExpand, onCollapse }: AgentNodeProps) => {
    const renderChild = useCallback((child: LayoutNode) => renderActivityChild(child, { onChildClick, onExpand, onCollapse }), [onChildClick, onExpand, onCollapse]);

    if (node.data.variant === "pill") {
        return <PillAgentNode node={node} isSelected={isSelected} onClick={onClick} renderChild={renderChild} />;
    }

    const opacityClass = node.data.isSkipped ? "opacity-50" : "";
    const isProcessing = node.data.status === "in-progress" || node.data.hasProcessingChildren;

    const haloClass = isProcessing ? ACTIVITY_NODE_PROCESSING_CLASS : "";

    const isExpanded = node.data.isExpanded;

    const hasChildren = node.children && node.children.length > 0;
    const hasParallelBranches = node.parallelBranches && node.parallelBranches.length > 0;
    const hasContent = hasChildren || hasParallelBranches;

    if (node.data.isCollapsed || !hasContent) {
        return <CollapsedAgentNode node={node} isSelected={isSelected} haloClass={haloClass} onClick={onClick} onExpand={onExpand} />;
    }

    return (
        <div className={`flex flex-col ${opacityClass} ${haloClass}`}>
            <div className={`card-surface rounded bg-(--background-w10) transition-all duration-200 ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""}`} style={{ minWidth: `${ACTIVITY_NODE_LAYOUT.CONTAINER_WIDTH}px` }}>
                <div {...clickableNodeProps(() => onClick?.(node))} className="group flex cursor-pointer items-center justify-between gap-4 px-4 py-2">
                    <AgentHeader label={node.data.label} />

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

                <div className="border-t border-(--secondary-w20)" />

                <div className="bg-(--secondary-w20) p-4">
                    <div className="flex flex-col items-center gap-2">
                        {hasChildren && <ConnectedChildren nodes={node.children} renderChild={renderChild} />}

                        {hasParallelBranches && node.parallelBranches && (
                            <div className="mt-2 w-full rounded-md border-2 border-(--brand-w20) bg-(--background-w10) p-4">
                                <BranchGrid branches={node.parallelBranches} renderChild={renderChild} />
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </div>
    );
};

export default AgentNode;
