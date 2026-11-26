// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";
import { BookOpen, Bot, Circle, CircleCheckBig, CirclePause, Globe, Loader2, MinusCircle, Plug, Workflow, XCircle } from "lucide-react";

import type { BuilderComponentProgress, BuilderComponentState } from "@/types/builder";

const COMPONENT_TYPE_ORDER = ["gateway", "workflow", "agent", "skill", "connector", "a2a_agent"] as const;

const COMPONENT_TYPE_LABELS: Record<string, string> = {
    gateway: "Entrypoint",
    workflow: "Workflow",
    agent: "Agent",
    skill: "Skill",
    connector: "Connector",
    a2a_agent: "Remote Agent",
};

const COMPONENT_ICON_MAP: Record<string, typeof Bot> = {
    workflow: Workflow,
    connector: Plug,
    gateway: Globe,
    skill: BookOpen,
    agent: Bot,
    a2a_agent: Globe,
};

function StateIcon({ state, className = "h-4 w-4" }: { state: BuilderComponentState; className?: string }) {
    switch (state) {
        case "completed":
            return <CircleCheckBig className={`${className} text-(--success-wMain)`} />;
        case "active":
            return <Loader2 className={`${className} animate-spin text-(--primary-wMain)`} />;
        case "failed":
            return <XCircle className={`${className}`} />;
        case "skipped":
            return <MinusCircle className={`${className} opacity-60`} />;
        case "input-required":
            return <CirclePause className={`${className} text-(--warning-wMain)`} />;
        case "queued":
        default:
            return <Circle className={`${className} opacity-40`} />;
    }
}

export interface BuilderProgressCardProps {
    components: BuilderComponentProgress[];
    isBuilding: boolean;
    buildName?: string;
    onComponentClick?: (componentId: string) => void;
}

export const BuilderProgressCard: React.FC<BuilderProgressCardProps> = ({ components, isBuilding, buildName, onComponentClick }) => {
    const groupedComponents = useMemo(() => {
        const groups: Array<{ type: string; items: BuilderComponentProgress[] }> = [];
        for (const type of COMPONENT_TYPE_ORDER) {
            const items = components.filter(c => c.type === type);
            if (items.length > 0) {
                groups.push({ type, items });
            }
        }
        return groups;
    }, [components]);

    const completedCount = components.filter(c => c.state === "completed").length;
    const totalCount = components.length;
    const hasFailed = components.some(c => c.state === "failed");
    const activeComponent = components.find(c => c.state === "active" || c.state === "input-required");

    const headerText = useMemo(() => {
        if (isBuilding) {
            return buildName ? `Creating: ${buildName}` : "Creating components...";
        }
        if (hasFailed) {
            return buildName ? `Build failed: ${buildName}` : "Build failed";
        }
        return buildName ? `Build complete: ${buildName}` : "Build complete";
    }, [isBuilding, hasFailed, buildName]);

    const headerIcon = useMemo(() => {
        if (isBuilding) return <Loader2 className="h-4 w-4 animate-spin text-(--primary-wMain)" />;
        if (hasFailed) return <XCircle className="h-4 w-4" />;
        return <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />;
    }, [isBuilding, hasFailed]);

    return (
        <div className="flex max-w-[480px] min-w-[300px] flex-col gap-2 rounded-lg border px-4 py-3" style={{ backgroundColor: "var(--background-w10)", borderColor: "var(--secondary-w8040)" }}>
            <div className="flex items-center gap-2.5">
                {headerIcon}
                <span className="truncate text-sm font-semibold">{headerText}</span>
            </div>

            {totalCount > 0 && (
                <div className="flex items-center gap-2">
                    <div className="h-1.5 flex-1 overflow-hidden rounded-full">
                        <div className="h-full rounded-full bg-(--success-wMain) transition-all duration-500 ease-out" style={{ width: `${totalCount > 0 ? (completedCount / totalCount) * 100 : 0}%` }} />
                    </div>
                    <span className="text-xs">
                        {completedCount}/{totalCount}
                    </span>
                </div>
            )}

            <div className="flex flex-col gap-1">
                {groupedComponents.map(group =>
                    group.items.map(component => {
                        const TypeIcon = COMPONENT_ICON_MAP[component.type] || Bot;
                        const isClickable = component.state === "completed" && !!onComponentClick;
                        const isActive = component.state === "active" || component.state === "input-required";

                        return (
                            <div
                                key={component.id}
                                className={`flex items-start gap-2 rounded-md px-2 py-1.5 transition-colors ${isClickable ? "cursor-pointer" : ""}`}
                                onClick={isClickable ? () => onComponentClick(component.id) : undefined}
                                role={isClickable ? "button" : undefined}
                                tabIndex={isClickable ? 0 : undefined}
                                onKeyDown={
                                    isClickable
                                        ? e => {
                                              if (e.key === "Enter" || e.key === " ") {
                                                  e.preventDefault();
                                                  onComponentClick(component.id);
                                              }
                                          }
                                        : undefined
                                }
                            >
                                <div className="mt-0.5 flex-shrink-0">
                                    <StateIcon state={component.state} />
                                </div>

                                <div className="min-w-0 flex-1">
                                    <div className="flex items-center gap-1.5">
                                        <TypeIcon className="h-3.5 w-3.5 flex-shrink-0" />
                                        <span className="truncate text-sm">{component.name}</span>
                                        <span className="text-xs">{COMPONENT_TYPE_LABELS[component.type] || component.type}</span>
                                    </div>

                                    {isActive && component.statusText && <p className="mt-0.5 truncate text-xs">{component.statusText}</p>}

                                    {component.state === "active" && component.type === "skill" && component.progressPercent != null && (
                                        <div className="mt-1 h-1 overflow-hidden rounded-full">
                                            <div className="h-full rounded-full bg-(--primary-wMain) transition-all duration-300 ease-out" style={{ width: `${component.progressPercent}%` }} />
                                        </div>
                                    )}
                                </div>
                            </div>
                        );
                    })
                )}
            </div>

            {isBuilding && activeComponent && !activeComponent.statusText && <p className="truncate text-xs">{activeComponent.state === "input-required" ? "Waiting for input..." : `Working on ${activeComponent.name}...`}</p>}
        </div>
    );
};
