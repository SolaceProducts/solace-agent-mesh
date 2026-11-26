// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";
import { BookOpen, Bot, Globe, Plug, Workflow } from "lucide-react";

import { Spinner } from "@/ui/spinner";

import type { BuildManifest, BuildManifestComponent } from "./useBuildPlanArtifact";

const COMPONENT_TYPE_ORDER = ["gateway", "workflow", "agent", "skill", "connector"] as const;

const COMPONENT_ICON_MAP: Record<string, typeof Bot> = {
    workflow: Workflow,
    connector: Plug,
    gateway: Globe,
    skill: BookOpen,
    agent: Bot,
};

function componentCounts(components: BuildManifestComponent[] | undefined): Record<string, number> {
    if (!components || components.length === 0) return {};
    const counts: Record<string, number> = {};
    for (const comp of components) {
        const t = comp.type || "agent";
        counts[t] = (counts[t] || 0) + 1;
    }
    return counts;
}

export interface StreamingPlanCardProps {
    manifest: BuildManifest | null;
}

export const StreamingPlanCard: React.FC<StreamingPlanCardProps> = ({ manifest }) => {
    const counts = useMemo(() => componentCounts(manifest?.components), [manifest?.components]);
    const hasComponents = Object.keys(counts).length > 0;
    const name = manifest?.display_name || manifest?.name;

    return (
        <div className="flex max-w-[420px] min-w-[280px] flex-col gap-2 rounded-lg border px-4 py-3" style={{ backgroundColor: "var(--background-w10)", borderColor: "var(--secondary-w8040)" }}>
            <div className="flex items-center gap-2.5">
                <Spinner size="small" variant="primary" />
                <span className="truncate text-sm font-semibold">{name ? `Creating build plan: ${name}` : "Creating build plan..."}</span>
            </div>
            {manifest?.description && <p className="line-clamp-2 text-xs leading-relaxed">{manifest.description}</p>}
            {hasComponents && (
                <div className="flex items-center gap-3 pt-0.5">
                    {COMPONENT_TYPE_ORDER.filter(t => counts[t]).map(t => {
                        const Icon = COMPONENT_ICON_MAP[t] || Bot;
                        return (
                            <span key={t} className="flex items-center gap-1" title={`${counts[t]} ${t}${counts[t] > 1 ? "s" : ""}`}>
                                <Icon className="h-3.5 w-3.5" />
                                {counts[t] > 1 && <span className="text-[10px]">{counts[t]}</span>}
                            </span>
                        );
                    })}
                </div>
            )}
        </div>
    );
};
