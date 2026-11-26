// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { NodeProps } from "@xyflow/react";
import { Handle, Position } from "@xyflow/react";
import { Bot, Workflow } from "lucide-react";

import { TruncatedText } from "@/ui/truncated-text";

import { humanizeComponentName } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { CardShell, typeColor } from "./CardShell";
import { useDerivedCardStatus } from "./useDerivedCardStatus";

interface WorkflowCardData {
    component: NonNullable<BuildManifest["components"]>[number];
    canvasData: ComponentCanvasData | null;
}

export function workflowPropsEq(prev: NodeProps, next: NodeProps): boolean {
    const a = prev.data as unknown as WorkflowCardData;
    const b = next.data as unknown as WorkflowCardData;
    return prev.selected === next.selected && a.component === b.component && a.canvasData === b.canvasData;
}

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

function WorkflowCardNodeInner({ data, selected }: NodeProps) {
    const { component, canvasData } = data as unknown as WorkflowCardData;
    const summary = canvasData?.workflowNodeSummary;
    const { isGenerating, isEditing, showsQueued, isBuildingDeps, isActive, isReady, showsDraft, statusText, effectiveStatus } = useDerivedCardStatus(canvasData, component);

    const typeBreakdown = summary
        ? Object.entries(summary.types)
              .map(([type, count]) => `${count} ${type}${count > 1 ? "s" : ""}`)
              .join(", ")
        : null;

    return (
        <CardShell
            selected={selected}
            generating={isGenerating}
            editing={isEditing}
            queued={showsQueued}
            buildingDeps={isBuildingDeps}
            ready={isReady}
            active={isActive}
            draft={showsDraft}
            error={effectiveStatus === "error"}
            width={360}
            statusText={statusText}
            componentName={component.name}
            testEligible
        >
            <div className="flex items-center justify-between gap-2 px-4 py-3">
                <div className="flex min-w-0 items-center gap-2">
                    <Workflow className="h-5 w-5 shrink-0" style={{ color: typeColor("workflow") }} />
                    <TruncatedText text={component.name} className="text-sm font-semibold">
                        {humanizeComponentName(component.name)}
                    </TruncatedText>
                </div>
                <span className="shrink-0 rounded px-2 py-0.5 text-sm font-medium text-[var(--secondary-text-wMain)]">Workflow</span>
            </div>
            {component.description && <p className="line-clamp-2 border-t px-4 py-2 text-xs leading-relaxed">{component.description}</p>}

            {summary && (
                <div className="border-t px-4 py-2">
                    {typeBreakdown && (
                        <div className="mb-1.5 text-[10px]">
                            {summary.total} nodes: {typeBreakdown}
                        </div>
                    )}
                    {summary.agents.length > 0 && (
                        <div className="space-y-0.5">
                            {summary.agents.map(name => (
                                <div key={name} className="flex items-center gap-1.5 text-xs">
                                    <Bot className="h-3 w-3 shrink-0" />
                                    <TruncatedText text={name} />
                                </div>
                            ))}
                        </div>
                    )}
                </div>
            )}

            <Handle id="target" type="target" position={Position.Left} style={HANDLE_STYLE} />
            <Handle id="source" type="source" position={Position.Right} style={HANDLE_STYLE} />
            <Handle id="top" type="target" position={Position.Top} style={HANDLE_STYLE} />
            <Handle id="bottom" type="source" position={Position.Bottom} style={HANDLE_STYLE} />
        </CardShell>
    );
}

export const WorkflowCardNode = memo(WorkflowCardNodeInner, workflowPropsEq);
