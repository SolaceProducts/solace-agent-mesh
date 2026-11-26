// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { BookOpen, Bot, ChevronDown, ChevronRight, CircleCheckBig, FlaskConical, Globe, Loader2, Plug, Workflow } from "lucide-react";

import { Button } from "@/ui";

import type { BuildManifest, BuildManifestComponent } from "./useBuildPlanArtifact";

const COMPONENT_TYPE_ORDER = ["gateway", "workflow", "agent", "skill", "connector"] as const;

const COMPONENT_ICON_MAP: Record<string, typeof Bot> = {
    workflow: Workflow,
    connector: Plug,
    gateway: Globe,
    skill: BookOpen,
    agent: Bot,
};

const COMPONENT_TYPE_LABELS: Record<string, string> = {
    gateway: "Entrypoint",
    workflow: "Workflow",
    agent: "Agent",
    skill: "Skill",
    connector: "Connector",
};

function stopPropagation(e: React.MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
}

export function getTestTarget(manifest: BuildManifest | null): { name: string; type: "agent" | "workflow"; displayName: string } | null {
    if (!manifest?.components) return null;
    const sessionComponents = manifest.components.filter(c => c.origin !== "platform");
    const workflow = sessionComponents.find(c => c.type === "workflow");
    if (workflow?.name) return { name: workflow.name, type: "workflow", displayName: manifest.display_name || workflow.name };
    const agent = sessionComponents.find(c => c.type === "agent");
    if (agent?.name) return { name: agent.name, type: "agent", displayName: manifest.display_name || agent.name };
    return null;
}

export interface ApprovedPlanTimelineProps {
    manifest: BuildManifest | null;
    sortedComponents: BuildManifestComponent[];
    isActivelyBuilding: boolean;
    onTestRequest?: (targetName: string, targetType: "agent" | "workflow", displayName: string) => void;
    isResponding: boolean;
}

export const ApprovedPlanTimeline: React.FC<ApprovedPlanTimelineProps> = ({ manifest, sortedComponents, isActivelyBuilding, onTestRequest, isResponding }) => {
    const [planExpanded, setPlanExpanded] = useState(false);
    const testTarget = getTestTarget(manifest);

    return (
        <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
                <Button
                    type="button"
                    variant="ghost"
                    className="flex items-center gap-1 text-sm text-(--secondary-text-wMain) transition-colors hover:text-(--primary-text-wMain)"
                    onClick={e => {
                        stopPropagation(e);
                        setPlanExpanded(!planExpanded);
                    }}
                >
                    {planExpanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                    <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />
                    <span className="font-medium">Approved Action Plan</span>
                </Button>
            </div>

            {planExpanded && (
                <div className="ml-6 flex w-[480px] max-w-full flex-col gap-2 rounded-lg border px-4 py-3" style={{ backgroundColor: "var(--background-w10)", borderColor: "var(--secondary-w8040)" }}>
                    {manifest?.description && <p className="text-sm leading-relaxed">{manifest.description}</p>}
                    {sortedComponents.length > 0 && (
                        <div className="flex flex-wrap items-center gap-3 pt-0.5">
                            {COMPONENT_TYPE_ORDER.filter(t => sortedComponents.some(c => c.type === t)).map(t => {
                                const Icon = COMPONENT_ICON_MAP[t] || Bot;
                                const count = sortedComponents.filter(c => c.type === t).length;
                                return (
                                    <span key={t} className="flex items-center gap-1" title={`${count} ${t}${count > 1 ? "s" : ""}`}>
                                        <Icon className="h-3.5 w-3.5" />
                                        <span className="text-xs">
                                            {count} {COMPONENT_TYPE_LABELS[t] || t}
                                            {count > 1 ? "s" : ""}
                                        </span>
                                    </span>
                                );
                            })}
                        </div>
                    )}
                </div>
            )}

            {isActivelyBuilding && (
                <div>
                    <div className="mb-1 px-2 py-1 text-sm font-medium text-(--secondary-text-wMain)">Creating...</div>
                    <div className="relative">
                        {sortedComponents.length > 1 && <div className="absolute top-[21px] left-[9px] z-0 w-[2px] rounded-full bg-current opacity-30" style={{ bottom: "21px" }} />}
                        {sortedComponents.map((comp, idx) => {
                            const Icon = COMPONENT_ICON_MAP[comp.type || "agent"] || Bot;
                            const isLast = idx === sortedComponents.length - 1;
                            const showSpinner = isLast;
                            return (
                                <div
                                    key={idx}
                                    className="flex items-start gap-3 py-2"
                                    style={{
                                        animation: "progressSlideIn 0.3s ease-out both",
                                        animationDelay: `${Math.min(idx * 50, 200)}ms`,
                                    }}
                                >
                                    <div className="relative z-10 flex h-5 w-5 flex-shrink-0 items-center justify-center">
                                        {showSpinner ? <Loader2 className="h-[14px] w-[14px] animate-spin text-(--primary-wMain)" /> : <div className="h-[10px] w-[10px] rounded-full bg-(--success-wMain)" />}
                                    </div>
                                    <div className="flex min-w-0 items-center gap-1.5">
                                        <Icon className="h-3.5 w-3.5 flex-shrink-0" />
                                        <span className={`text-sm leading-relaxed ${showSpinner ? "text-(--primary-text-wMain)" : "text-(--secondary-text-wMain)"}`}>{comp.name || "Unnamed"}</span>
                                        <span className="text-xs">{COMPONENT_TYPE_LABELS[comp.type || "agent"]}</span>
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                </div>
            )}

            {!isActivelyBuilding && (
                <div className="flex items-center gap-3 px-2">
                    <span className="text-xs italic">All components created.</span>
                    {onTestRequest && testTarget && !isResponding && (
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="gap-1.5 text-xs"
                            onClick={e => {
                                stopPropagation(e);
                                onTestRequest(testTarget.name, testTarget.type, testTarget.displayName);
                            }}
                        >
                            <FlaskConical className="h-3 w-3" />
                            Test {testTarget.type === "workflow" ? "Workflow" : "Agent"}
                        </Button>
                    )}
                </div>
            )}
        </div>
    );
};
