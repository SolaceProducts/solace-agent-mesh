// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Fragment, useEffect, useRef, useState } from "react";
import { ChevronDown, ChevronRight, ChevronUp, Loader2 } from "lucide-react";

import { useUIModeContext } from "@/providers/contexts";

import { ViewWorkflowButton } from "@/common/buttons/ViewWorkflowButton";
import { MarkdownWrapper } from "@/common/markdown";
import { HOVER_REVEAL, LiveActivityLine } from "@/common/timeline/LiveActivityLine";
import { TimelineRow } from "@/common/timeline/TimelineRow";

import type { ProgressUpdate } from "@/types";
import { Button } from "@/ui";

import { dedupeConsecutiveUpdates } from "./activityIndicator";

interface InlineProgressUpdatesProps {
    updates: ProgressUpdate[];
    isActive?: boolean;
    allDone?: boolean;
    interrupted?: boolean;
    onViewWorkflow?: () => void;
    headerless?: boolean;
    trailingConnector?: boolean;
    railClassName?: string;
}

const COLLAPSE_THRESHOLD = 10;

const stripTrailingEllipsis = (text: string): string => text.replace(/(\.\.\.|\u2026)\s*$/, "");

function stepText(text: string, state: { isActiveStep: boolean; allDone: boolean; isStoppedStep: boolean }): string {
    if (state.isStoppedStep) return `${stripTrailingEllipsis(text)} (interrupted)`;
    return state.allDone && !state.isActiveStep ? stripTrailingEllipsis(text) : text;
}

export const InlineProgressUpdates = ({
    updates,
    isActive = false,
    allDone = false,
    interrupted = false,
    onViewWorkflow,
    headerless = false,
    trailingConnector = false,
    railClassName = "bg-current opacity-30",
}: Readonly<InlineProgressUpdatesProps>) => {
    const [isTimelineOpen, setIsTimelineOpen] = useState(false);
    const [isListExpanded, setIsListExpanded] = useState(false);
    const [expandedThinkingIds, setExpandedThinkingIds] = useState<Set<number>>(new Set());
    const hasAutoCollapsed = useRef(false);
    const { showActivityPanel } = useUIModeContext();
    const onOpenActivityPanel = showActivityPanel ? onViewWorkflow : undefined;

    useEffect(() => {
        if (headerless) return;
        if (!isActive && !hasAutoCollapsed.current && updates.length > 0) {
            hasAutoCollapsed.current = true;
            setIsTimelineOpen(false);
        }
    }, [isActive, updates.length, headerless]);

    if (!updates || updates.length === 0) return null;

    const deduped = dedupeConsecutiveUpdates(updates);

    const shouldCollapseList = deduped.length > COLLAPSE_THRESHOLD;
    const visibleIndices = shouldCollapseList && !isListExpanded ? [0, ...Array.from({ length: 2 }, (_, i) => deduped.length - 2 + i)] : deduped.map((_, i) => i);
    const visibleUpdates = visibleIndices.map(i => deduped[i]);
    const hiddenCount = shouldCollapseList && !isListExpanded ? Math.max(0, deduped.length - visibleUpdates.length) : 0;

    const toggleThinking = (dedupedIndex: number) => {
        setExpandedThinkingIds(prev => {
            const next = new Set(prev);
            if (next.has(dedupedIndex)) {
                next.delete(dedupedIndex);
            } else {
                next.add(dedupedIndex);
            }
            return next;
        });
    };

    const itemsBody = (
        <>
            <div>
                {visibleUpdates.map((update, index) => {
                    const dedupedIndex = visibleIndices[index];
                    const isLast = index === visibleUpdates.length - 1;
                    const isThinking = update.type === "thinking";
                    const isThinkingExpanded = expandedThinkingIds.has(dedupedIndex);
                    const isActiveStep = isLast && isActive;
                    const isStoppedStep = isLast && interrupted && !isActive;

                    const showExpandButton = shouldCollapseList && !isListExpanded && index === 0;

                    const dot = (
                        <div className="relative z-10 flex h-5 w-5 flex-shrink-0 items-center justify-center">
                            <div className="absolute h-4 w-4 rounded-full bg-(--background-w10)" />
                            {isActiveStep ? (
                                <Loader2 className="relative h-[14px] w-[14px] animate-spin text-(--primary-wMain)" />
                            ) : (
                                <div className={`relative h-[10px] w-[10px] rounded-full ${isStoppedStep ? "bg-(--warning-wMain)" : "bg-(--success-wMain)"}`} />
                            )}
                        </div>
                    );

                    return (
                        <Fragment key={`${update.timestamp}-${dedupedIndex}`}>
                            <TimelineRow dot={dot} isFirst={index === 0} isLast={isLast && !trailingConnector} railClassName={railClassName} animationDelayMs={Math.min(index * 50, 200)}>
                                {isThinking ? (
                                    <div>
                                        <button type="button" className="flex items-center gap-1 text-sm leading-relaxed text-(--secondary-text-wMain) transition-colors hover:text-(--primary-text-wMain)" onClick={() => toggleThinking(dedupedIndex)}>
                                            <span className="font-medium">{isStoppedStep ? stepText(update.text, { isActiveStep, allDone, isStoppedStep }) : update.text}</span>
                                            {isThinkingExpanded ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
                                        </button>

                                        {isThinkingExpanded && update.expandableContent && (
                                            <div className="mt-2 rounded-lg px-3 py-2">
                                                <div className="max-h-96 overflow-y-auto text-sm text-(--secondary-text-wMain) opacity-70">
                                                    <MarkdownWrapper content={update.expandableContent} />
                                                </div>
                                            </div>
                                        )}
                                    </div>
                                ) : (
                                    <span className={`text-sm leading-relaxed ${isActiveStep ? "text-(--primary-text-wMain)" : "text-(--secondary-text-wMain)"}`}>{stepText(update.text, { isActiveStep, allDone, isStoppedStep })}</span>
                                )}
                            </TimelineRow>

                            {showExpandButton && (
                                <div className="py-0.5">
                                    <Button variant="ghost" size="sm" className="h-6 gap-1 px-1 text-xs text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)" onClick={() => setIsListExpanded(true)}>
                                        <ChevronDown className="h-3 w-3" />
                                        {hiddenCount} more step{hiddenCount > 1 ? "s" : ""}
                                    </Button>
                                </div>
                            )}
                        </Fragment>
                    );
                })}
            </div>

            {shouldCollapseList && isListExpanded && (
                <div className="py-0.5">
                    <Button variant="ghost" size="sm" className="h-6 gap-1 px-1 text-xs text-(--secondary-text-wMain) hover:text-(--primary-text-wMain)" onClick={() => setIsListExpanded(false)}>
                        <ChevronUp className="h-3 w-3" />
                        Show less
                    </Button>
                </div>
            )}
        </>
    );

    if (headerless) {
        return <div>{itemsBody}</div>;
    }

    if (isActive && !isTimelineOpen) {
        const latest = deduped.at(-1);
        if (!latest) return null;
        return (
            <LiveActivityLine className="mb-3" text={latest.text} animationKey={`${latest.timestamp}-${latest.text}`} onExpand={() => setIsTimelineOpen(true)}>
                {onOpenActivityPanel && <ViewWorkflowButton onClick={onOpenActivityPanel} className={HOVER_REVEAL} />}
            </LiveActivityLine>
        );
    }

    if (!isTimelineOpen) {
        return (
            <div className="mb-3 flex items-center gap-2">
                <Button variant="ghost" className="flex items-center gap-1 text-sm text-(--secondary-text-wMain) transition-colors hover:text-(--primary-text-wMain)" onClick={() => setIsTimelineOpen(true)} aria-expanded={false}>
                    <ChevronRight className="h-3.5 w-3.5" />
                    <span className="font-medium">Activity Timeline</span>
                </Button>
                {onOpenActivityPanel && <ViewWorkflowButton onClick={onOpenActivityPanel} />}
            </div>
        );
    }

    return (
        <div className="mb-3">
            <div className="mb-1 flex items-center gap-1">
                <Button variant="ghost" className="flex items-center gap-1 text-sm text-(--secondary-text-wMain) transition-colors hover:text-(--primary-text-wMain)" onClick={() => setIsTimelineOpen(false)} aria-expanded={true}>
                    <ChevronDown className="h-3.5 w-3.5" />
                    <span className="font-medium">Activity Timeline</span>
                </Button>
                {onOpenActivityPanel && <ViewWorkflowButton onClick={onOpenActivityPanel} />}
            </div>

            {itemsBody}
        </div>
    );
};

export default InlineProgressUpdates;
