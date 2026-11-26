// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo, useMemo } from "react";
import type { NodeProps } from "@xyflow/react";
import { Handle, Position } from "@xyflow/react";
import yaml from "js-yaml";
import { BookOpen } from "lucide-react";

import { TruncatedText } from "@/ui/truncated-text";

import { humanizeComponentName, skillToolNames } from "../builderUtils";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { CardShell } from "./CardShell";
import { useDerivedCardStatus } from "./useDerivedCardStatus";

interface SkillCardData {
    component: NonNullable<BuildManifest["components"]>[number];
    canvasData: ComponentCanvasData | null;
    manifest: BuildManifest;
}

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

const MAX_SKILL_ITEMS = 4;

function SkillItemRow({ label, items }: Readonly<{ label: string; items: string[] }>) {
    const shown = items.slice(0, MAX_SKILL_ITEMS);
    const extra = items.length - shown.length;
    return (
        <div className="mt-2">
            <div className="text-[9px] font-semibold tracking-wide text-(--secondary-text-wMain) uppercase">{label}</div>
            <div className="mt-1 flex flex-wrap gap-1">
                {shown.map(name => (
                    <TruncatedText key={name} text={name} className="max-w-full rounded bg-(--secondary-w8040) px-1.5 py-0.5 text-[10px] leading-tight" />
                ))}
                {extra > 0 && <span className="px-1 py-0.5 text-[10px] text-(--secondary-text-wMain)">+{extra} more</span>}
            </div>
        </div>
    );
}

function extractSkillDescription(yamlText: string | null | undefined): string | undefined {
    if (!yamlText) return undefined;
    try {
        const doc = yaml.load(yamlText, { json: true }) as Record<string, unknown> | null;
        if (!doc || typeof doc !== "object") return undefined;
        return typeof doc.description === "string" ? doc.description : undefined;
    } catch {
        return undefined;
    }
}

function SkillCardNodeInner({ data, selected }: NodeProps) {
    const { component, canvasData } = data as unknown as SkillCardData;
    const { isGenerating, isEditing, showsQueued, isActive, isReady, showsDraft, statusText, effectiveStatus } = useDerivedCardStatus(canvasData, component);

    const description = useMemo(() => extractSkillDescription(canvasData?.yamlText) || component.description, [canvasData?.yamlText, component.description]);

    const assets = canvasData?.skillConfig?.references ?? [];
    const tools = useMemo(() => skillToolNames(component.tools, canvasData?.skillConfig?.tools), [component.tools, canvasData?.skillConfig?.tools]);

    return (
        <CardShell
            selected={selected}
            generating={isGenerating}
            editing={isEditing}
            queued={showsQueued}
            ready={isReady}
            active={isActive}
            doneLabel="Available"
            draft={showsDraft}
            error={effectiveStatus === "error"}
            width={280}
            statusText={statusText}
        >
            <div className="px-4 py-3">
                <div className="flex items-center gap-2.5">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-(--info-wMain)/15">
                        <BookOpen className="h-4 w-4" style={{ color: "var(--info-wMain)" }} />
                    </div>
                    <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                            <TruncatedText text={component.name} className="text-sm font-semibold">
                                {humanizeComponentName(component.name)}
                            </TruncatedText>
                        </div>
                        <span className="text-[10px]">Skill</span>
                    </div>
                </div>
                {description && <p className="mt-2 line-clamp-2 text-xs leading-relaxed">{description}</p>}
                {tools.length > 0 && <SkillItemRow label="Tools" items={tools} />}
                {assets.length > 0 && <SkillItemRow label="References" items={assets} />}
            </div>

            <Handle id="target" type="target" position={Position.Left} style={HANDLE_STYLE} />
        </CardShell>
    );
}

export const SkillCardNode = memo(SkillCardNodeInner);
