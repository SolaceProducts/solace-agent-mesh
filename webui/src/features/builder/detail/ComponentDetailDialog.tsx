// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, type ReactNode, useLayoutEffect, useMemo, useRef, useState } from "react";
import yaml from "js-yaml";
import type { LucideIcon } from "lucide-react";
import { BookOpen, Bot, Globe, Plug, Workflow, X } from "lucide-react";

import { useAgents } from "@/api/agents";

import { workflowBody } from "@/utils/agentUtils";

import { Button } from "@/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle, VisuallyHidden } from "@/ui/dialog";

import { AgentView } from "@/features/agents/AgentView";

import { componentTypeLabel, humanizeComponentName } from "../builderUtils";
import { StatusBadge, typeColor } from "../canvas/CardShell";
import { lookupConnectorIconByKey } from "../canvas/connectorIconInference";
import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";

import { claimedKeys, getSectionMap, type SectionMap, type SectionMapEntry } from "./sectionMaps";
import { YamlSectionRenderer } from "./YamlSectionRenderer";

interface ComponentDetailDialogProps {
    data: ComponentCanvasData;
    isOpen: boolean;
    onClose: () => void;
    manifest?: BuildManifest | null;
    componentData?: Map<string, ComponentCanvasData>;
    workflowRole?: { workflowName: string; node: Record<string, unknown> | null } | null;
}

const TYPE_ICON: Record<string, LucideIcon> = {
    agent: Bot,
    workflow: Workflow,
    gateway: Globe,
    connector: Plug,
    skill: BookOpen,
};

function extractConfig(data: ComponentCanvasData): Record<string, unknown> | null {
    if (!data.yamlText) return null;

    try {
        if (data.type === "connector") {
            const parsed = JSON.parse(data.yamlText) as Record<string, unknown>;
            return parsed;
        }

        const parsed = yaml.load(data.yamlText, { json: true }) as Record<string, unknown>;

        if (data.type === "skill") {
            return parsed;
        }

        const apps = parsed?.apps as Record<string, unknown>[] | undefined;
        if (apps && apps.length > 0) {
            const appConfig = apps[0]?.app_config as Record<string, unknown>;
            if (appConfig) {
                if (data.type === "workflow") {
                    const body = workflowBody(appConfig);
                    if (body === appConfig) return appConfig;
                    const { workflow: _nestedBody, ...outer } = appConfig;
                    return { ...outer, ...body };
                }
                return appConfig;
            }
        }

        return parsed;
    } catch {
        return null;
    }
}

interface ResolvedSection extends SectionMapEntry {
    hasData: boolean;
}

function resolveSections(sectionMap: SectionMap, config: Record<string, unknown>): { sections: ResolvedSection[]; otherKeys: string[] } {
    const claimed = claimedKeys(sectionMap);
    const allKeys = Object.keys(config);

    const sections: ResolvedSection[] = sectionMap.map(entry => ({
        ...entry,
        hasData: entry.keys.some(k => k in config && config[k] !== undefined && config[k] !== null),
    }));

    const otherKeys = allKeys.filter(k => !claimed.has(k));

    return { sections, otherKeys };
}

export function ComponentDetailDialog({ data, isOpen, onClose, manifest, componentData, workflowRole }: ComponentDetailDialogProps) {
    const Icon = TYPE_ICON[data.type] ?? Bot;
    const color = typeColor(data.type);
    const label = componentTypeLabel(data.type);

    const config = useMemo(() => extractConfig(data), [data]);

    const openerRef = useRef<HTMLElement | null>(null);
    useLayoutEffect(() => {
        if (isOpen) openerRef.current = document.activeElement as HTMLElement | null;
    }, [isOpen]);

    const isAgent = data.type === "agent";
    const { data: userAgents = [], isLoading: agentsLoading } = useAgents({ enabled: isAgent && !config });
    const matchedAgent = useMemo(() => {
        if (!isAgent) return null;
        return userAgents.find(a => a.name === data.name) ?? userAgents.find(a => a.name.toLowerCase() === data.name.toLowerCase()) ?? null;
    }, [isAgent, userAgents, data.name]);

    const sectionMap = useMemo(() => getSectionMap(data.type as "agent" | "workflow" | "gateway" | "connector" | "skill"), [data.type]);

    const { sections, otherKeys } = useMemo(() => {
        if (!config) return { sections: [] as ResolvedSection[], otherKeys: [] as string[] };
        return resolveSections(sectionMap, config);
    }, [sectionMap, config]);

    const connectors = useMemo(() => {
        if (data.type !== "agent" || !manifest) return [];
        const manifestComp = manifest.components?.find(c => c.name === data.name);
        const connectorNames = new Set([...(data.dependsOn ?? []), ...(manifestComp?.connectors ?? [])]);
        return [...connectorNames].map(dep => manifest.components?.find(c => c.name === dep)).filter(c => c?.type === "connector") as NonNullable<BuildManifest["components"]>[number][];
    }, [data.type, data.name, data.dependsOn, manifest]);

    const hasConnectors = connectors.length > 0;
    const visibleSections = sections.filter(s => s.hasData);

    const noteClass = "flex h-full items-center justify-center text-sm italic";
    let fallbackBody: ReactNode;
    if (matchedAgent) {
        fallbackBody = <AgentView agent={matchedAgent} />;
    } else if (isAgent && agentsLoading) {
        fallbackBody = <div className={noteClass}>Loading agent details…</div>;
    } else {
        fallbackBody = <div className={noteClass}>No configuration data available</div>;
    }

    return (
        <Dialog open={isOpen} onOpenChange={open => !open && onClose()}>
            <DialogContent
                className="flex h-[80vh] !max-w-3xl flex-col gap-0 rounded-lg bg-(--background-w10) p-0"
                onInteractOutside={() => onClose()}
                onCloseAutoFocus={event => {
                    event.preventDefault();
                    openerRef.current?.focus();
                }}
            >
                <VisuallyHidden>
                    <DialogDescription>{label} configuration details</DialogDescription>
                </VisuallyHidden>
                <div className="border-b px-6 py-4">
                    <div className="flex items-center justify-between">
                        <div className="flex items-center gap-3">
                            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg" style={{ backgroundColor: `color-mix(in srgb, ${color} 15%, transparent)` }}>
                                <Icon className="h-4 w-4" style={{ color }} />
                            </div>
                            <DialogTitle title={data.name}>{humanizeComponentName(data.name)}</DialogTitle>
                            <span
                                className="rounded px-2 py-0.5 text-xs font-medium"
                                style={{
                                    backgroundColor: `color-mix(in srgb, ${color} 12%, transparent)`,
                                    color,
                                }}
                            >
                                {label}
                            </span>
                            <StatusBadge status={data.status} />
                        </div>
                        <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
                            <X className="h-5 w-5" />
                        </Button>
                    </div>
                </div>

                <div className="min-h-0 flex-1 overflow-auto px-6 py-4">
                    {workflowRole && (
                        <section className="pb-6">
                            <div className="mb-3 flex items-center gap-2">
                                <Workflow className="h-4 w-4 shrink-0" />
                                <h3 className="text-sm font-semibold">
                                    Role in workflow <span className="font-normal">· {workflowRole.workflowName}</span>
                                </h3>
                            </div>
                            {workflowRole.node ? (
                                <pre className="overflow-x-auto rounded-md bg-(--background-w20) p-3 font-mono text-xs leading-relaxed">{yaml.dump(workflowRole.node, { lineWidth: 100, noRefs: true })}</pre>
                            ) : (
                                <p className="text-xs italic">Loading workflow config…</p>
                            )}
                        </section>
                    )}
                    {config ? (
                        <div className={workflowRole ? "divide-y border-t pt-4" : "divide-y"}>
                            {visibleSections.map((s, i) => {
                                const SIcon = s.icon;
                                const insertConnectorsAfter = hasConnectors && s.id === "tools";
                                return (
                                    <Fragment key={s.id}>
                                        <section className={i === 0 ? "pb-6" : "py-6"}>
                                            <div className="mb-3 flex items-center gap-2">
                                                <SIcon className="h-4 w-4 shrink-0" />
                                                <h3 className="text-sm font-semibold">{s.label}</h3>
                                            </div>
                                            <YamlSectionRenderer data={config} keys={s.keys} />
                                        </section>
                                        {insertConnectorsAfter && (
                                            <section className="py-6">
                                                <div className="mb-3 flex items-center gap-2">
                                                    <Plug className="h-4 w-4 shrink-0" />
                                                    <h3 className="text-sm font-semibold">Connectors</h3>
                                                </div>
                                                <ConnectorsSection connectors={connectors} componentData={componentData} />
                                            </section>
                                        )}
                                    </Fragment>
                                );
                            })}
                            {hasConnectors && !visibleSections.some(s => s.id === "tools") && (
                                <section className="py-6">
                                    <div className="mb-3 flex items-center gap-2">
                                        <Plug className="h-4 w-4 shrink-0" />
                                        <h3 className="text-sm font-semibold">Connectors</h3>
                                    </div>
                                    <ConnectorsSection connectors={connectors} componentData={componentData} />
                                </section>
                            )}
                            {otherKeys.length > 0 && (
                                <section className="py-6">
                                    <div className="mb-3 flex items-center gap-2">
                                        <h3 className="text-sm font-semibold">Other</h3>
                                    </div>
                                    <YamlSectionRenderer data={config} keys={otherKeys} />
                                </section>
                            )}
                        </div>
                    ) : (
                        fallbackBody
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}

interface ConnectorInfo {
    name: string;
    icon?: string;
    description?: string;
    type?: string;
    subtype?: string;
    origin?: string;
}

function ConnectorsSection({ connectors, componentData }: { connectors: NonNullable<BuildManifest["components"]>[number][]; componentData?: Map<string, ComponentCanvasData> }) {
    const resolved: ConnectorInfo[] = connectors.map(conn => {
        const connData = componentData?.get(conn.name);
        const meta = connData?.connectorMetadata;
        let iconUrl: string | undefined;
        if (meta?.icon && meta.icon !== "other") {
            iconUrl = lookupConnectorIconByKey(meta.icon);
        }
        if (!iconUrl && conn.icon && conn.icon !== "other") {
            iconUrl = lookupConnectorIconByKey(conn.icon);
        }
        return {
            name: conn.name,
            icon: iconUrl,
            description: meta?.description,
            type: meta?.type,
            subtype: meta?.subtype,
            origin: conn.origin,
        };
    });

    return (
        <div className="space-y-4">
            <div>
                <h3 className="text-sm font-medium">Connectors</h3>
                <p className="mt-1 text-xs">Data connectors attached to this agent.</p>
            </div>
            <div className="space-y-3">
                {resolved.map(conn => (
                    <ConnectorCard key={conn.name} connector={conn} />
                ))}
            </div>
        </div>
    );
}

function ConnectorCard({ connector }: { connector: ConnectorInfo }) {
    const [imgError, setImgError] = useState(false);
    const hasIcon = connector.icon && !imgError;

    return (
        <div className="flex items-start gap-3 rounded-lg border p-4">
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg" style={{ backgroundColor: "color-mix(in srgb, var(--accent-n2-wMain) 15%, transparent)" }}>
                {hasIcon ? <img src={connector.icon} alt={connector.name} className="h-4 w-4 object-contain" onError={() => setImgError(true)} /> : <Plug className="h-4 w-4" style={{ color: "var(--accent-n2-wMain)" }} />}
            </div>
            <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                    <span className="text-sm font-medium">{connector.name}</span>
                    {connector.origin === "session" && (
                        <span className="rounded-full px-1.5 py-0.5 text-[9px] font-medium" style={{ backgroundColor: "color-mix(in srgb, var(--brand-wMain) 15%, transparent)", color: "var(--brand-wMain)" }}>
                            New
                        </span>
                    )}
                </div>
                {connector.description && <p className="mt-0.5 text-xs">{connector.description}</p>}
                {(connector.type || connector.subtype) && (
                    <div className="mt-1 text-[10px]">
                        {connector.type}
                        {connector.subtype ? ` · ${connector.subtype}` : ""}
                    </div>
                )}
            </div>
        </div>
    );
}
