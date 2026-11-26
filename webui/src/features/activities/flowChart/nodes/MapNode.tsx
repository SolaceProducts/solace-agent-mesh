// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FC, Fragment, useMemo } from "react";
import { Repeat2 } from "lucide-react";

import { clickableNodeProps } from "@/utils/nodeInteraction";

import { NODE_COLORS } from "@/features/activities/nodeColors";

import { ACTIVITY_NODE_BASE_STYLES, ACTIVITY_NODE_LAYOUT, ACTIVITY_NODE_SELECTED_CLASS, CONNECTOR_LINE_CLASSES, CONNECTOR_SIZES, CONTAINER_CHILDREN_CLASSES } from "../utils/nodeStyles";
import type { LayoutNode } from "../utils/types";

import AgentNode from "./AgentNode";

interface MapNodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    onClick?: (node: LayoutNode) => void;
    onChildClick?: (child: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
}

const MapNode: FC<MapNodeProps> = ({ node, isSelected, onClick, onChildClick, onExpand, onCollapse }) => {
    const HEADER_HEIGHT = ACTIVITY_NODE_LAYOUT.HEADER_HEIGHT;

    const branches = useMemo(() => {
        const branchMap = new Map<number, typeof node.children>();
        for (const child of node.children) {
            const iterationIndex = child.data.iterationIndex ?? 0;
            if (!branchMap.has(iterationIndex)) {
                branchMap.set(iterationIndex, []);
            }
            branchMap.get(iterationIndex)!.push(child);
        }
        return Array.from(branchMap.entries())
            .sort((a, b) => a[0] - b[0])
            .map(([, children]) => children);
    }, [node]);

    const hasChildren = branches.length > 0;
    const label = "Map";

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

    if (hasChildren) {
        return (
            <div className="flex flex-col px-4" style={{ width: "fit-content", minWidth: "200px" }}>
                <div {...clickableNodeProps(() => onClick?.(node))} className={`${ACTIVITY_NODE_BASE_STYLES.CONTAINER_HEADER} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""}`}>
                    <div className="flex items-center gap-2 px-4 py-2">
                        <Repeat2 className="h-4 w-4 text-(--accent-n0-wMain)" />
                        <span className="text-sm font-semibold">{node.data.label || label}</span>
                    </div>
                </div>

                <div className={CONTAINER_CHILDREN_CLASSES} style={{ marginTop: `-${HEADER_HEIGHT / 2}px`, paddingTop: `${HEADER_HEIGHT / 2 + 16}px` }}>
                    <div className="px-3 pb-4">
                        <div className="grid gap-4" style={{ gridAutoFlow: "column", gridAutoColumns: "1fr" }}>
                            {branches.map((branch, branchIndex) => (
                                <div key={`branch-${branchIndex}`} className="flex flex-col items-center">
                                    {branch.map((child, childIndex) => (
                                        <Fragment key={child.id}>
                                            {renderChild(child)}
                                            {childIndex < branch.length - 1 && <div className={`my-1 ${CONNECTOR_SIZES.MAIN} ${CONNECTOR_LINE_CLASSES}`} />}
                                        </Fragment>
                                    ))}
                                </div>
                            ))}
                        </div>
                    </div>
                </div>
            </div>
        );
    }

    return (
        <div {...clickableNodeProps(() => onClick?.(node))} className={`${ACTIVITY_NODE_BASE_STYLES.RECTANGULAR} ${isSelected ? ACTIVITY_NODE_SELECTED_CLASS : ""}`} style={{ width: "fit-content", minWidth: "120px" }}>
            <div className="flex items-center gap-2">
                <Repeat2 className={`h-4 w-4 ${NODE_COLORS.map}`} />
                <span className={`text-sm font-semibold ${NODE_COLORS.mapLabel}`}>{node.data.label || label}</span>
            </div>
        </div>
    );
};

export default MapNode;
