// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Maximize2, Minimize2, Repeat2 } from "lucide-react";
import type { FC, MouseEvent, ReactNode } from "react";

import { NODE_COLORS } from "@/features/activities/nodeColors";

import { Button } from "@/ui";

import { LAYOUT_CONSTANTS, NODE_BASE_STYLES, NODE_HIGHLIGHT_CLASSES, NODE_SELECTED_CLASS, type NodeProps } from "../utils/types";

const { NODE_HEIGHTS } = LAYOUT_CONSTANTS;

interface MapNodeProps extends NodeProps {
    renderChildren?: (children: NodeProps["node"]["children"]) => ReactNode;
}

const MapNode: FC<MapNodeProps> = ({ node, isSelected, isHighlighted, onClick, onExpand, onCollapse, renderChildren }) => {
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
                    <Repeat2 className={`h-4 w-4 ${NODE_COLORS.map}`} />
                    <span className={`text-sm font-semibold ${NODE_COLORS.mapLabel}`}>Map</span>
                </div>

                {canHaveChildren && (
                    <Button onClick={handleToggle} variant="ghost" size="icon" className="h-8 w-8" tooltip="Expand">
                        <Maximize2 className="h-4 w-4" />
                    </Button>
                )}
            </div>
        );
    }

    return (
        <div
            className="relative"
            style={{
                width: `${node.width}px`,
                height: `${node.height}px`,
            }}
        >
            <div className="absolute inset-0 rounded border-1 border-dashed bg-(--secondary-w10)" style={{ top: `${NODE_HEIGHTS.CONTAINER_HEADER / 2}px` }}>
                <div className="px-3 pt-12 pb-4">
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
                        <Repeat2 className="h-4 w-4 text-(--accent-n0-wMain)" />
                        <span className="text-sm font-semibold">Map</span>
                    </div>

                    <Button onClick={handleToggle} variant="ghost" size="icon" className="h-8 w-8" tooltip="Collapse">
                        <Minimize2 className="h-4 w-4" />
                    </Button>
                </div>
            </div>
        </div>
    );
};

export default MapNode;
