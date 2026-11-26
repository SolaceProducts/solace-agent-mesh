// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type KeyboardEvent, type MouseEvent as ReactMouseEvent, type RefObject, useCallback, useEffect, useState } from "react";

import { CHAT_PANEL_MIN_WIDTH, clampSplitPanelWidth, splitPanelMaxWidth } from "./hooks/builderSplitLayout";

const KEYBOARD_RESIZE_STEP = 20;

interface SplitMeasure {
    width: number;
    rowWidth: number;
}

function measureSplitPanel(panel: HTMLElement | null): SplitMeasure | null {
    if (!panel) return null;
    return { width: panel.getBoundingClientRect().width, rowWidth: panel.offsetParent?.getBoundingClientRect().width ?? globalThis.innerWidth };
}

function useSplitPanelMeasure(panelRef: RefObject<HTMLElement | null>) {
    const [measure, setMeasure] = useState<SplitMeasure | null>(null);
    const refresh = useCallback(() => {
        const next = measureSplitPanel(panelRef.current);
        setMeasure(previous => (previous?.width === next?.width && previous?.rowWidth === next?.rowWidth ? previous : next));
        return next;
    }, [panelRef]);

    useEffect(() => {
        const panel = panelRef.current;
        refresh();
        if (!panel || !globalThis.ResizeObserver) return undefined;
        const observer = new ResizeObserver(refresh);
        observer.observe(panel);
        if (panel.offsetParent) observer.observe(panel.offsetParent);
        return () => observer.disconnect();
    }, [panelRef, refresh]);

    return { measure, refresh };
}

interface SplitResizeHandleProps {
    panelRef: RefObject<HTMLElement | null>;
    onDragStart: (event: ReactMouseEvent) => void;
    onWidthChange: (width: number) => void;
}

export function SplitResizeHandle({ panelRef, onDragStart, onWidthChange }: Readonly<SplitResizeHandleProps>) {
    const { measure, refresh } = useSplitPanelMeasure(panelRef);

    const handleKeyDown = useCallback(
        (event: KeyboardEvent<HTMLDivElement>) => {
            const isResizeArrow = event.key === "ArrowLeft" || event.key === "ArrowRight";
            const hasBrowserModifier = event.altKey || event.ctrlKey || event.metaKey;
            if (!isResizeArrow || hasBrowserModifier) return;
            const current = refresh();
            if (!current) return;
            event.preventDefault();
            const delta = event.key === "ArrowRight" ? KEYBOARD_RESIZE_STEP : -KEYBOARD_RESIZE_STEP;
            onWidthChange(clampSplitPanelWidth(current.width + delta, current.rowWidth, globalThis.innerWidth));
        },
        [refresh, onWidthChange]
    );

    return (
        <div
            data-testid="builder-chat-resize-handle"
            role="separator"
            aria-orientation="vertical"
            aria-label="Resize chat panel"
            aria-valuemin={CHAT_PANEL_MIN_WIDTH}
            aria-valuemax={measure ? Math.round(splitPanelMaxWidth(measure.rowWidth, globalThis.innerWidth)) : undefined}
            aria-valuenow={measure ? Math.round(measure.width) : undefined}
            tabIndex={0}
            className="absolute top-0 right-0 bottom-0 z-10 w-1 cursor-col-resize transition-colors hover:bg-(--primary-wMain)/30 focus-visible:bg-(--primary-wMain)/50 focus-visible:outline-none active:bg-(--primary-wMain)/50"
            onMouseDown={onDragStart}
            onFocus={refresh}
            onKeyDown={handleKeyDown}
        />
    );
}
