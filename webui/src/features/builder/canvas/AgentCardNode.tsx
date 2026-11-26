// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo, useRef, useState } from "react";
import type { NodeProps } from "@xyflow/react";
import { Handle, Position } from "@xyflow/react";
import { BookOpen, Bot, CircleCheckBig, Clock, FileText, Loader2, Pause, Plug } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";
import { TruncatedText } from "@/ui/truncated-text";

import { humanizeComponentName } from "../builderUtils";
import type { ComponentCanvasData, DependencyProgress } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { agentCardName } from "./cardLabels";
import { CardShell } from "./CardShell";
import { lookupConnectorIconByKey } from "./connectorIconInference";
import { useDerivedCardStatus } from "./useDerivedCardStatus";

interface AgentCardData {
    component: NonNullable<BuildManifest["components"]>[number];
    canvasData: ComponentCanvasData;
    manifest: BuildManifest;
    componentData?: Map<string, ComponentCanvasData>;
    isSingleAgent?: boolean;
}

export function agentPropsEq(prev: NodeProps, next: NodeProps): boolean {
    const a = prev.data as unknown as AgentCardData;
    const b = next.data as unknown as AgentCardData;
    if (prev.selected !== next.selected) return false;
    if (a.component !== b.component) return false;
    if (a.canvasData !== b.canvasData) return false;
    if (a.manifest !== b.manifest) return false;
    if (a.isSingleAgent !== b.isSingleAgent) return false;
    const depNames = new Set([...(a.component.depends_on ?? []), ...(a.component.connectors ?? [])]);
    for (const name of depNames) {
        if (a.componentData?.get(name) !== b.componentData?.get(name)) return false;
    }
    return true;
}

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

function ConnectorSchemaIcon({ icon, name, className = "h-3.5 w-3.5" }: { icon?: string; name: string; className?: string }) {
    const [error, setError] = useState(false);
    if (icon && icon !== "plug" && !error) {
        return <img src={icon} alt={name} className={cn(className, "object-contain")} onError={() => setError(true)} />;
    }
    return <Plug className={className} style={{ color: "var(--accent-n2-wMain)" }} />;
}

function ConnectorIconStack({ items, max = 3 }: { items: Array<{ name: string; icon?: string; schemaName: string }>; max?: number }) {
    const visible = items.slice(0, max);
    const overflow = items.length - max;

    return (
        <div className="flex shrink-0 items-center" style={{ marginLeft: 4 }}>
            {visible.map(({ name: connName, icon, schemaName }, i) => (
                <div key={connName} className="flex h-6 w-6 items-center justify-center rounded-full border border-(--background-w10) bg-(--background-w20)" style={{ marginLeft: i === 0 ? 0 : -6, zIndex: visible.length - i }} title={connName}>
                    <ConnectorSchemaIcon icon={icon} name={schemaName} className="h-3.5 w-3.5" />
                </div>
            ))}
            {overflow > 0 && (
                <div
                    className="flex h-6 items-center justify-center rounded-full border border-(--background-w10) bg-(--background-w20) px-1.5"
                    style={{ marginLeft: -6, zIndex: 0 }}
                    title={items
                        .slice(max)
                        .map(c => c.name)
                        .join(", ")}
                >
                    <span className="text-[9px] font-medium">+{overflow}</span>
                </div>
            )}
        </div>
    );
}

function SkeletonBars({ count = 3, className = "" }: { count?: number; className?: string }) {
    return (
        <div className={cn("space-y-1.5", className)}>
            {Array.from({ length: count }).map((_, i) => (
                <div key={i} className="h-3 animate-pulse rounded bg-current opacity-10" style={{ width: `${100 - i * 15}%` }} />
            ))}
        </div>
    );
}

export function DepStatusIcon({ status }: Readonly<{ status: DependencyProgress["status"] }>) {
    switch (status) {
        case "generating":
            return <Loader2 className="h-3 w-3 shrink-0 animate-spin" style={{ color: "var(--brand-wMain)" }} />;
        case "done":
            return <CircleCheckBig className="h-3 w-3 shrink-0" style={{ color: "var(--success-wMain)" }} />;
        case "waiting":
            return <Pause className="h-3 w-3 shrink-0" style={{ color: "var(--warning-wMain)" }} />;
        default:
            return <Clock className="h-3 w-3 shrink-0" />;
    }
}

function DepTypeIcon({ type, icon }: { type: string; icon?: string }) {
    if (type === "connector") {
        if (icon && icon !== "other") {
            const resolved = lookupConnectorIconByKey(icon);
            if (resolved) return <img src={resolved} alt={icon} className="h-3 w-3 shrink-0 object-contain" />;
        }
        return <Plug className="h-3 w-3 shrink-0" />;
    }
    if (type === "skill") return <BookOpen className="h-3 w-3 shrink-0" />;
    return <Bot className="h-3 w-3 shrink-0" />;
}

function SubCard({ icon: Icon, name, iconUrl, isNew }: { icon: typeof Bot; name: string; iconUrl?: string; isNew?: boolean }) {
    const [imgError, setImgError] = useState(false);
    return (
        <div className="flex items-center gap-2 rounded-md border px-3 py-2">
            {iconUrl && !imgError ? <img src={iconUrl} alt={name} className="h-3.5 w-3.5 shrink-0 object-contain" onError={() => setImgError(true)} /> : <Icon className="h-3.5 w-3.5 shrink-0" />}
            <TruncatedText text={name} className="min-w-0 flex-1 text-xs font-medium" />
            {isNew && (
                <span className="shrink-0 rounded-full bg-(--brand-wMain)/15 px-1.5 py-0.5 text-[9px] font-medium" style={{ color: "var(--brand-wMain)" }}>
                    New
                </span>
            )}
        </div>
    );
}

function AgentCardNodeInner({ data, selected }: NodeProps) {
    const { component, canvasData, manifest, componentData, isSingleAgent } = data as unknown as AgentCardData;
    const config = canvasData?.agentConfig;
    const name = agentCardName(canvasData, component?.name);
    const lastDescriptionRef = useRef<string>("");
    const lastSkillsRef = useRef<Array<{ name: string }>>([]);
    const lastInstructionRef = useRef<string>("");

    const currentDescription = config?.description || component?.description || "";
    if (currentDescription) lastDescriptionRef.current = currentDescription;
    const description = currentDescription || lastDescriptionRef.current;

    const currentInstruction = config?.instruction || "";
    if (currentInstruction) lastInstructionRef.current = currentInstruction;
    const instruction = currentInstruction || lastInstructionRef.current;
    const hasInstruction = instruction.trim().length > 0;

    const { isGenerating, isEditing, showsQueued, isBuildingDeps, isActive, isReady, showsDraft, statusText, effectiveStatus } = useDerivedCardStatus(canvasData);
    const depProgress = canvasData?.dependencyProgress ?? [];

    if (config?.skills && config.skills.length > 0) lastSkillsRef.current = config.skills;
    const currentSkills: Array<{ name: string }> = config?.skills ?? [];
    const skills = currentSkills.length > 0 ? currentSkills : lastSkillsRef.current;
    const hasSkills = skills.length > 0;

    const connectorNames = new Set([...(component?.depends_on ?? []), ...(component?.connectors ?? [])]);
    const connectors = [...connectorNames].map(dep => manifest?.components?.find(c => c.name === dep)).filter(c => c?.type === "connector") as NonNullable<BuildManifest["components"]>[number][];
    const hasConnectors = connectors.length > 0;

    const connectorSchemaInfos = connectors.map(conn => {
        const connData = componentData?.get(conn.name);
        const connMeta = connData?.connectorMetadata;
        let effectiveIcon: string | undefined;
        if (connMeta?.icon && connMeta.icon !== "other") {
            effectiveIcon = lookupConnectorIconByKey(connMeta.icon);
        }
        if (!effectiveIcon && conn.icon && conn.icon !== "other") {
            effectiveIcon = lookupConnectorIconByKey(conn.icon);
        }
        return { name: conn.name, icon: effectiveIcon, schemaName: conn.name, origin: conn.origin };
    });

    const expanded = isSingleAgent === true;
    const cardWidth = expanded ? 480 : 360;

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
            width={cardWidth}
            statusText={statusText}
            componentName={component.name}
            testEligible
        >
            <div className="px-4 py-3">
                <div className="flex items-center gap-2.5">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-(--brand-wMain)/15">
                        <Bot className="h-4 w-4" style={{ color: "var(--brand-wMain)" }} />
                    </div>
                    <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                            <TruncatedText text={name} className="text-sm font-semibold">
                                {humanizeComponentName(name)}
                            </TruncatedText>
                        </div>
                        <span className="text-xs text-(--secondary-text-wMain)">Agent</span>
                    </div>
                    {connectorSchemaInfos.length > 0 && <ConnectorIconStack items={connectorSchemaInfos} max={3} />}
                </div>
                {description ? <p className={cn("mt-2 text-xs leading-relaxed", !expanded && "line-clamp-2")}>{description}</p> : isGenerating && !lastDescriptionRef.current ? <SkeletonBars count={2} className="mt-2" /> : null}
            </div>

            {isBuildingDeps && depProgress.length > 0 && (
                <div className="px-4 py-2">
                    <div className="space-y-1">
                        {depProgress.map(dep => (
                            <div key={dep.name} className="flex items-center gap-2">
                                <DepTypeIcon type={dep.type} icon={dep.icon} />
                                <TruncatedText text={dep.name} className="min-w-0 flex-1 text-[11px]" />
                                <DepStatusIcon status={dep.status} />
                            </div>
                        ))}
                    </div>
                </div>
            )}

            {expanded && hasInstruction && (
                <div className="px-4 py-2">
                    <div className="mt-1.5 mb-1.5 flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
                        <FileText className="h-3 w-3" />
                        Instructions
                    </div>
                    <div>
                        <div
                            className="overflow-hidden px-3 pt-2 text-xs leading-relaxed font-normal whitespace-pre-wrap"
                            style={{
                                display: "-webkit-box",
                                WebkitLineClamp: 4,
                                WebkitBoxOrient: "vertical",
                            }}
                        >
                            {instruction}
                        </div>
                        <Button
                            variant="link"
                            size="sm"
                            onClick={e => {
                                e.stopPropagation();
                                window.dispatchEvent(new CustomEvent("builder-open-component-detail", { detail: { name: component.name } }));
                            }}
                            className="h-auto px-3 pt-1 pb-2 text-xs font-medium"
                            title="View full instructions"
                        >
                            See more
                        </Button>
                    </div>
                </div>
            )}

            {expanded && hasConnectors && (
                <div className="px-4 py-2">
                    <div className="mt-1.5 mb-1.5 flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
                        <Plug className="h-3 w-3" />
                        Connectors
                    </div>
                    <div className="space-y-1.5">
                        {connectorSchemaInfos.map(({ name: connName, icon, origin }) => (
                            <SubCard key={connName} icon={Plug} name={connName} iconUrl={icon} isNew={origin === "session"} />
                        ))}
                    </div>
                </div>
            )}

            {expanded && hasSkills && (
                <div className="px-4 py-2">
                    <div className="mb-1.5 text-xs font-medium tracking-wider text-(--secondary-text-wMain) uppercase">Skills</div>
                    <div className="space-y-1.5">
                        {skills.map((skill: { name: string }, i: number) => (
                            <SubCard key={`skill-${i}`} icon={BookOpen} name={skill.name} isNew />
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

export const AgentCardNode = memo(AgentCardNodeInner, agentPropsEq);
