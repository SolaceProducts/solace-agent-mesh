// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { NodeProps } from "@xyflow/react";
import { Handle, Position } from "@xyflow/react";
import { Bot, Globe, Loader2, Workflow } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { TruncatedText } from "@/ui/truncated-text";

import { humanizeComponentName } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { StatusBadge, typeColor } from "./CardShell";
import { lookupConnectorIconByKey } from "./connectorIconInference";

interface AgentEntry {
    name: string;
    status?: string;
    origin?: string;
    isGenerating?: boolean;
    connectorIcons?: Array<{ name: string; icon?: string }>;
}

interface GatewayEntry {
    name: string;
    status?: string;
    origin?: string;
}

export interface WorkflowContainerData {
    component: NonNullable<BuildManifest["components"]>[number];
    canvasData: ComponentCanvasData | null;
    agents: AgentEntry[];
    gateway?: GatewayEntry | null;
}

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

function WorkflowContainerNodeInner({ data, selected }: NodeProps) {
    const { component, canvasData, agents, gateway } = data as unknown as WorkflowContainerData;
    const isGenerating = canvasData?.isGenerating ?? false;
    const isQueued = canvasData?.isQueued ?? false;
    const wfColor = typeColor("workflow");

    return (
        <div className="relative">
            <Handle id="top" type="target" position={Position.Top} style={HANDLE_STYLE} />

            <div className={cn("card-surface rounded transition-shadow", selected ? "ring-2 ring-(--accent-n2-wMain)/40" : "")} style={{ backgroundColor: "var(--background-w10)" }}>
                <div className="flex items-center gap-2.5 px-4 py-3">
                    <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg bg-(--accent-n1-wMain)/15">
                        <Workflow className="h-3.5 w-3.5" style={{ color: wfColor }} />
                    </div>
                    <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                            <TruncatedText text={component.name} className="text-sm font-semibold">
                                {humanizeComponentName(component.name)}
                            </TruncatedText>
                            <StatusBadge status={component.origin === "platform" ? "deployed" : component.status} />
                        </div>
                    </div>
                    {(isGenerating || isQueued) && <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-(--brand-wMain)" />}
                </div>

                {gateway && (
                    <div className="border-t px-3 py-2">
                        <div className="flex items-center gap-2 rounded-lg bg-(--background-w20) px-3 py-2">
                            <Globe className="h-3.5 w-3.5 shrink-0" style={{ color: typeColor("gateway") }} />
                            <TruncatedText text={gateway.name} className="text-xs font-medium" />
                            <span className="ml-auto shrink-0 text-[10px]">Entrypoint</span>
                        </div>
                    </div>
                )}

                {agents.length > 0 && (
                    <div className={cn("px-3 pb-3", gateway ? "pt-1" : "border-t pt-2")}>
                        <div className="space-y-1.5">
                            {agents.map(agent => (
                                <div key={agent.name} className="flex items-center gap-2 rounded-lg bg-(--background-w20) px-3 py-2">
                                    <Bot className="h-3.5 w-3.5 shrink-0" style={{ color: typeColor("agent") }} />
                                    <TruncatedText text={agent.name} className="text-xs font-medium" />
                                    {agent.connectorIcons && agent.connectorIcons.length > 0 && (
                                        <div className="ml-auto flex shrink-0 items-center gap-1">
                                            {agent.connectorIcons.slice(0, 3).map(ic => {
                                                const iconUrl = ic.icon && ic.icon !== "other" ? lookupConnectorIconByKey(ic.icon) : undefined;
                                                return iconUrl ? <img key={ic.name} src={iconUrl} alt={ic.name} className="h-3 w-3 object-contain" /> : null;
                                            })}
                                        </div>
                                    )}
                                    <span className="ml-auto shrink-0 text-[10px]">Agent</span>
                                </div>
                            ))}
                        </div>
                    </div>
                )}
            </div>

            <Handle id="bottom" type="source" position={Position.Bottom} style={HANDLE_STYLE} />
        </div>
    );
}

export const WorkflowContainerNode = memo(WorkflowContainerNodeInner);
