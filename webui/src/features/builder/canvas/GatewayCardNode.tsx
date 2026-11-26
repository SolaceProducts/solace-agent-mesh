// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { NodeProps } from "@xyflow/react";
import { Handle, Position } from "@xyflow/react";
import { Bot, Globe } from "lucide-react";

import { TruncatedText } from "@/ui/truncated-text";

import { humanizeComponentName } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { CardShell } from "./CardShell";
import { useDerivedCardStatus } from "./useDerivedCardStatus";

interface GatewayCardData {
    component: NonNullable<BuildManifest["components"]>[number];
    canvasData: ComponentCanvasData | null;
    isSingleGateway?: boolean;
    compact?: boolean;
}

export function gatewayPropsEq(prev: NodeProps, next: NodeProps): boolean {
    const a = prev.data as unknown as GatewayCardData;
    const b = next.data as unknown as GatewayCardData;
    return prev.selected === next.selected && a.component === b.component && a.canvasData === b.canvasData && a.isSingleGateway === b.isSingleGateway && a.compact === b.compact;
}

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

function GatewayCardNodeInner({ data, selected }: NodeProps) {
    const { component, canvasData, isSingleGateway, compact } = data as unknown as GatewayCardData;
    const metadata = canvasData?.gatewayMetadata;
    const { isGenerating, isEditing, showsQueued, isActive, isReady, showsDraft, statusText, effectiveStatus } = useDerivedCardStatus(canvasData, component);
    const expanded = isSingleGateway === true;

    const targetAgents = metadata?.eventHandlers?.map(h => h.target_agent_name || h.target_workflow_name).filter(Boolean) ?? [];

    if (compact) {
        return (
            <>
                <Handle id="top" type="target" position={Position.Top} style={HANDLE_STYLE} />
                <CardShell selected={selected} generating={isGenerating} editing={isEditing} queued={showsQueued} ready={isReady} active={isActive} draft={showsDraft} error={effectiveStatus === "error"} statusText={statusText}>
                    <div className="flex h-10 items-center gap-2.5 px-4">
                        <Globe className="h-4 w-4 shrink-0" style={{ color: "var(--accent-n3-wMain)" }} />
                        <TruncatedText text={component.name} className="text-sm font-semibold">
                            {humanizeComponentName(component.name)}
                        </TruncatedText>
                        <span className="ml-auto shrink-0 text-xs">Entrypoint</span>
                    </div>
                </CardShell>
                <Handle id="bottom" type="source" position={Position.Bottom} style={HANDLE_STYLE} />
            </>
        );
    }

    return (
        <CardShell selected={selected} generating={isGenerating} editing={isEditing} queued={showsQueued} ready={isReady} active={isActive} draft={showsDraft} error={effectiveStatus === "error"} width={360} statusText={statusText}>
            <div className="px-4 py-3">
                <div className="flex items-center gap-2.5">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-(--accent-n3-wMain)/15">
                        <Globe className="h-4 w-4" style={{ color: "var(--accent-n3-wMain)" }} />
                    </div>
                    <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                            <TruncatedText text={component.name} className="text-sm font-semibold">
                                {humanizeComponentName(component.name)}
                            </TruncatedText>
                        </div>
                        <span className="text-[10px]">Entrypoint</span>
                    </div>
                </div>
                {expanded && component.description && <p className="mt-2 line-clamp-2 text-xs leading-relaxed">{component.description}</p>}
            </div>

            {expanded && targetAgents.length > 0 && (
                <div className="border-t px-4 py-2">
                    <div className="mb-1 text-[10px] font-medium tracking-wider uppercase">Targets ({metadata?.eventHandlers.length ?? 0} handlers)</div>
                    <div className="space-y-0.5">
                        {targetAgents.map((name, i) => (
                            <div key={`${name}-${i}`} className="flex items-center gap-1.5 text-xs">
                                <Bot className="h-3 w-3 shrink-0" />
                                <TruncatedText text={name ?? ""} />
                            </div>
                        ))}
                    </div>
                </div>
            )}

            <Handle id="target" type="target" position={Position.Left} style={HANDLE_STYLE} />
            <Handle id="source" type="source" position={Position.Right} style={HANDLE_STYLE} />
            <Handle id="top" type="target" position={Position.Top} style={HANDLE_STYLE} />
            <Handle id="bottom" type="source" position={Position.Bottom} style={HANDLE_STYLE} />
        </CardShell>
    );
}

export const GatewayCardNode = memo(GatewayCardNodeInner, gatewayPropsEq);
