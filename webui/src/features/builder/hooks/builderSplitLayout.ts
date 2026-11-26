// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { CSSProperties } from "react";

import type { ChatPanelMode } from "../useBuilderPageState";

export const CHAT_PANEL_MIN_WIDTH = 320;
export const CANVAS_MIN_WIDTH = 400;
export const SPLIT_MIN_WIDTH = CHAT_PANEL_MIN_WIDTH + CANVAS_MIN_WIDTH;
const CHAT_PANEL_MAX_VIEWPORT_SHARE = 0.6;

export function isSplitTooNarrow(containerWidth: number | null): boolean {
    return containerWidth !== null && containerWidth > 0 && containerWidth < SPLIT_MIN_WIDTH;
}

export function splitPanelSizing(preferredWidth: number): CSSProperties {
    return { width: `${preferredWidth}px`, minWidth: `${CHAT_PANEL_MIN_WIDTH}px`, maxWidth: `${CHAT_PANEL_MAX_VIEWPORT_SHARE * 100}vw`, flexShrink: 1 };
}

export const CANVAS_PANE_SIZING: CSSProperties = { minWidth: `${CANVAS_MIN_WIDTH}px` };

export function splitPanelMaxWidth(rowWidth: number, viewportWidth: number): number {
    return Math.max(CHAT_PANEL_MIN_WIDTH, Math.min(viewportWidth * CHAT_PANEL_MAX_VIEWPORT_SHARE, rowWidth - CANVAS_MIN_WIDTH));
}

export function clampSplitPanelWidth(width: number, rowWidth: number, viewportWidth: number): number {
    return Math.min(Math.max(width, CHAT_PANEL_MIN_WIDTH), splitPanelMaxWidth(rowWidth, viewportWidth));
}

interface SplitLayoutContext {
    chatPanelMode: ChatPanelMode;
    isMobile: boolean;
    isSessionTransitioning: boolean;
    isSplitTooNarrow: boolean;
}

export function resolveChatPanelMode({ chatPanelMode, isMobile, isSessionTransitioning, isSplitTooNarrow, canvasViewOpen }: SplitLayoutContext & { canvasViewOpen: boolean }): ChatPanelMode {
    if (isMobile || isSessionTransitioning) return "fullscreen";
    if (!isSplitTooNarrow || chatPanelMode === "floating") return chatPanelMode;
    return canvasViewOpen ? "minimized" : "fullscreen";
}

export function canToggleCanvasView({ chatPanelMode, isMobile, isSessionTransitioning, isSplitTooNarrow, hasCanvasContent }: SplitLayoutContext & { hasCanvasContent: boolean }): boolean {
    const canvasIsReachable = hasCanvasContent && !isMobile && !isSessionTransitioning;
    return isSplitTooNarrow && canvasIsReachable && chatPanelMode !== "floating";
}
