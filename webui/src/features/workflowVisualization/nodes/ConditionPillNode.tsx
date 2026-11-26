// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback } from "react";

import { clickableNodeProps } from "@/utils/nodeInteraction";

import { cn } from "@/utils";

import { getValidNodeReferences } from "../utils/expressionParser";
import { NODE_BASE_STYLES, NODE_SELECTED_CLASS_COMPACT, type NodeProps } from "../utils/types";

const ConditionPillNode: React.FC<NodeProps> = ({ node, isSelected, onClick, onHighlightNodes, knownNodeIds }) => {
    const isDefault = node.data.isDefaultCase;
    const label = node.data.conditionLabel || node.data.label;
    const caseNumber = node.data.caseNumber;
    const isEscalate = node.data.branchKind === "escalate";

    const handleMouseEnter = useCallback(() => {
        if (!onHighlightNodes || !knownNodeIds || !label) return;
        const nodeRefs = getValidNodeReferences(label, knownNodeIds);
        if (nodeRefs.length > 0) {
            onHighlightNodes(nodeRefs);
        }
    }, [label, onHighlightNodes, knownNodeIds]);

    const handleMouseLeave = useCallback(() => {
        onHighlightNodes?.([]);
    }, [onHighlightNodes]);

    const displayText = isDefault ? "Default" : label;
    const fullText = isDefault ? "Default" : [caseNumber, label].filter(Boolean).join(" ");

    return (
        <div
            className={cn(
                NODE_BASE_STYLES.CONDITION_PILL,
                isEscalate ? "border border-(--warning-wMain) bg-(--warning-w10) text-(--warning-wMain)" : "border border-(--secondary-w20) bg-(--background-w10) text-(--secondary-text-wMain)",
                isSelected && NODE_SELECTED_CLASS_COMPACT
            )}
            style={{
                width: isDefault ? "auto" : `${node.width}px`,
                height: `${node.height}px`,
            }}
            {...(onClick ? clickableNodeProps(() => onClick(node)) : {})}
            onMouseEnter={handleMouseEnter}
            onMouseLeave={handleMouseLeave}
            title={fullText}
        >
            {!isDefault && caseNumber && (
                <>
                    <span className="flex-shrink-0 font-medium">{caseNumber}</span>
                    <div className="h-4 w-px bg-(--secondary-w20)" />
                </>
            )}
            <span className={cn("block flex-1 overflow-hidden text-ellipsis whitespace-nowrap", isEscalate ? "text-(--warning-wMain)" : "text-(--secondary-text-wMain)")}>{displayText}</span>
        </div>
    );
};

export default ConditionPillNode;
