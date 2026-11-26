// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useState } from "react";
import { Bot, ChevronRight, Workflow } from "lucide-react";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { AgentBuilderPreview } from "./AgentBuilderPreview";
import { BuilderActions } from "./BuilderActions";
import { BuilderComponentSelector } from "./BuilderComponentSelector";
import { type ConfigType, extractConfigName } from "./builderUtils";
import type { BuilderConfigState } from "./hooks/useBuilderConfig";
import type { BuilderManifestState } from "./hooks/useBuilderManifest";
import { WorkflowBuilderPreview } from "./WorkflowBuilderPreview";

type BuilderView = { type: "root" } | { type: "agent"; agentName: string; yamlText: string };

const ROOT_TYPE_LABEL: Partial<Record<ConfigType, string>> = { workflow: "Workflow", agent: "Agent" };

interface BuilderPanelProps {
    config: BuilderConfigState;
    manifest: BuilderManifestState;
    findConfigByAgentName: (agentName: string) => Promise<string | null>;
    userAgentsRefetch?: () => Promise<void>;
}

export function BuilderPanel({ config, manifest, findConfigByAgentName, userAgentsRefetch }: BuilderPanelProps) {
    const { configYaml, configType, isLoading } = config;
    const [viewStack, setViewStack] = useState<BuilderView[]>([{ type: "root" }]);

    const currentView = viewStack[viewStack.length - 1];

    const handleAgentDrillDown = useCallback(
        async (agentName: string) => {
            const yamlText = await findConfigByAgentName(agentName);
            if (!yamlText) return;
            setViewStack(prev => [...prev, { type: "agent", agentName, yamlText }]);
        },
        [findConfigByAgentName]
    );

    const handleBreadcrumbClick = useCallback((index: number) => {
        setViewStack(prev => prev.slice(0, index + 1));
    }, []);

    if (isLoading) {
        return (
            <div className="builder-blueprint flex h-full items-center justify-center">
                <p className="text-sm">Loading configuration...</p>
            </div>
        );
    }

    if (!configYaml) {
        return (
            <div className="builder-blueprint flex h-full items-center justify-center p-4 text-center">
                <div>
                    <Workflow className="mx-auto mb-3 h-10 w-10" />
                    <p className="text-sm font-medium">No configuration yet</p>
                    <p className="mt-1 text-xs">The builder will show a preview here once it generates a configuration.</p>
                </div>
            </div>
        );
    }

    const rootName = extractConfigName(configYaml);
    const RootTypeIcon = configType === "workflow" ? Workflow : Bot;
    const rootTypeLabel = ROOT_TYPE_LABEL[configType] ?? "Config";

    const isDrilledIn = currentView.type !== "root";
    const activeYaml = currentView.type === "agent" ? currentView.yamlText : configYaml;
    const activeType = currentView.type === "agent" ? ("agent" as const) : configType;

    return (
        <div className="flex h-full flex-col">
            <div className="border-b px-3 py-2">
                <div className="flex items-center gap-1.5">
                    {isDrilledIn ? (
                        <>
                            {viewStack.map((view, i) => {
                                const isLast = i === viewStack.length - 1;
                                const viewName = view.type === "root" ? rootName : view.agentName;
                                const ViewIcon = view.type === "root" ? RootTypeIcon : Bot;

                                return (
                                    <span key={i} className="flex items-center gap-1.5">
                                        {i > 0 && <ChevronRight className="h-3 w-3 shrink-0" />}
                                        {isLast ? (
                                            <span className="flex items-center gap-1.5">
                                                <ViewIcon className="h-4 w-4 shrink-0" />
                                                <span className="min-w-0 truncate text-sm font-medium">{viewName}</span>
                                                <span
                                                    className={cn(
                                                        "shrink-0 rounded px-1.5 py-0.5 text-xs font-medium",
                                                        view.type === "root" && configType === "workflow" ? "bg-(--info-w10) text-(--info-wMain)" : "bg-(--success-w10) text-(--success-wMain)"
                                                    )}
                                                >
                                                    {view.type === "root" ? rootTypeLabel : "Agent"}
                                                </span>
                                            </span>
                                        ) : (
                                            <Button variant="ghost" size="sm" onClick={() => handleBreadcrumbClick(i)} className="flex h-auto items-center gap-1.5 px-0 py-0 transition-colors">
                                                <ViewIcon className="h-3.5 w-3.5 shrink-0" />
                                                <span className="min-w-0 truncate text-xs">{viewName}</span>
                                            </Button>
                                        )}
                                    </span>
                                );
                            })}
                        </>
                    ) : (
                        <>
                            <RootTypeIcon className="h-4 w-4 shrink-0" />
                            <span className="min-w-0 truncate text-sm font-medium">{rootName}</span>
                            <span className={cn("shrink-0 rounded px-1.5 py-0.5 text-xs font-medium", configType === "workflow" ? "bg-(--info-w10) text-(--info-wMain)" : "bg-(--success-w10) text-(--success-wMain)")}>{rootTypeLabel}</span>
                        </>
                    )}
                </div>
            </div>

            {!isDrilledIn && manifest.manifest && <BuilderComponentSelector manifest={manifest.manifest} />}

            <div className="builder-blueprint min-h-0 flex-1">
                {activeType === "workflow" ? <WorkflowBuilderPreview yamlText={activeYaml} onAgentDrillDown={handleAgentDrillDown} /> : activeType === "agent" ? <AgentBuilderPreview yamlText={activeYaml} /> : <UnknownConfigPreview />}
            </div>

            {!isDrilledIn && (configType === "agent" || configType === "workflow" || configType === "skill") && (
                <BuilderActions configYaml={configYaml} configType={configType} manifest={manifest.manifest} saveManifest={manifest.saveManifest} userAgentsRefetch={userAgentsRefetch} />
            )}
        </div>
    );
}

function UnknownConfigPreview() {
    return (
        <div className="flex h-full items-center justify-center p-4 text-center">
            <p className="text-sm">Unknown configuration type. The builder may still be working on it.</p>
        </div>
    );
}
