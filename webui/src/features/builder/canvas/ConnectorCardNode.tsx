// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo, useMemo, useState } from "react";
import type { NodeProps } from "@xyflow/react";
import { Handle, Position } from "@xyflow/react";
import { AlertTriangle, Plug } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { TruncatedText } from "@/ui/truncated-text";

import { humanizeComponentName, isSecretKey } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { CardShell } from "./CardShell";
import { inferConnectorIconFromName, lookupConnectorIconByKey } from "./connectorIconInference";
import { useDerivedCardStatus } from "./useDerivedCardStatus";

interface ConnectorCardData {
    component: NonNullable<BuildManifest["components"]>[number];
    canvasData: ComponentCanvasData | null;
    isSingleConnector?: boolean;
}

export function connectorPropsEq(prev: NodeProps, next: NodeProps): boolean {
    const a = prev.data as unknown as ConnectorCardData;
    const b = next.data as unknown as ConnectorCardData;
    return prev.selected === next.selected && a.component === b.component && a.canvasData === b.canvasData && a.isSingleConnector === b.isSingleConnector;
}

const SAM_REQUIRED = "<<__SAM_REQUIRED__>>";

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

function renderSecretAlwaysMaskedOnCanvas() {
    return <span className="shrink-0 font-mono text-[10px] tracking-wider">{"••••••••"}</span>;
}

function renderParamValue(key: string, value: unknown) {
    if (value === SAM_REQUIRED) {
        return (
            <span className="shrink-0 rounded-full px-1.5 py-0.5 text-[9px] font-medium" style={{ backgroundColor: "color-mix(in srgb, var(--warning-wMain) 15%, transparent)", color: "var(--warning-wMain)" }}>
                missing
            </span>
        );
    }
    if (isSecretKey(key)) {
        return renderSecretAlwaysMaskedOnCanvas();
    }
    const shown = typeof value === "string" && value.length > 24 ? value.slice(0, 24) + "…" : String(value);
    return <span className="shrink-0 truncate text-[10px]">{shown}</span>;
}

function ConnectorCardNodeInner({ data, selected }: NodeProps) {
    const { component, canvasData, isSingleConnector } = data as unknown as ConnectorCardData;
    const expanded = isSingleConnector === true;
    const metadata = canvasData?.connectorMetadata;
    const { isGenerating, isEditing, showsQueued, isActive, isReady, showsDraft, statusText, effectiveStatus } = useDerivedCardStatus(canvasData, component);

    const [iconError, setIconError] = useState(false);
    const schemaName = metadata?.name ?? component.name;

    const effectiveIcon = useMemo(() => {
        if (metadata?.icon && metadata.icon !== "other") {
            const resolved = lookupConnectorIconByKey(metadata.icon);
            if (resolved) return resolved;
        }
        return inferConnectorIconFromName(component.name);
    }, [metadata?.icon, component.name]);

    const allParamEntries = Object.entries(metadata?.values ?? {});
    const missingCount = allParamEntries.filter(([, v]) => v === SAM_REQUIRED).length;
    const PARAM_PREVIEW_LIMIT = 5;
    const shownParams = allParamEntries.slice(0, PARAM_PREVIEW_LIMIT);
    const extraParams = Math.max(0, allParamEntries.length - PARAM_PREVIEW_LIMIT);

    const cardWidth = expanded ? 380 : 280;

    return (
        <CardShell selected={selected} generating={isGenerating} editing={isEditing} queued={showsQueued} ready={isReady} active={isActive} draft={showsDraft} error={effectiveStatus === "error"} width={cardWidth} statusText={statusText}>
            <div className={expanded && (allParamEntries.length > 0 || missingCount > 0) ? "border-b px-4 py-3" : "px-4 py-3"}>
                <div className="flex items-center gap-2.5">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-(--accent-n2-wMain)/15">
                        {effectiveIcon && !iconError ? <img src={effectiveIcon} alt={schemaName} className="h-5 w-5 object-contain" onError={() => setIconError(true)} /> : <Plug className="h-4 w-4" style={{ color: "var(--accent-n2-wMain)" }} />}
                    </div>
                    <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                            <TruncatedText text={component.name} className="text-sm font-semibold">
                                {humanizeComponentName(component.name)}
                            </TruncatedText>
                        </div>
                        <span className="text-[10px]">{metadata ? `${metadata.type} · ${metadata.subtype}` : "Connector"}</span>
                    </div>
                </div>
                {(metadata?.description || component.description) && <p className={cn("mt-2 text-xs leading-relaxed", !expanded && "line-clamp-2")}>{metadata?.description || component.description}</p>}
            </div>

            {expanded && allParamEntries.length > 0 && (
                <div className={missingCount > 0 ? "border-b px-4 py-2" : "px-4 py-2"}>
                    <div className="mb-1.5 text-[10px] font-medium tracking-wider uppercase">Parameters</div>
                    <div className="space-y-1">
                        {shownParams.map(([key, value]) => (
                            <div key={key} className="flex items-center gap-2 text-xs">
                                <span className="min-w-0 flex-1 truncate font-mono">{key}</span>
                                {renderParamValue(key, value)}
                            </div>
                        ))}
                        {extraParams > 0 && <div className="pt-0.5 text-[10px] italic">+{extraParams} more</div>}
                    </div>
                </div>
            )}

            {missingCount > 0 && (
                <div className={expanded ? "px-4 py-2" : "border-t px-4 py-2"}>
                    <div className="flex items-center gap-1.5 text-xs" style={{ color: "var(--warning-wMain)" }}>
                        <AlertTriangle className="h-3 w-3 shrink-0" />
                        <span>
                            {missingCount} required parameter{missingCount > 1 ? "s" : ""} missing
                        </span>
                    </div>
                </div>
            )}

            <Handle id="target" type="target" position={Position.Left} style={HANDLE_STYLE} />
        </CardShell>
    );
}

export const ConnectorCardNode = memo(ConnectorCardNodeInner, connectorPropsEq);
