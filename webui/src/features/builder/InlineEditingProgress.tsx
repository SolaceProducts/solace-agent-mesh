// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef, useState } from "react";
import { BookOpen, Bot, ChevronDown, ChevronRight, CircleCheckBig, Globe, Loader2, Play, Plug, Workflow } from "lucide-react";

import { TimelineRow } from "@/common/timeline/TimelineRow";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { componentTypeLabel } from "./builderUtils";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

type EditStatus = "editing" | "done" | "queued";

interface EditEntry {
    name: string;
    type: string;
    status: EditStatus;
}

function getTypeIcon(type: string) {
    switch (type) {
        case "workflow":
            return Workflow;
        case "connector":
            return Plug;
        case "gateway":
            return Globe;
        case "skill":
            return BookOpen;
        default:
            return Bot;
    }
}

function ComponentIcon({ type }: Readonly<{ type: string }>) {
    const Icon = getTypeIcon(type);
    return <Icon className="h-3.5 w-3.5 shrink-0 text-(--secondary-text-wMain)" />;
}

function StatusDot({ status }: Readonly<{ status: EditStatus }>) {
    return (
        <div className="relative z-10 flex h-5 w-5 flex-shrink-0 items-center justify-center">
            {status === "done" && (
                <>
                    <div className="absolute h-4 w-4 rounded-full bg-(--background-w10)" />
                    <div className="relative h-[10px] w-[10px] rounded-full bg-(--success-wMain)" />
                </>
            )}
            {status === "editing" && (
                <>
                    <div className="absolute h-4 w-4 rounded-full bg-(--background-w10)" />
                    <Loader2 className="relative h-[14px] w-[14px] animate-spin text-(--primary-wMain)" />
                </>
            )}
            {status === "queued" && (
                <>
                    <div className="absolute h-4 w-4 rounded-full bg-(--background-w10)" />
                    <div className="relative h-[10px] w-[10px] rounded-full bg-(--secondary-text-wMain)/20" />
                </>
            )}
        </div>
    );
}

function TimelineList({ entries, allDone }: Readonly<{ entries: EditEntry[]; allDone: boolean }>) {
    const rail = allDone ? "bg-(--success-wMain)/30" : "bg-(--secondary-text-wMain)/15";
    return (
        <div className="relative">
            {entries.map((e, index) => {
                const isActive = e.status === "editing";
                return (
                    <TimelineRow
                        key={e.name}
                        dot={<StatusDot status={e.status} />}
                        isFirst={index === 0}
                        isLast={index === entries.length - 1}
                        railClassName={rail}
                        animationDelayMs={Math.min(index * 50, 200)}
                        contentClassName={e.status === "queued" ? "opacity-50" : undefined}
                    >
                        <div className="flex min-w-0 items-center gap-2">
                            <ComponentIcon type={e.type} />
                            <span className={cn("text-sm leading-relaxed", isActive ? "font-medium text-(--primary-text-wMain)" : "text-(--secondary-text-wMain)")}>{e.name}</span>
                            <span className="text-[10px] text-(--secondary-text-wMain)/60">{componentTypeLabel(e.type)}</span>
                        </div>
                    </TimelineRow>
                );
            })}
        </div>
    );
}

interface Props {
    manifest: BuildManifest;
    componentData: Map<string, ComponentCanvasData>;
    isResponding: boolean;
    onSwitchToTest: () => void;
    isCollapsed?: boolean;
    onCollapsedChange?: (collapsed: boolean) => void;
}

export function InlineEditingProgress({ manifest, componentData, isResponding, onSwitchToTest, isCollapsed: controlledCollapsed, onCollapsedChange }: Readonly<Props>) {
    const touchedRef = useRef<Map<string, { type: string; doneOnce: boolean }>>(new Map());

    const sessionComps = (manifest.components ?? []).filter(c => c.origin !== "platform");
    for (const comp of sessionComps) {
        const cd = componentData.get(comp.name);
        const isEditing = !!cd?.isGenerating;
        const wasGenerating = cd?.isGenerating;
        const existing = touchedRef.current.get(comp.name);
        if (isEditing) {
            if (!existing) {
                touchedRef.current.set(comp.name, { type: comp.type, doneOnce: false });
            }
        } else if (existing && wasGenerating === false && !existing.doneOnce) {
            touchedRef.current.set(comp.name, { ...existing, doneOnce: true });
        }
    }

    const entries: EditEntry[] = [];
    for (const [name, info] of touchedRef.current) {
        const cd = componentData.get(name);
        const stillEditing = !!cd?.isGenerating;
        entries.push({
            name,
            type: info.type,
            status: stillEditing ? "editing" : "done",
        });
    }

    const editingCount = entries.filter(e => e.status === "editing").length;
    const totalCount = entries.length;
    const allDone = totalCount > 0 && editingCount === 0 && !isResponding;

    const [internalCollapsed, setInternalCollapsed] = useState(false);
    const isCollapsed = controlledCollapsed !== undefined ? controlledCollapsed : internalCollapsed;
    const setCollapsed = (v: boolean) => {
        if (onCollapsedChange) onCollapsedChange(v);
        else setInternalCollapsed(v);
    };
    const hasAutoCollapsed = useRef(false);
    useEffect(() => {
        if (!allDone || hasAutoCollapsed.current) return;
        hasAutoCollapsed.current = true;
        const t = setTimeout(() => setCollapsed(true), 1500);
        return () => clearTimeout(t);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [allDone]);

    if (totalCount === 0) return null;

    const headerLabel = allDone ? "Edits applied" : "Editing";

    if (isCollapsed) {
        return (
            <div className="mb-1 ml-2 flex items-center gap-1">
                <Button
                    variant="ghost"
                    size="sm"
                    className="flex h-auto items-center gap-1 rounded px-2 py-1 text-sm text-(--secondary-text-wMain) transition-colors hover:bg-(--secondary-w10) hover:text-(--primary-text-wMain)"
                    onClick={() => setCollapsed(false)}
                >
                    <ChevronRight className="h-3.5 w-3.5" />
                    {allDone && <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />}
                    <span className="font-medium">{headerLabel}</span>
                </Button>
                {totalCount > 0 && <span className="text-xs">{totalCount}</span>}
            </div>
        );
    }

    return (
        <div className="mb-1 ml-2">
            <div className="mb-1 flex items-center gap-2">
                {allDone ? (
                    <Button
                        variant="ghost"
                        size="sm"
                        className="flex h-auto items-center gap-1 rounded px-2 py-1 text-sm text-(--secondary-text-wMain) transition-colors hover:bg-(--secondary-w10) hover:text-(--primary-text-wMain)"
                        onClick={() => setCollapsed(true)}
                    >
                        <ChevronDown className="h-3.5 w-3.5" />
                        <CircleCheckBig className="h-4 w-4 text-(--success-wMain)" />
                        <span className="font-medium">{headerLabel}</span>
                    </Button>
                ) : (
                    <div className="flex items-center gap-2 px-2 py-1">
                        <Loader2 className="h-4 w-4 shrink-0 animate-spin text-(--primary-wMain)" />
                        <span className="text-sm font-semibold text-(--primary-text-wMain)">{headerLabel}</span>
                    </div>
                )}
                {totalCount > 0 && <span className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-(--primary-wMain) px-1.5 text-[11px] font-semibold text-(--background-w10)">{totalCount}</span>}
                <div className="flex-1" />
                {allDone && (
                    <Button size="sm" onClick={onSwitchToTest} className="shrink-0 gap-1.5">
                        <Play className="h-3.5 w-3.5" />
                        Go to Test Mode
                    </Button>
                )}
            </div>

            <TimelineList entries={entries} allDone={allDone} />
        </div>
    );
}
