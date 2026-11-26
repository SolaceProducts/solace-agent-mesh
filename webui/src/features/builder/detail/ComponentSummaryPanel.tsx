// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import yaml from "js-yaml";
import type { LucideIcon } from "lucide-react";
import { AlertTriangle, ArrowRight, BookOpen, Bot, ExternalLink, Globe, Package, Plug, Workflow, Wrench, X } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { humanizeComponentName } from "../builderUtils";
import { StatusBadge, typeColor } from "../canvas/CardShell";
import { lookupConnectorIconByKey } from "../canvas/connectorIconInference";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { BooleanBadge, DetailPill, DetailSection, ExpandableText } from "./DetailPrimitives";

interface ComponentSummaryPanelProps {
    data: ComponentCanvasData;
    manifest: BuildManifest | null;
    componentData?: Map<string, ComponentCanvasData>;
    onClose: () => void;
    onViewDetails: () => void;
}

const TYPE_CONFIG: Record<string, { icon: LucideIcon; label: string }> = {
    agent: { icon: Bot, label: "Agent" },
    workflow: { icon: Workflow, label: "Workflow" },
    gateway: { icon: Globe, label: "Entrypoint" },
    connector: { icon: Plug, label: "Connector" },
    skill: { icon: BookOpen, label: "Skill" },
};

export function ComponentSummaryPanel({ data, manifest, componentData, onClose, onViewDetails }: ComponentSummaryPanelProps) {
    const typeCfg = TYPE_CONFIG[data.type] ?? { icon: Bot, label: data.type };
    const Icon = typeCfg.icon;
    const color = typeColor(data.type);

    return (
        <div className="absolute top-2 right-2 bottom-2 z-10 flex w-80 flex-col overflow-hidden rounded-xl border bg-(--background-w10) shadow-lg transition-opacity duration-200">
            <div className="flex items-center gap-3 border-b px-4 py-3">
                <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg" style={{ backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)` }}>
                    <Icon className="h-4.5 w-4.5" style={{ color }} />
                </div>
                <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                        <span className="truncate text-sm font-semibold" title={data.name}>
                            {humanizeComponentName(data.name)}
                        </span>
                        <StatusBadge status={data.status} />
                    </div>
                    <span className="text-[10px]">{typeCfg.label}</span>
                </div>
                <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close" className="shrink-0">
                    <X className="h-4 w-4" />
                </Button>
            </div>

            <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
                {data.type === "agent" && <AgentSummary data={data} manifest={manifest} componentData={componentData} />}
                {data.type === "workflow" && <WorkflowSummary data={data} />}
                {data.type === "gateway" && <GatewaySummary data={data} />}
                {data.type === "connector" && <ConnectorSummary data={data} />}
                {data.type === "skill" && <SkillSummary data={data} />}
            </div>

            <div className="border-t px-4 py-3">
                <Button className="w-full gap-1.5" size="sm" onClick={onViewDetails} disabled={!data.yamlText}>
                    <ExternalLink className="h-3.5 w-3.5" />
                    View Details
                </Button>
            </div>
        </div>
    );
}

function ConnectorIcon({ icon, name, className = "h-3 w-3 shrink-0" }: { icon?: string; name: string; className?: string }) {
    const [error, setError] = useState(false);
    if (icon && icon !== "plug" && !error) {
        return <img src={icon} alt={name} className={cn(className, "object-contain")} onError={() => setError(true)} />;
    }
    return <Plug className={className} style={{ color: "var(--accent-n2-wMain)" }} />;
}

function AgentSummary({ data, manifest, componentData }: { data: ComponentCanvasData; manifest: BuildManifest | null; componentData?: Map<string, ComponentCanvasData> }) {
    const config = data.agentConfig;
    const description = config?.description;
    const tools = config?.tools?.filter(t => t.tool_type !== "builtin-group") ?? [];
    const builtinGroups = config?.tools?.filter(t => t.tool_type === "builtin-group") ?? [];
    const toolsets = config?.toolsets ?? [];
    const skills = config?.skills ?? [];
    const hasTools = tools.length > 0 || builtinGroups.length > 0 || toolsets.length > 0;

    const extraFields = useMemo(() => {
        if (!data.yamlText) return { model: null as string | null, supportsStreaming: null as boolean | null, hasHil: false, hasOutputSchema: false };
        try {
            const parsed = yaml.load(data.yamlText, { json: true }) as Record<string, unknown>;
            const apps = parsed?.apps as Record<string, unknown>[] | undefined;
            const appConfig = apps?.[0]?.app_config as Record<string, unknown> | undefined;
            if (!appConfig) return { model: null, supportsStreaming: null, hasHil: false, hasOutputSchema: false };

            const modelRaw = appConfig.model;
            const model = typeof modelRaw === "string" ? modelRaw : modelRaw && typeof modelRaw === "object" ? (((modelRaw as Record<string, unknown>).model as string) ?? null) : null;

            return {
                model,
                supportsStreaming: typeof appConfig.supports_streaming === "boolean" ? appConfig.supports_streaming : null,
                hasHil: appConfig.hil != null,
                hasOutputSchema: appConfig.output_schema != null,
            };
        } catch {
            return { model: null, supportsStreaming: null, hasHil: false, hasOutputSchema: false };
        }
    }, [data.yamlText]);

    const manifestComp = manifest?.components?.find(c => c.name === data.name);
    const connectorNames = new Set([...(data.dependsOn ?? []), ...(manifestComp?.connectors ?? [])]);
    const connectors = [...connectorNames].map(dep => manifest?.components?.find(c => c.name === dep)).filter(c => c?.type === "connector") as NonNullable<BuildManifest["components"]>[number][];

    return (
        <>
            {description && (
                <DetailSection label="Description">
                    <ExpandableText text={description} />
                </DetailSection>
            )}

            {extraFields.model && (
                <DetailSection label="Model">
                    <DetailPill variant="brand">{extraFields.model}</DetailPill>
                </DetailSection>
            )}

            {hasTools && (
                <DetailSection label="Tools">
                    <div className="space-y-1">
                        {tools.map((tool, i) => (
                            <div key={`t-${i}`} className="flex items-center gap-1.5 text-xs">
                                <Wrench className="h-3 w-3 shrink-0" style={{ color: "var(--accent-n2-wMain)" }} />
                                <span className="min-w-0 truncate">{tool.tool_name || "Unnamed"}</span>
                                <span className="ml-auto shrink-0 text-[10px]">{tool.tool_type === "sam_remote" ? "Remote" : tool.tool_type === "mcp" ? "MCP" : "Built-in"}</span>
                            </div>
                        ))}
                        {builtinGroups.map((tool, i) => (
                            <div key={`bg-${i}`} className="flex items-center gap-1.5 text-xs">
                                <Package className="h-3 w-3 shrink-0" style={{ color: "var(--accent-n2-wMain)" }} />
                                <span className="min-w-0 truncate">{humanizeComponentName(tool.group_name || "Unnamed")}</span>
                                <span className="ml-auto shrink-0 text-[10px]">Group</span>
                            </div>
                        ))}
                        {toolsets.map((ts, i) => (
                            <div key={`ts-${i}`} className="flex items-center gap-1.5 text-xs">
                                <Package className="h-3 w-3 shrink-0" style={{ color: "var(--accent-n2-wMain)" }} />
                                <span className="min-w-0 truncate">{humanizeComponentName(ts)}</span>
                                <span className="ml-auto shrink-0 text-[10px]">Group</span>
                            </div>
                        ))}
                    </div>
                </DetailSection>
            )}

            {skills.length > 0 && (
                <DetailSection label="Skills">
                    <div className="space-y-1">
                        {skills.map((skill, i) => (
                            <div key={i} className="flex items-center gap-1.5 text-xs">
                                <BookOpen className="h-3 w-3 shrink-0" style={{ color: "var(--info-wMain)" }} />
                                <span className="min-w-0 truncate">{skill.name}</span>
                            </div>
                        ))}
                    </div>
                </DetailSection>
            )}

            {connectors.length > 0 && (
                <DetailSection label="Connectors">
                    <div className="space-y-1">
                        {connectors.map(conn => {
                            const connData = componentData?.get(conn.name);
                            const connMeta = connData?.connectorMetadata;
                            let iconUrl: string | undefined;
                            if (connMeta?.icon && connMeta.icon !== "other") {
                                iconUrl = lookupConnectorIconByKey(connMeta.icon);
                            }
                            if (!iconUrl && conn.icon && conn.icon !== "other") {
                                iconUrl = lookupConnectorIconByKey(conn.icon);
                            }
                            return (
                                <div key={conn.name} className="flex items-center gap-1.5 text-xs">
                                    <ConnectorIcon icon={iconUrl} name={conn.name} />
                                    <span className="min-w-0 truncate">{conn.name}</span>
                                </div>
                            );
                        })}
                    </div>
                </DetailSection>
            )}

            <FlagRow
                flags={
                    [
                        extraFields.supportsStreaming != null && { label: "Streaming", value: extraFields.supportsStreaming },
                        extraFields.hasHil && { label: "Human-in-Loop", value: true },
                        extraFields.hasOutputSchema && { label: "Structured Output", value: true },
                    ].filter(Boolean) as { label: string; value: boolean }[]
                }
            />
        </>
    );
}

function WorkflowSummary({ data }: { data: ComponentCanvasData }) {
    const summary = data.workflowNodeSummary;

    return (
        <>
            {summary && (
                <>
                    <DetailSection label="Nodes">
                        <div className="text-xs">
                            {summary.total} node{summary.total !== 1 ? "s" : ""}
                            {Object.keys(summary.types).length > 0 && (
                                <span className="ml-1">
                                    (
                                    {Object.entries(summary.types)
                                        .map(([t, c]) => `${c} ${t}`)
                                        .join(", ")}
                                    )
                                </span>
                            )}
                        </div>
                    </DetailSection>

                    {summary.agents.length > 0 && (
                        <DetailSection label="Agents Referenced">
                            <div className="space-y-1">
                                {summary.agents.map(name => (
                                    <div key={name} className="flex items-center gap-1.5 text-xs">
                                        <Bot className="h-3 w-3 shrink-0" style={{ color: "var(--brand-wMain)" }} />
                                        <span className="min-w-0 truncate">{name}</span>
                                    </div>
                                ))}
                            </div>
                        </DetailSection>
                    )}
                </>
            )}
        </>
    );
}

function GatewaySummary({ data }: { data: ComponentCanvasData }) {
    const meta = data.gatewayMetadata;
    if (!meta) return null;

    const targets = meta.eventHandlers.map(h => h.target_agent_name || h.target_workflow_name).filter(Boolean) as string[];
    const uniqueTargets = [...new Set(targets)];

    return (
        <>
            {meta.eventHandlers.length > 0 && (
                <DetailSection label="Event Handlers">
                    <div className="mb-1 text-xs">
                        {meta.eventHandlers.length} handler{meta.eventHandlers.length !== 1 ? "s" : ""}
                    </div>
                    {uniqueTargets.length > 0 && (
                        <div className="space-y-1">
                            {uniqueTargets.map(name => (
                                <div key={name} className="flex items-center gap-1.5 text-xs">
                                    <ArrowRight className="h-3 w-3 shrink-0" style={{ color: "var(--brand-wMain)" }} />
                                    <span className="min-w-0 truncate">{name}</span>
                                </div>
                            ))}
                        </div>
                    )}
                </DetailSection>
            )}

            {meta.outputHandlers.length > 0 && (
                <DetailSection label="Output Handlers">
                    <div className="text-xs">
                        {meta.outputHandlers.length} handler{meta.outputHandlers.length !== 1 ? "s" : ""}
                    </div>
                </DetailSection>
            )}

            {meta.hasCustomBroker && (
                <DetailSection label="Broker">
                    <DetailPill variant="accent">Custom Event Mesh</DetailPill>
                </DetailSection>
            )}
        </>
    );
}

function ConnectorSummary({ data }: { data: ComponentCanvasData }) {
    const meta = data.connectorMetadata;
    if (!meta) return null;

    const missingParams = meta.values ? Object.entries(meta.values).filter(([, v]) => v === "<<__SAM_REQUIRED__>>") : [];

    return (
        <>
            {meta.description && (
                <DetailSection label="Description">
                    <ExpandableText text={meta.description} />
                </DetailSection>
            )}

            <DetailSection label="Type">
                <DetailPill variant="accent">
                    {meta.type}
                    {meta.subtype ? ` \u00B7 ${meta.subtype}` : ""}
                </DetailPill>
            </DetailSection>

            {missingParams.length > 0 && (
                <DetailSection label="Missing Parameters">
                    <div className="flex items-start gap-1.5 text-xs">
                        <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" style={{ color: "var(--warning-wMain)" }} />
                        <span>
                            {missingParams.length} required parameter{missingParams.length !== 1 ? "s" : ""} not yet configured
                        </span>
                    </div>
                </DetailSection>
            )}
        </>
    );
}

function SkillSummary({ data }: { data: ComponentCanvasData }) {
    let description = "";
    if (data.yamlText) {
        try {
            const match = data.yamlText.match(/^description:\s*(.+)$/m);
            if (match) description = match[1].trim().replace(/^["']|["']$/g, "");
        } catch {}
    }

    return (
        <>
            {description && (
                <DetailSection label="Description">
                    <ExpandableText text={description} />
                </DetailSection>
            )}
        </>
    );
}

function FlagRow({ flags }: { flags: { label: string; value: boolean }[] }) {
    if (flags.length === 0) return null;
    return (
        <DetailSection label="Features">
            <div className="flex flex-wrap gap-x-4 gap-y-1">
                {flags.map(f => (
                    <div key={f.label} className="flex items-center gap-1.5 text-xs">
                        <span>{f.label}</span>
                        <BooleanBadge value={f.value} />
                    </div>
                ))}
            </div>
        </DetailSection>
    );
}
