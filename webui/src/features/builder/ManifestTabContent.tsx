// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BookOpen, Bot, Globe, Layers, Package, Plug, Variable, Workflow } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { MarkdownWrapper } from "@/common";

import { componentTypeLabel, humanizeComponentName } from "./builderUtils";
import type { BuildManifest } from "./hooks/useBuilderManifest";

export function allComponentsReady(manifest: BuildManifest): boolean {
    const components = manifest.components ?? [];
    if (components.length === 0) return false;
    return components.every(c => c.status === "validated" || c.status === "deployed" || c.origin === "platform");
}

const COMPONENT_TYPE_ORDER: Record<string, number> = {
    gateway: 0,
    workflow: 1,
    agent: 2,
    skill: 3,
    connector: 4,
};

function sortComponents(components: NonNullable<BuildManifest["components"]>) {
    return [...components].sort((a, b) => (COMPONENT_TYPE_ORDER[a.type] ?? 99) - (COMPONENT_TYPE_ORDER[b.type] ?? 99));
}

export function ManifestTabContent({ manifest }: { manifest: BuildManifest }) {
    const displayName = manifest.display_name || manifest.name || "Untitled Build";
    const components = sortComponents(manifest.components ?? []);
    const envVars = manifest.env_vars ?? [];

    return (
        <div className="h-full overflow-y-auto p-4">
            <div className="space-y-5">
                <div>
                    <h2 className="text-base font-semibold">{displayName}</h2>
                    {manifest.description && <p className="mt-1 text-sm leading-relaxed">{manifest.description}</p>}
                </div>

                {manifest.architecture && (
                    <div>
                        <div className="mb-1.5 flex items-center gap-1.5 text-xs font-semibold tracking-wide uppercase">
                            <Layers className="h-3.5 w-3.5" />
                            Architecture
                        </div>
                        <div className="rounded-md px-3 py-2">
                            <div className="prose-sm prose-muted text-sm leading-relaxed">
                                <MarkdownWrapper content={manifest.architecture.trim()} />
                            </div>
                        </div>
                    </div>
                )}

                {components.length > 0 && (
                    <div>
                        <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold tracking-wide uppercase">
                            <Package className="h-3.5 w-3.5" />
                            Components ({components.length})
                        </div>
                        <div className="space-y-1.5">
                            {components.map(comp => {
                                const Icon = comp.type === "workflow" ? Workflow : comp.type === "connector" ? Plug : comp.type === "gateway" ? Globe : comp.type === "skill" ? BookOpen : Bot;
                                const displayStatus = comp.origin === "platform" ? "deployed" : comp.status;
                                return (
                                    <div key={comp.filename || comp.name} className="flex items-start gap-2.5 rounded-md px-3 py-2">
                                        <Icon className="mt-0.5 h-4 w-4 shrink-0" />
                                        <div className="min-w-0 flex-1">
                                            <div className="flex items-center gap-2">
                                                <span className="text-sm font-medium" title={comp.name}>
                                                    {humanizeComponentName(comp.name)}
                                                </span>
                                                <span className="text-[10px]">{componentTypeLabel(comp.type)}</span>
                                                {displayStatus && (
                                                    <span
                                                        className={cn(
                                                            "rounded px-1 py-0.5 text-[9px] font-medium",
                                                            displayStatus === "validated"
                                                                ? "bg-(--success-w10) text-(--success-wMain)"
                                                                : displayStatus === "deployed"
                                                                  ? "bg-(--info-w10) text-(--info-wMain)"
                                                                  : displayStatus === "error"
                                                                    ? "bg-(--error-w10) text-(--error-wMain)"
                                                                    : ""
                                                        )}
                                                    >
                                                        {displayStatus}
                                                    </span>
                                                )}
                                            </div>
                                            {comp.description && <p className="mt-0.5 text-xs leading-relaxed">{comp.description}</p>}
                                            {comp.depends_on && comp.depends_on.length > 0 && <p className="mt-1 text-[10px]">Depends on: {comp.depends_on.join(", ")}</p>}
                                            {comp.tools && comp.tools.length > 0 && <p className="mt-0.5 text-[10px]">Tools: {comp.tools.join(", ")}</p>}
                                        </div>
                                    </div>
                                );
                            })}
                        </div>
                    </div>
                )}

                {envVars.length > 0 && (
                    <div>
                        <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold tracking-wide uppercase">
                            <Variable className="h-3.5 w-3.5" />
                            Environment Variables ({envVars.length})
                        </div>
                        <div className="space-y-1">
                            {envVars.map(v => (
                                <div key={v.name} className="rounded-md px-3 py-2">
                                    <div className="flex items-center gap-2">
                                        <code className="text-xs font-medium">{v.name}</code>
                                        {v.required && <span className="text-[9px] font-medium text-(--error-wMain)">required</span>}
                                        {v.default && <span className="text-[10px]">default: {v.default}</span>}
                                    </div>
                                    {v.description && <p className="mt-0.5 text-xs">{v.description}</p>}
                                </div>
                            ))}
                        </div>
                    </div>
                )}
            </div>
        </div>
    );
}
