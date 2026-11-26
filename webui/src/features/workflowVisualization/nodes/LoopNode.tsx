// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Maximize2, Minimize2, RefreshCw } from "lucide-react";
import type { FC, MouseEvent, ReactNode } from "react";

import { NODE_COLORS } from "@/features/activities/nodeColors";

import { Button } from "@/ui";

import { LAYOUT_CONSTANTS, NODE_BASE_STYLES, NODE_HIGHLIGHT_CLASSES, NODE_SELECTED_CLASS, type NodeProps } from "../utils/types";

const { NODE_HEIGHTS } = LAYOUT_CONSTANTS;

interface LoopNodeProps extends NodeProps {
    renderChildren?: (children: NodeProps["node"]["children"]) => ReactNode;
}

const LoopNode: FC<LoopNodeProps> = ({ node, isSelected, isHighlighted, onClick, onExpand, onCollapse, renderChildren }) => {
    const isCollapsed = node.isCollapsed;
    const hasChildren = node.children && node.children.length > 0;
    const canHaveChildren = hasChildren || !!node.data.childNodeId;

    const handleToggle = (e: MouseEvent) => {
        e.stopPropagation();
        if (isCollapsed) {
            onExpand?.(node.id);
        } else {
            onCollapse?.(node.id);
        }
    };

    const formatCondition = (condition?: string) => {
        if (!condition) return null;
        const maxLen = 30;
        return condition.length > maxLen ? `${condition.slice(0, maxLen)}...` : condition;
    };

    const hasConditionRow = node.data.condition || node.data.maxIterations;

    if (isCollapsed || !hasChildren) {
        return (
            <div
                className={`${NODE_BASE_STYLES.RECTANGULAR_COMPACT} ${isSelected ? NODE_SELECTED_CLASS : ""} ${isHighlighted ? NODE_HIGHLIGHT_CLASSES : ""}`}
                style={{
                    width: `${node.width}px`,
                    height: `${node.height}px`,
                }}
                onClick={
                    onClick
                        ? e => {
                              e.stopPropagation();
                              onClick(node);
                          }
                        : undefined
                }
            >
                <div className="flex items-center gap-2">
                    <RefreshCw className={`h-4 w-4 ${NODE_COLORS.loop}`} />
                    <span className="text-sm font-semibold">Loop</span>
                </div>

                <div className="flex items-center gap-2">
                    {node.data.maxIterations && <span className="text-sm text-(--secondary-text-wMain)">max: {node.data.maxIterations}</span>}
                    {canHaveChildren && (
                        <Button onClick={handleToggle} variant="ghost" size="icon" className="h-8 w-8" tooltip="Expand">
                            <Maximize2 className="h-4 w-4" />
                        </Button>
                    )}
                </div>
            </div>
        );
    }

    const totalHeaderHeightPx = NODE_HEIGHTS.CONTAINER_HEADER + (hasConditionRow ? NODE_HEIGHTS.LOOP_CONDITION_ROW : 0);

    return (
        <div
            className="relative"
            style={{
                width: `${node.width}px`,
                height: `${node.height}px`,
            }}
        >
            <div className="absolute inset-0 rounded border-1 border-dashed border-(--secondary-w40) bg-(--secondary-w10)" style={{ top: `${totalHeaderHeightPx / 2}px` }}>
                <div className={`px-3 pb-4 ${hasConditionRow ? "pt-16" : "pt-12"}`}>
                    <div className="flex flex-col items-center gap-2">{renderChildren ? renderChildren(node.children) : null}</div>
                </div>
            </div>

            <div
                className={`${NODE_BASE_STYLES.CONTAINER_HEADER} ${isSelected ? NODE_SELECTED_CLASS : ""} ${isHighlighted ? NODE_HIGHLIGHT_CLASSES : ""}`}
                onClick={
                    onClick
                        ? e => {
                              e.stopPropagation();
                              onClick(node);
                          }
                        : undefined
                }
            >
                <div className="flex items-center justify-between gap-4 px-4 py-2">
                    <div className="flex items-center gap-2">
                        <RefreshCw className={`h-4 w-4 ${NODE_COLORS.loop}`} />
                        <span className="text-sm font-semibold">Loop</span>
                    </div>

                    <div className="flex items-center gap-2">
                        {node.data.maxIterations && <span className="text-sm text-(--secondary-text-wMain)">max: {node.data.maxIterations}</span>}
                        <Button onClick={handleToggle} variant="ghost" size="icon" className="h-8 w-8" tooltip="Collapse">
                            <Minimize2 className="h-4 w-4" />
                        </Button>
                    </div>
                </div>

                {hasConditionRow && (
                    <div className="px-4 pt-0 pb-3">
                        <span className="block truncate rounded bg-(--secondary-w10) px-2 py-1 text-sm" title={node.data.condition}>
                            while: {formatCondition(node.data.condition)}
                        </span>
                    </div>
                )}
            </div>
        </div>
    );
};

export default LoopNode;
