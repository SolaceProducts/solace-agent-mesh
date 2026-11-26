// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot } from "lucide-react";
import type { FC } from "react";

import { TruncatedText } from "@/ui/truncated-text";

import { NODE_BASE_STYLES, NODE_HIGHLIGHT_CLASSES, NODE_SELECTED_CLASS, type NodeProps } from "../utils/types";

const AgentNode: FC<NodeProps> = ({ node, isSelected, isHighlighted, onClick }) => {
    const agentName = node.data.agentName || node.data.label;
    const icons = node.data.icons as Array<{ name: string; url?: string }> | undefined;

    return (
        <div
            className={`${NODE_BASE_STYLES.RECTANGULAR} ${isSelected ? NODE_SELECTED_CLASS : ""} ${isHighlighted ? NODE_HIGHLIGHT_CLASSES : ""}`}
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
            <div className="flex items-center gap-2 overflow-hidden">
                <Bot className="h-5 w-5 flex-shrink-0 text-(--brand-wMain)" />
                <TruncatedText text={agentName} className="text-sm font-semibold" />
            </div>
            <div className="ml-2 flex flex-shrink-0 items-center gap-1">
                {icons && icons.length > 0 && (
                    <div className="flex items-center">
                        {icons.slice(0, 3).map((ic, i) => (
                            <div key={ic.name} className="flex h-5 w-5 items-center justify-center" style={{ marginLeft: i === 0 ? 0 : -3 }}>
                                {ic.url ? <img src={ic.url} alt={ic.name} className="h-3.5 w-3.5 object-contain" /> : null}
                            </div>
                        ))}
                        {icons.length > 3 && <span className="text-[9px] text-(--secondary-text-wMain)">+{icons.length - 3}</span>}
                    </div>
                )}
                <span className="rounded px-2 py-0.5 text-sm font-medium text-(--secondary-text-wMain)">Agent</span>
            </div>
        </div>
    );
};

export default AgentNode;
