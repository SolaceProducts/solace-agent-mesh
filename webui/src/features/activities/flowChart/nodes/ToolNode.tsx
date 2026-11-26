// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Wrench } from "lucide-react";
import type { FC } from "react";

import { cn } from "@/utils/cnTailwind";
import { clickableNodeProps } from "@/utils/nodeInteraction";

import { Badge } from "@/ui";

import { ACTIVITY_NODE_BASE_STYLES, ACTIVITY_NODE_ERROR_CLASS, ACTIVITY_NODE_PROCESSING_CLASS, ACTIVITY_NODE_SELECTED_CLASS, ACTIVITY_NODE_SKIPPED_CLASS } from "../utils/nodeStyles";
import type { LayoutNode } from "../utils/types";

interface ToolNodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    onClick?: (node: LayoutNode) => void;
}

const ToolNode: FC<ToolNodeProps> = ({ node, isSelected, onClick }) => {
    const isProcessing = node.data.status === "in-progress";
    const isError = node.data.status === "error";
    const isSkipped = !!node.data.isSkipped;
    const artifactCount = node.data.createdArtifacts?.length || 0;

    return (
        <div
            {...clickableNodeProps(() => onClick?.(node))}
            data-testid={`tool-node-${node.data.label}`}
            data-skipped={isSkipped || undefined}
            data-status={node.data.status}
            className={cn(
                ACTIVITY_NODE_BASE_STYLES.CONTAINER_HEADER,
                "flex flex-col justify-center",
                isError && ACTIVITY_NODE_ERROR_CLASS,
                isSkipped && ACTIVITY_NODE_SKIPPED_CLASS,
                isSelected && ACTIVITY_NODE_SELECTED_CLASS,
                isProcessing && ACTIVITY_NODE_PROCESSING_CLASS
            )}
            style={{ width: "225px", minHeight: "50px" }}
        >
            <div className="flex items-center gap-2 px-4 py-2">
                <Wrench className={cn("h-4 w-4 flex-shrink-0", isError ? "text-(--error-wMain)" : "text-(--accent-n7-wMain)")} />
                <div className="min-w-0">
                    <div className="truncate text-sm font-semibold">{node.data.label}</div>
                    {node.data.toolName && node.data.toolName !== node.data.label && <div className="truncate text-xs text-(--secondary-text-wMain)">{node.data.toolName}</div>}
                    {isSkipped && (
                        <div className="text-xs text-(--secondary-text-wMain)" title="This step did not run. Select it to see why.">
                            Skipped
                        </div>
                    )}
                </div>
            </div>
            {artifactCount > 0 && (
                <div className="px-4 pt-0 pb-2 pl-10">
                    <Badge variant="default">
                        {artifactCount} {artifactCount === 1 ? "artifact" : "artifacts"}
                    </Badge>
                </div>
            )}
        </div>
    );
};

export default ToolNode;
