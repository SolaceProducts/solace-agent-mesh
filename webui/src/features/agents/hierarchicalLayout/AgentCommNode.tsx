// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { Handle, type Node, type NodeProps, Position } from "@xyflow/react";
import { BotIcon } from "lucide-react";

import { type AgentNodeData } from "@/types/graph";

import { AgentNameDuplicateIcon } from "@/features/agents/AgentNameDuplicateIcon";
import { useDuplicateDisplayName } from "@/features/agents/hooks";

const AgentCommNode: React.FC<NodeProps<Node<AgentNodeData>>> = ({ data, selected }) => {
    const { hasDuplicateDisplayName } = useDuplicateDisplayName();
    const hasDuplicate = hasDuplicateDisplayName(data.agentName);
    return (
        <div
            className={`relative rounded-sm bg-(--background-w10) px-4 py-5 shadow-lg transition-all duration-200 hover:shadow-xl ${selected ? "shadow-2xl" : ""} `}
            style={{
                minWidth: "160px",
                maxWidth: "200px",
                boxShadow: selected
                    ? "0 20px 25px -5px rgba(0, 0, 0, 0.15), 0 10px 10px -5px rgba(0, 0, 0, 0.08), 0 0 0 1px var(--success-wMain)"
                    : "0 0 12px 2px color-mix(in srgb, var(--primary-w60), transparent 70%), 0 4px 6px -2px rgba(0, 0, 0, 0.1)",
            }}
        >
            {selected && <div className="absolute top-0 bottom-0 left-0 w-1 rounded-l-lg bg-(--success-wMain)" />}
            <Handle
                id="top"
                type="target"
                position={Position.Top}
                style={{
                    width: "1px",
                    height: "1px",
                    background: "transparent",
                    border: "none",
                    top: "0px",
                }}
                isConnectable={true}
            />
            <Handle
                id="bottom"
                type="source"
                position={Position.Bottom}
                style={{
                    width: "1px",
                    height: "1px",
                    background: "transparent",
                    border: "none",
                    bottom: "0px",
                }}
                isConnectable={true}
            />
            <Handle
                id="left"
                type="target"
                position={Position.Left}
                style={{
                    width: "1px",
                    height: "1px",
                    background: "transparent",
                    border: "none",
                    left: "0px",
                }}
                isConnectable={true}
            />
            <Handle
                id="right"
                type="source"
                position={Position.Right}
                style={{
                    width: "1px",
                    height: "1px",
                    background: "transparent",
                    border: "none",
                    right: "0px",
                }}
                isConnectable={true}
            />

            <div className="flex w-full flex-col items-center justify-center space-y-2">
                <BotIcon className="h-5 w-5 text-(--brand-w100)" />
                <div className="flex w-full items-center justify-center gap-1">
                    <div className="truncate text-center text-sm font-semibold">{data.label}</div>
                    <AgentNameDuplicateIcon show={hasDuplicate} className="size-3 shrink-0 text-(--warning-wMain)" />
                </div>
            </div>
        </div>
    );
};

export { AgentCommNode };
