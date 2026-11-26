// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type RefObject, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

import type { ChatPanelMode } from "../useBuilderPageState";

import { canToggleCanvasView, isSplitTooNarrow, resolveChatPanelMode } from "./builderSplitLayout";

function useIsRowTooNarrow(ref: RefObject<HTMLElement | null>): boolean {
    const [tooNarrow, setTooNarrow] = useState(false);

    useLayoutEffect(() => {
        const element = ref.current;
        if (!element) return undefined;
        setTooNarrow(isSplitTooNarrow(element.getBoundingClientRect().width));
        if (!globalThis.ResizeObserver) return undefined;
        const observer = new ResizeObserver(entries => setTooNarrow(isSplitTooNarrow(entries[0].contentRect.width)));
        observer.observe(element);
        return () => observer.disconnect();
    }, [ref]);

    return tooNarrow;
}

interface BuilderSplitLayoutInput {
    sessionId: string;
    chatPanelMode: ChatPanelMode;
    isMobile: boolean;
    isSessionTransitioning: boolean;
    hasCanvasContent: boolean;
}

export interface CanvasViewToggle {
    showingCanvas: boolean;
    onToggle: () => void;
}

export function useBuilderSplitLayout({ sessionId, chatPanelMode, isMobile, isSessionTransitioning, hasCanvasContent }: BuilderSplitLayoutInput) {
    const rowRef = useRef<HTMLDivElement>(null);
    const tooNarrow = useIsRowTooNarrow(rowRef);
    const [canvasViewOpen, setCanvasViewOpen] = useState(false);

    const toggleAvailable = canToggleCanvasView({ chatPanelMode, isMobile, isSessionTransitioning, isSplitTooNarrow: tooNarrow, hasCanvasContent });

    useEffect(() => {
        setCanvasViewOpen(false);
    }, [sessionId, toggleAvailable]);

    const showingCanvas = toggleAvailable && canvasViewOpen;
    const effectiveMode = resolveChatPanelMode({ chatPanelMode, isMobile, isSessionTransitioning, isSplitTooNarrow: tooNarrow, canvasViewOpen: showingCanvas });
    const onToggle = useCallback(() => setCanvasViewOpen(open => !open), []);
    const canvasViewToggle: CanvasViewToggle | null = toggleAvailable ? { showingCanvas, onToggle } : null;

    return { rowRef, effectiveMode, canvasViewToggle };
}
