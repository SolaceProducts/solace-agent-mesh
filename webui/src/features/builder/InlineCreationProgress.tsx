// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronRight, CircleCheckBig, Loader2, Play } from "lucide-react";

import { ViewWorkflowButton } from "@/common/buttons/ViewWorkflowButton";
import { HOVER_REVEAL, LiveActivityLine } from "@/common/timeline/LiveActivityLine";

import { cn } from "@/utils/cnTailwind";

import { Button } from "@/ui/button";

import { dedupeConsecutiveUpdates } from "@/features/chat/activityIndicator";
import { InlineProgressUpdates } from "@/features/chat/InlineProgressUpdates";

import type { ProgressUpdate } from "@/types";

import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";
import { areWorkflowChildAgentsDone, isBuildStopped } from "./workflowChildStatus";

const COMPONENT_TYPE_ORDER: Record<string, number> = {
    connector: 0,
    skill: 1,
    gateway: 2,
    agent: 3,
    workflow: 4,
};

type CreationStatus = "done" | "generating" | "validating" | "error" | "queued" | "waiting";

interface ComponentProgress {
    type: string;
    status: CreationStatus;
    children?: ComponentProgress[];
}

function deriveCreationStatus(
    comp: NonNullable<BuildManifest["components"]>[number],
    canvasData: ComponentCanvasData | undefined,
    _isResponding: boolean,
    _hasAnyGenerating: boolean,
    manifest?: BuildManifest,
    componentData?: Map<string, ComponentCanvasData>
): CreationStatus {
    if (comp.origin === "platform") return "done";

    if (canvasData?.isWaitingForInput) return "waiting";

    if (canvasData?.isGenerating) return "generating";

    if (comp.status === "error") return "error";

    const hasSelfContent = comp.status === "validated" || comp.status === "deployed" || !!canvasData?.yamlText;

    if (comp.type === "workflow" && hasSelfContent && manifest && componentData) {
        const childAgents = (comp.nodes ?? []).filter(n => n.type === "agent" && n.agent_name);
        const allChildrenDone = areWorkflowChildAgentsDone(
            childAgents.map(n => n.agent_name!),
            componentData,
            manifest
        );
        if (!allChildrenDone) return "generating";
    }

    if (hasSelfContent) return "done";
    return "queued";
}

interface InlineCreationProgressProps {
    manifest: BuildManifest;
    componentData: Map<string, ComponentCanvasData>;
    isResponding: boolean;
    isCollapsed?: boolean;
    onCollapsedChange?: (collapsed: boolean) => void;
    activityUpdates?: ProgressUpdate[];
    activityActive?: boolean;
    onViewWorkflow?: () => void;
    isInterrupted?: boolean;
    onResume?: () => void;
}

const HEADER_BUTTON_CLASS = "flex h-auto items-center gap-1 rounded px-2 py-1 text-sm text-(--secondary-text-wMain) transition-colors hover:bg-(--secondary-w10) hover:text-(--primary-text-wMain)";

interface TimelineHeaderProps {
    isLive: boolean;
    allSettled: boolean;
    allDone: boolean;
    spinner: ReactNode;
    onCollapseDetail: () => void;
    onCollapseBlock: () => void;
}

function TimelineHeader({ isLive, allSettled, allDone, spinner, onCollapseDetail, onCollapseBlock }: Readonly<TimelineHeaderProps>) {
    if (isLive) {
        return (
            <Button variant="ghost" size="sm" className={HEADER_BUTTON_CLASS} onClick={onCollapseDetail} aria-expanded={true}>
                <ChevronDown className="h-3.5 w-3.5" />
                {spinner}
                <span className="font-medium">Activity Timeline</span>
            </Button>
        );
    }
    if (allSettled) {
        return (
            <Button variant="ghost" size="sm" className={HEADER_BUTTON_CLASS} onClick={onCollapseBlock} aria-expanded={true}>
                <ChevronDown className="h-3.5 w-3.5" />
                {allDone && <CircleCheckBig className="h-4 w-4" style={{ color: "var(--success-wMain)" }} />}
                <span className="font-medium">Activity Timeline</span>
            </Button>
        );
    }
    return (
        <span className="flex items-center gap-1 px-2 py-1 text-sm font-medium text-(--secondary-text-wMain)">
            {spinner}
            Activity Timeline
        </span>
    );
}

function deriveCompletion(args: { doneCount: number; errorCount: number; totalCount: number; buildStopped: boolean; hasWaiting: boolean; isResponding: boolean; hostSaysInterrupted: boolean }) {
    const rowsAllDone = args.doneCount === args.totalCount && args.totalCount > 0;
    const rowsAllSettled = args.doneCount + args.errorCount === args.totalCount && args.totalCount > 0;
    const isInterrupted = args.hostSaysInterrupted || (!args.isResponding && (args.buildStopped || (!args.hasWaiting && !rowsAllSettled && args.doneCount > 0)));
    return { allDone: rowsAllDone && !isInterrupted, allSettled: rowsAllSettled && !isInterrupted, isInterrupted };
}

function BuildChrome({ statusSuffix, isInterrupted, allDone, errorCount }: Readonly<{ statusSuffix: string | null; isInterrupted: boolean; allDone: boolean; errorCount: number }>) {
    return (
        <>
            {statusSuffix && <span className={cn("text-xs", isInterrupted ? "text-(--warning-wMain)" : "")}>{statusSuffix}</span>}
            {!allDone && errorCount > 0 && <span className="text-xs text-(--warning-wMain)">{`${errorCount} error${errorCount > 1 ? "s" : ""}`}</span>}
        </>
    );
}

function deriveStatusSuffix(hasWaiting: boolean, errorCount: number, isInterrupted: boolean): string | null {
    if (hasWaiting) return "Waiting for input";
    if (errorCount > 0) return "Fixing errors";
    if (isInterrupted) return "Build interrupted";
    return null;
}

function ownedConnectorsOf(agentName: string, manifest: BuildManifest, ownedNames: Set<string>): string[] {
    const agentComp = manifest.components?.find(c => c.name === agentName);
    const connNames = new Set([...(agentComp?.depends_on ?? []), ...(agentComp?.connectors ?? [])]);
    const result: string[] = [];
    for (const cn of connNames) {
        const connComp = manifest.components?.find(c => c.name === cn);
        if (connComp?.type === "connector" && connComp.origin !== "platform" && !ownedNames.has(cn)) {
            ownedNames.add(cn);
            result.push(cn);
        }
    }
    return result;
}

function collectWorkflowChildren(manifest: BuildManifest): { ownedNames: Set<string>; childMap: Map<string, string[]> } {
    const ownedNames = new Set<string>();
    const childMap = new Map<string, string[]>();
    for (const comp of manifest.components ?? []) {
        if (comp.type !== "workflow" || comp.origin === "platform") continue;
        const childNames: string[] = [];
        for (const node of comp.nodes ?? []) {
            if (node.type !== "agent" || !node.agent_name) continue;
            ownedNames.add(node.agent_name);
            childNames.push(...ownedConnectorsOf(node.agent_name, manifest, ownedNames), node.agent_name);
        }
        if (childNames.length > 0) childMap.set(comp.name, childNames);
    }
    return { ownedNames, childMap };
}

function buildComponentProgressList(manifest: BuildManifest, componentData: Map<string, ComponentCanvasData>, isResponding: boolean, hasAnyGenerating: boolean): ComponentProgress[] {
    const { ownedNames, childMap } = collectWorkflowChildren(manifest);
    const seen = new Set<string>();
    const makeProgress = (comp: NonNullable<BuildManifest["components"]>[number]): ComponentProgress => ({
        type: comp.type,
        status: deriveCreationStatus(comp, componentData.get(comp.name), isResponding, hasAnyGenerating, manifest, componentData),
    });

    const components: ComponentProgress[] = [];
    for (const comp of manifest.components ?? []) {
        if (comp.origin === "platform" || seen.has(comp.name) || ownedNames.has(comp.name)) continue;
        seen.add(comp.name);
        const entry = makeProgress(comp);
        if (comp.type === "workflow" && childMap.has(comp.name)) {
            entry.children = childMap
                .get(comp.name)!
                .map(name => manifest.components?.find(c => c.name === name))
                .filter((c): c is NonNullable<BuildManifest["components"]>[number] => !!c && !seen.has(c.name))
                .map(c => {
                    seen.add(c.name);
                    return makeProgress(c);
                });
        }
        components.push(entry);
    }
    components.sort((a, b) => (COMPONENT_TYPE_ORDER[a.type] ?? 99) - (COMPONENT_TYPE_ORDER[b.type] ?? 99));
    return components;
}

export function InlineCreationProgress({
    manifest,
    componentData,
    isResponding,
    isCollapsed: controlledCollapsed,
    onCollapsedChange,
    activityUpdates,
    activityActive,
    onViewWorkflow,
    isInterrupted: hostSaysInterrupted = false,
    onResume,
}: Readonly<InlineCreationProgressProps>) {
    const [internalOpen, setInternalOpen] = useState(true);
    const isTimelineOpen = controlledCollapsed !== undefined ? !controlledCollapsed : internalOpen;
    const setIsTimelineOpen = (open: boolean) => {
        if (onCollapsedChange) {
            onCollapsedChange(!open);
        } else {
            setInternalOpen(open);
        }
    };
    const hasAutoCollapsed = useRef(false);

    const [isLiveDetailOpen, setIsLiveDetailOpen] = useState(false);
    const wasActivityActive = useRef(false);

    const hasAnyGenerating = Array.from(componentData.values()).some(d => d.isGenerating);

    const components = buildComponentProgressList(manifest, componentData, isResponding, hasAnyGenerating);

    const allFlat = components.flatMap(c => [c, ...(c.children ?? [])]);
    const doneCount = allFlat.filter(c => c.status === "done").length;
    const errorCount = allFlat.filter(c => c.status === "error").length;
    const totalCount = allFlat.length;
    const hasWaiting = allFlat.some(c => c.status === "waiting");
    const { allDone, allSettled, isInterrupted } = deriveCompletion({ doneCount, errorCount, totalCount, buildStopped: isBuildStopped(componentData.values()), hasWaiting, isResponding, hostSaysInterrupted });

    const statusSuffix = deriveStatusSuffix(hasWaiting, errorCount, isInterrupted);

    useEffect(() => {
        if (!allDone && totalCount > 0 && !isTimelineOpen) {
            setIsTimelineOpen(true);
        }
    }, [allDone, totalCount, isInterrupted]);

    useEffect(() => {
        if (activityActive && !wasActivityActive.current) setIsLiveDetailOpen(false);
        wasActivityActive.current = !!activityActive;
    }, [activityActive]);

    useEffect(() => {
        if (allDone && !isResponding && !hasAutoCollapsed.current && totalCount > 0) {
            hasAutoCollapsed.current = true;
            const timer = setTimeout(() => {
                setIsTimelineOpen(false);
                setIsLiveDetailOpen(false);
            }, 1500);
            return () => clearTimeout(timer);
        }
    }, [allDone, isResponding, totalCount]);

    if (totalCount === 0) return null;

    const railClassName = allDone ? "bg-(--success-wMain)/30" : "bg-(--secondary-text-wMain)/15";
    const activityBody =
        activityUpdates && activityUpdates.length > 0 ? (
            <InlineProgressUpdates headerless updates={activityUpdates} isActive={!!activityActive && !isInterrupted} allDone={allDone || hostSaysInterrupted} interrupted={hostSaysInterrupted} railClassName={railClassName} />
        ) : null;

    const isLive = !!activityActive && !isInterrupted;
    const latestUpdate = dedupeConsecutiveUpdates(activityUpdates ?? []).at(-1);
    const buildChrome = <BuildChrome statusSuffix={statusSuffix} isInterrupted={isInterrupted} allDone={allDone} errorCount={errorCount} />;

    const headerSpinner = isResponding && !activityBody ? <Loader2 className="h-4 w-4 animate-spin text-(--primary-wMain)" /> : null;

    if (!isTimelineOpen) {
        return (
            <div className="mb-1 ml-2 flex items-center gap-1">
                <Button variant="ghost" size="sm" className={HEADER_BUTTON_CLASS} onClick={() => setIsTimelineOpen(true)} aria-expanded={false}>
                    <ChevronRight className="h-3.5 w-3.5" />
                    {isResponding ? <Loader2 className="h-4 w-4 animate-spin text-(--primary-wMain)" /> : allDone && <CircleCheckBig className="h-4 w-4" style={{ color: "var(--success-wMain)" }} />}
                    <span className="font-medium">Activity Timeline</span>
                </Button>
                {onViewWorkflow && <ViewWorkflowButton onClick={onViewWorkflow} />}
            </div>
        );
    }

    if (isLive && !isLiveDetailOpen && latestUpdate) {
        return (
            <LiveActivityLine className="mb-1 ml-2" text={latestUpdate.text} animationKey={`${latestUpdate.timestamp}-${latestUpdate.text}`} onExpand={() => setIsLiveDetailOpen(true)}>
                {buildChrome}
                {onViewWorkflow && <ViewWorkflowButton onClick={onViewWorkflow} className={HOVER_REVEAL} />}
            </LiveActivityLine>
        );
    }

    return (
        <div className="mb-1 ml-2">
            <div className="mb-1 flex items-center gap-1">
                <TimelineHeader isLive={isLive} allSettled={allSettled} allDone={allDone} spinner={headerSpinner} onCollapseDetail={() => setIsLiveDetailOpen(false)} onCollapseBlock={() => setIsTimelineOpen(false)} />
                {buildChrome}
                {isInterrupted && onResume && (
                    <Button variant="outline" size="sm" className="ml-2 h-7 gap-1.5 bg-(--background-w20)" onClick={onResume}>
                        <Play className="h-3.5 w-3.5" />
                        Resume build
                    </Button>
                )}
                {onViewWorkflow && <ViewWorkflowButton onClick={onViewWorkflow} />}
            </div>

            {activityBody}
        </div>
    );
}
