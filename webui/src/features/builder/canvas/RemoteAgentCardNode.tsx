// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { NodeProps } from "@xyflow/react";
import { Handle, Position } from "@xyflow/react";
import { Globe } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { TruncatedText } from "@/ui/truncated-text";

import { humanizeComponentName } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";

import { CardShell } from "./CardShell";
import { useDerivedCardStatus } from "./useDerivedCardStatus";

interface RemoteAgentCardData {
    component: NonNullable<import("../hooks/useBuilderManifest").BuildManifest["components"]>[number];
    canvasData: ComponentCanvasData | null;
}

export function remoteAgentPropsEq(prev: NodeProps, next: NodeProps): boolean {
    const a = prev.data as unknown as RemoteAgentCardData;
    const b = next.data as unknown as RemoteAgentCardData;
    return prev.selected === next.selected && a.component === b.component && a.canvasData === b.canvasData;
}

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

const CARD_LOCATION_LABEL: Record<string, string> = {
    well_known: "well-known card",
    custom: "custom card URL",
    none: "no card (inline skills)",
};

function RemoteAgentCardNodeInner({ data, selected }: NodeProps) {
    const { component, canvasData } = data as unknown as RemoteAgentCardData;
    const metadata = canvasData?.a2aAgentMetadata;
    const { isGenerating, isEditing, showsQueued, isActive, isReady, showsDraft, statusText, effectiveStatus } = useDerivedCardStatus(canvasData, component);

    const agentUrl = metadata?.agentUrl;
    const authType = metadata?.agentAuthConfig?.type ?? "none";
    const cardLocation = metadata?.agentCardLocation ? (CARD_LOCATION_LABEL[metadata.agentCardLocation] ?? metadata.agentCardLocation) : undefined;

    return (
        <CardShell
            selected={selected}
            generating={isGenerating}
            editing={isEditing}
            queued={showsQueued}
            ready={isReady}
            active={isActive}
            draft={showsDraft}
            error={effectiveStatus === "error"}
            width={280}
            statusText={statusText}
            componentName={component.name}
            testEligible
        >
            <div className="px-4 py-3">
                <div className="flex items-center gap-2.5">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-(--accent-n2-wMain)/15">
                        <Globe className="h-4 w-4" style={{ color: "var(--accent-n2-wMain)" }} />
                    </div>
                    <div className="min-w-0 flex-1">
                        <TruncatedText text={component.name} className="block text-sm font-semibold">
                            {humanizeComponentName(component.name)}
                        </TruncatedText>
                        <span className="block text-[10px]">External A2A agent</span>
                    </div>
                </div>
                {(metadata?.description || component.description) && <p className={cn("mt-2 line-clamp-2 text-xs leading-relaxed")}>{metadata?.description || component.description}</p>}
                {agentUrl && (
                    <p className="mt-2 truncate font-mono text-[10px]" title={agentUrl}>
                        {agentUrl}
                    </p>
                )}
                <div className="mt-1 flex flex-wrap gap-1 text-[9px]">
                    {cardLocation && <span className="rounded-full bg-(--accent-n2-wMain)/10 px-1.5 py-0.5">{cardLocation}</span>}
                    <span className="rounded-full bg-(--accent-n2-wMain)/10 px-1.5 py-0.5">auth: {authType}</span>
                </div>
            </div>

            <Handle id="target" type="target" position={Position.Left} style={HANDLE_STYLE} />
        </CardShell>
    );
}

export const RemoteAgentCardNode = memo(RemoteAgentCardNodeInner, remoteAgentPropsEq);
