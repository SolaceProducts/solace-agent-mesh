// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Wrench } from "lucide-react";
import type { FC } from "react";

import { TruncatedText } from "@/ui/truncated-text";

import { toolNodeLabel } from "../utils/toolName";
import { NODE_BASE_STYLES, NODE_HIGHLIGHT_CLASSES, NODE_SELECTED_CLASS, type NodeProps } from "../utils/types";

const ToolNode: FC<NodeProps> = ({ node, isSelected, isHighlighted, onClick }) => {
    const toolName = toolNodeLabel(node.data, node.data.label ?? "");
    const className = `${NODE_BASE_STYLES.RECTANGULAR} ${isSelected ? NODE_SELECTED_CLASS : ""} ${isHighlighted ? NODE_HIGHLIGHT_CLASSES : ""}`;
    const style = { width: `${node.width}px`, height: `${node.height}px` };

    const content = (
        <>
            <div className="flex items-center gap-2 overflow-hidden">
                <Wrench className="h-5 w-5 flex-shrink-0 text-(--brand-wMain)" />
                <TruncatedText text={toolName} className="text-sm font-semibold" />
            </div>
            <span className="ml-2 flex-shrink-0 rounded px-2 py-0.5 text-sm font-medium text-(--secondary-text-wMain)">Tool</span>
        </>
    );

    if (onClick) {
        return (
            <button
                type="button"
                className={`${className} w-full text-left`}
                style={style}
                onClick={e => {
                    e.stopPropagation();
                    onClick(node);
                }}
            >
                {content}
            </button>
        );
    }

    return (
        <div className={className} style={style}>
            {content}
        </div>
    );
};

export default ToolNode;
