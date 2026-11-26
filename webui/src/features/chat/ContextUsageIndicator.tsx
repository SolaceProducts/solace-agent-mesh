// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, FoldVertical, Loader2, MessageSquare, Sparkles } from "lucide-react";

import { sessionKeys, useCompactSession, useSessionContextUsage } from "@/api/sessions";

import { useChatContext } from "@/providers/contexts";

import { ConfirmationDialog } from "@/common";
import { useIsMobile } from "@/hooks";
import { Button, Popover, PopoverContent, PopoverTrigger, Progress, Tooltip, TooltipContent, TooltipTrigger, UsageRing } from "@/ui";
import { cn, formatTokenCount } from "@/utils";

const COMPACT_MIN_MESSAGES = 4;
const COMPACT_MIN_USAGE_PCT = 50;
const COMPACT_TOOLBAR_MIN_USAGE_PCT = 80;
const INDICATOR_MIN_USAGE_PCT = 50;
const CLOSE_GRACE_MS = 150;

interface ContextUsageIndicatorProps {
    sessionId: string;
    onCompacted?: () => void;
    messageCount?: number;
}

function formatModelName(model: string): string {
    const slashIdx = model.lastIndexOf("/");
    return slashIdx >= 0 ? model.slice(slashIdx + 1) : model;
}

function getUsageColor(percentage: number): string {
    if (percentage >= 90) return "text-(--error-wMain)";
    if (percentage >= 75) return "text-(--warning-wMain)";
    if (percentage >= 50) return "text-(--warning-w70)";
    return "text-(--success-wMain)";
}

function getUsageBarColor(percentage: number): string {
    if (percentage >= 90) return "bg-(--error-wMain)";
    if (percentage >= 75) return "bg-(--warning-wMain)";
    if (percentage >= 50) return "bg-(--warning-w70)";
    return "bg-(--success-wMain)";
}

export function ContextUsageIndicator({ sessionId, onCompacted, messageCount = 0 }: Readonly<ContextUsageIndicatorProps>) {
    const isMobile = useIsMobile();
    const [isExpanded, setIsExpanded] = useState(false);
    const [compactError, setCompactError] = useState<string | null>(null);
    const [compactSuccess, setCompactSuccess] = useState<string | null>(null);
    const [showCompactConfirm, setShowCompactConfirm] = useState(false);
    const { selectedAgentName } = useChatContext();
    const queryClient = useQueryClient();
    const closeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const openedByHoverRef = useRef(false);
    const panelHadFocusRef = useRef(false);
    const panelTitleId = useId();

    const { data: usage } = useSessionContextUsage(sessionId, selectedAgentName || undefined);
    const compactMutation = useCompactSession(sessionId);
    const isCompacting = compactMutation.isPending;
    const isCompactFlowActive = isCompacting || showCompactConfirm;

    const cancelClose = useCallback(() => {
        if (closeTimerRef.current) {
            clearTimeout(closeTimerRef.current);
            closeTimerRef.current = null;
        }
    }, []);

    const scheduleClose = useCallback(() => {
        cancelClose();
        if (isCompactFlowActive) return;
        closeTimerRef.current = setTimeout(() => setIsExpanded(false), CLOSE_GRACE_MS);
    }, [cancelClose, isCompactFlowActive]);

    const openNow = useCallback(() => {
        if (isMobile) return;
        cancelClose();
        setIsExpanded(true);
    }, [cancelClose, isMobile]);

    const openOnHover = useCallback(() => {
        openedByHoverRef.current = true;
        openNow();
    }, [openNow]);

    const openOnClick = useCallback(() => {
        openedByHoverRef.current = false;
        openNow();
    }, [openNow]);

    useEffect(() => cancelClose, [cancelClose]);

    useEffect(() => {
        if (!compactSuccess) return;
        const timer = setTimeout(() => setCompactSuccess(null), 5000);
        return () => clearTimeout(timer);
    }, [compactSuccess]);

    useEffect(() => {
        if (!compactError) return;
        const timer = setTimeout(() => setCompactError(null), 8000);
        return () => clearTimeout(timer);
    }, [compactError]);

    const pct = usage?.usagePercentage ?? 0;
    const formattedCurrent = formatTokenCount(usage?.currentContextTokens ?? 0);
    const formattedLimit = usage?.maxInputTokens ? formatTokenCount(usage.maxInputTokens) : null;

    const canCompact = useMemo(() => messageCount >= COMPACT_MIN_MESSAGES || pct >= COMPACT_MIN_USAGE_PCT, [messageCount, pct]);
    const shouldShowToolbarIcon = useMemo(() => canCompact && pct >= COMPACT_TOOLBAR_MIN_USAGE_PCT, [canCompact, pct]);

    const isBelowThreshold = pct < INDICATOR_MIN_USAGE_PCT && !isCompactFlowActive && !compactSuccess && !compactError;
    const isVisible = Boolean(sessionId) && usage?.maxInputTokens != null && !isBelowThreshold;

    useEffect(() => {
        if (isVisible || compactSuccess || compactError) return;
        cancelClose();
        setIsExpanded(false);
    }, [isVisible, compactSuccess, compactError, cancelClose]);

    const requestCompress = () => {
        if (isCompacting) return;
        cancelClose();
        setShowCompactConfirm(true);
    };

    const handleCompress = async () => {
        if (isCompacting) return;
        setCompactError(null);
        setCompactSuccess(null);
        try {
            const result = await compactMutation.mutateAsync(undefined);
            const remainingSuffix = result.remainingTokens > 0 ? `${formatTokenCount(result.remainingTokens)} tokens remaining.` : "";
            setCompactSuccess(`Compacted ${result.eventsCompacted} events. ${remainingSuffix}`);
            openOnClick();

            await queryClient.invalidateQueries({ queryKey: sessionKeys.chatTasks(sessionId) });
            onCompacted?.();
        } catch (err) {
            setCompactError(err instanceof Error ? err.message : "Failed to compact session");
            openOnClick();
        }
    };

    if (!sessionId) return null;
    if (!usage) return null;
    if (usage.maxInputTokens == null) return null;
    if (isBelowThreshold) return null;

    return (
        <Popover open={isExpanded && !isMobile} onOpenChange={open => !isMobile && setIsExpanded(open)}>
            <div className="flex items-center">
                <PopoverTrigger asChild>
                    <Button
                        variant="ghost"
                        size="icon"
                        aria-label={`Context usage: ${pct}%`}
                        onMouseEnter={openOnHover}
                        onMouseLeave={scheduleClose}
                        onClick={event => {
                            event.preventDefault();
                            openOnClick();
                        }}
                    >
                        <UsageRing value={pct} className={getUsageColor(pct)} />
                    </Button>
                </PopoverTrigger>
                {(shouldShowToolbarIcon || isCompacting) && (
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <Button
                                variant="ghost"
                                size="icon"
                                aria-label={isCompacting ? "Compressing conversation" : "Compress conversation"}
                                disabled={isCompacting}
                                onClick={() => {
                                    if (!isCompacting) requestCompress();
                                }}
                            >
                                {isCompacting ? <Loader2 className="h-4 w-4 animate-spin text-(--primary-wMain)" /> : <FoldVertical className="h-4 w-4" />}
                            </Button>
                        </TooltipTrigger>
                        <TooltipContent side="top">{isCompacting ? "Compressing..." : "Compress conversation"}</TooltipContent>
                    </Tooltip>
                )}
            </div>

            <PopoverContent
                side="top"
                align="end"
                sideOffset={4}
                collisionPadding={8}
                aria-labelledby={panelTitleId}
                className="w-64 rounded-lg border bg-(--background-w10) px-0 py-0 text-(--primary-text-wMain) shadow-lg"
                onMouseEnter={openNow}
                onMouseLeave={scheduleClose}
                onFocusCapture={() => {
                    cancelClose();
                    panelHadFocusRef.current = true;
                }}
                onBlurCapture={scheduleClose}
                onOpenAutoFocus={event => {
                    panelHadFocusRef.current = false;
                    if (openedByHoverRef.current) event.preventDefault();
                }}
                onCloseAutoFocus={event => {
                    if (!panelHadFocusRef.current) event.preventDefault();
                }}
                onInteractOutside={event => {
                    const target = event.detail.originalEvent.target;
                    if (target instanceof Element && target.closest('[role="dialog"], [role="alertdialog"]')) {
                        event.preventDefault();
                    }
                }}
            >
                <div className="space-y-3 p-3">
                    <div className="flex items-center justify-between">
                        <span id={panelTitleId} className="text-sm font-semibold">
                            Context Window Usage
                        </span>
                        <span className={cn("font-mono text-xs font-semibold", getUsageColor(pct))}>{pct}%</span>
                    </div>

                    <div className="space-y-1">
                        <Progress value={pct} className="h-2" indicatorClassName={getUsageBarColor(pct)} />
                        <div className="flex justify-between text-xs">
                            <span>{formattedCurrent} used</span>
                            {formattedLimit && <span>{formattedLimit} limit</span>}
                        </div>
                    </div>

                    <div className="space-y-2 text-xs">
                        <div className="flex items-center justify-between border-t pt-2">
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <span>Session total</span>
                                </TooltipTrigger>
                                <TooltipContent>Cumulative tokens sent and received across this agent's tasks in the session</TooltipContent>
                            </Tooltip>
                            <div className="flex items-center gap-3 font-mono text-xs">
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <span className="flex items-center gap-1">
                                            <ArrowUp className="h-3 w-3" />
                                            {formatTokenCount(usage.promptTokens)}
                                        </span>
                                    </TooltipTrigger>
                                    <TooltipContent>Total sent (prompt tokens)</TooltipContent>
                                </Tooltip>
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <span className="flex items-center gap-1">
                                            <ArrowDown className="h-3 w-3" />
                                            {formatTokenCount(usage.completionTokens)}
                                        </span>
                                    </TooltipTrigger>
                                    <TooltipContent>Total received (completion tokens)</TooltipContent>
                                </Tooltip>
                            </div>
                        </div>
                        <div className="flex items-center justify-between gap-2">
                            <span className="shrink-0">Model:</span>
                            <Tooltip>
                                <TooltipTrigger asChild>
                                    <span className="min-w-0 truncate font-mono font-semibold">{formatModelName(usage.model)}</span>
                                </TooltipTrigger>
                                <TooltipContent className="max-w-xs break-all">{usage.model}</TooltipContent>
                            </Tooltip>
                        </div>
                        <div className="flex items-center justify-between border-t pt-2">
                            <span className="flex items-center gap-1">
                                <MessageSquare className="h-3 w-3" />
                                Messages
                            </span>
                            <span className="font-mono">{usage.totalMessages}</span>
                        </div>
                    </div>

                    {(canCompact || compactSuccess || compactError) && (
                        <div className="space-y-2 border-t pt-3">
                            {compactSuccess && (
                                <output className="flex items-start justify-between gap-1 rounded bg-(--success-w10) p-2 text-xs text-(--success-wMain)">
                                    <span>{compactSuccess}</span>
                                    <button className="shrink-0 opacity-60 hover:opacity-100" onClick={() => setCompactSuccess(null)} aria-label="Dismiss">
                                        ✕
                                    </button>
                                </output>
                            )}
                            {compactError && (
                                <div role="alert" className="flex items-start justify-between gap-1 rounded bg-(--error-w10) p-2 text-xs text-(--error-wMain)">
                                    <span>{compactError}</span>
                                    <button className="shrink-0 opacity-60 hover:opacity-100" onClick={() => setCompactError(null)} aria-label="Dismiss">
                                        ✕
                                    </button>
                                </div>
                            )}
                            {canCompact && (
                                <Tooltip>
                                    <TooltipTrigger asChild>
                                        <Button variant="outline" size="sm" className="w-full" onClick={requestCompress} disabled={isCompacting}>
                                            {isCompacting ? (
                                                <>
                                                    <Loader2 className="mr-2 h-3 w-3 animate-spin" />
                                                    Compressing...
                                                </>
                                            ) : (
                                                <>
                                                    <Sparkles className="mr-2 h-3 w-3" />
                                                    Compress Conversation
                                                </>
                                            )}
                                        </Button>
                                    </TooltipTrigger>
                                    <TooltipContent>Compress older messages to free context space</TooltipContent>
                                </Tooltip>
                            )}
                        </div>
                    )}

                    {pct >= 90 && <div className="rounded bg-(--error-w10) p-2 text-xs text-(--error-wMain)">Approaching context limit! Consider compressing the conversation.</div>}
                    {pct >= 75 && pct < 90 && <div className="rounded bg-(--warning-w10) p-2 text-xs text-(--warning-wMain)">Context usage is high. Consider compressing soon.</div>}
                </div>
            </PopoverContent>

            <ConfirmationDialog
                open={showCompactConfirm}
                onOpenChange={setShowCompactConfirm}
                title="Compress conversation?"
                description="Older messages will be compressed so the agent has more room for new context. Your chat history stays visible here, but on the next turn the agent will work from the summary instead of the full original messages. This cannot be undone."
                actionLabels={{ confirm: "Compress", cancel: "Cancel" }}
                isLoading={isCompacting}
                onConfirm={handleCompress}
            />
        </Popover>
    );
}
