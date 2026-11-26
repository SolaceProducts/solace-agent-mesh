// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FC, Fragment } from "react";
import { RefreshCw } from "lucide-react";

import { clickableNodeProps } from "@/utils/nodeInteraction";

import { NODE_COLORS } from "@/features/activities/nodeColors";

import { ACTIVITY_NODE_BASE_STYLES, ACTIVITY_NODE_LAYOUT, ACTIVITY_NODE_SELECTED_CLASS, CONNECTOR_LINE_CLASSES, CONNECTOR_SIZES, CONTAINER_CHILDREN_CLASSES } from "../utils/nodeStyles";
import type { LayoutNode } from "../utils/types";

import AgentNode from "./AgentNode";

interface LoopNodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    onClick?: (node: LayoutNode) => void;
    onChildClick?: (child: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
}

const LoopNode: FC<LoopNodeProps> = ({ node, isSelected, onClick, onChildClick, onExpand, onCollapse }) => {
    const currentIteration = node.data.currentIteration ?? 0;
    const maxIterations = node.data.maxIterations ?? 100;
    const hasChildren = node.children && node.children.length > 0;

    const LOOP_WIDTH = ACTIVITY_NODE_LAYOUT.CONTAINER_WIDTH;
    const HEADER_HEIGHT = ACTIVITY_NODE_LAYOUT.HEADER_HEIGHT;
    const MAX_CONDITION_LENGTH = 30;

    const renderChild = (child: LayoutNode) => {
        const childProps = {
            node: child,
            onClick: onChildClick,
            onChildClick: onChildClick,
            onExpand,
            onCollapse,
        };

        switch (child.type) {
            case "agent":
                return <AgentNode key={child.id} {...childProps} />;
            default:
                return null;
        }
    };

    const formatCondition = (condition?: string) => {
        if (!condition) return null;
        return condition.length > MAX_CONDITION_LENGTH ? `${condition.slice(0, MAX_CONDITION_LENGTH)}...` : condition;
    };

    if (hasChildren) {
        const hasConditionRow = node.data.condition || maxIterations;

        return (
            <div className="flex flex-col px-4" style={{ minWidth: `${LOOP_WIDTH + 72}px` }}>
                <div {...clickableNodeProps(() => onClick?.(node))} className={`${ACTIVITY_NODE_BASE_STYLES.CONTAINER_HEADER} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""}`} style={{ width: `${LOOP_WIDTH}px` }}>
                    <div className="flex items-center gap-4 px-4 py-2">
                        <div className="flex items-center gap-2">
                            <RefreshCw className="h-4 w-4 text-(--accent-n0-wMain)" />
                            <span className="text-sm font-semibold">{node.data.label || "Loop"}</span>
                        </div>
                        {maxIterations && <span className="text-sm text-(--secondary-text-wMain)">max: {maxIterations}</span>}
                    </div>

                    {hasConditionRow && node.data.condition && (
                        <div className="px-4 pt-0 pb-3">
                            <span className="block truncate rounded bg-(--secondary-w10) px-2 py-1 text-sm text-(--secondary-text-wMain)" title={node.data.condition}>
                                while: {formatCondition(node.data.condition)}
                            </span>
                        </div>
                    )}
                </div>

                <div className={CONTAINER_CHILDREN_CLASSES} style={{ marginTop: `-${HEADER_HEIGHT / 2}px`, paddingTop: `${HEADER_HEIGHT / 2 + (hasConditionRow ? 32 : 16)}px`, minWidth: `${LOOP_WIDTH + 72}px` }}>
                    <div className="px-3 pb-4">
                        <div className="flex flex-col items-center gap-2">
                            {node.children.map((child, index) => (
                                <Fragment key={child.id}>
                                    {renderChild(child)}
                                    {index < node.children.length - 1 && <div className={`my-1 ${CONNECTOR_SIZES.MAIN} ${CONNECTOR_LINE_CLASSES}`} />}
                                </Fragment>
                            ))}

                            {node.data.status === "in-progress" && currentIteration > node.children.length && (
                                <div className="mt-2 rounded bg-(--background-w10) px-2 py-1 text-[9px] font-medium text-(--secondary-text-wMain)">Processing iteration {currentIteration}...</div>
                            )}
                        </div>
                    </div>
                </div>
            </div>
        );
    }

    return (
        <div {...clickableNodeProps(() => onClick?.(node))} className={`${ACTIVITY_NODE_BASE_STYLES.RECTANGULAR} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""}`} style={{ width: "fit-content", minWidth: "120px" }}>
            <div className="flex items-center gap-2">
                <RefreshCw className={`h-4 w-4 ${NODE_COLORS.loop}`} />
                <span className="text-sm font-semibold">Loop</span>
            </div>
            {maxIterations && <span className="text-sm text-(--secondary-text-wMain)">max: {maxIterations}</span>}
        </div>
    );
};

export default LoopNode;
