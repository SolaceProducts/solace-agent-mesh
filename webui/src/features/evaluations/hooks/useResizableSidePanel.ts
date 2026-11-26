// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

const DEFAULTS = {
    minWidth: 360,
    defaultFraction: 0.4,
    maxFraction: 0.6,
} as const;

interface UseResizableSidePanelOptions {
    readonly minWidth?: number;
    readonly defaultFraction?: number;
    readonly maxFraction?: number;
}

export function useResizableSidePanel(open: boolean, options?: UseResizableSidePanelOptions) {
    const minWidth = options?.minWidth ?? DEFAULTS.minWidth;
    const defaultFraction = options?.defaultFraction ?? DEFAULTS.defaultFraction;
    const maxFraction = options?.maxFraction ?? DEFAULTS.maxFraction;

    const containerRef = useRef<HTMLDivElement>(null);
    const [width, setWidth] = useState<number | null>(null);
    const draggingRef = useRef(false);

    useEffect(() => {
        if (!open) return;
        const recompute = () => {
            const containerWidth = containerRef.current?.clientWidth ?? 0;
            if (containerWidth <= 0) return;
            const max = Math.max(minWidth, containerWidth * maxFraction);
            setWidth(prev => {
                const target = prev ?? containerWidth * defaultFraction;
                return Math.min(Math.max(target, minWidth), max);
            });
        };
        recompute();
        window.addEventListener("resize", recompute);
        return () => window.removeEventListener("resize", recompute);
    }, [open, minWidth, defaultFraction, maxFraction]);

    const handleDragStart = useCallback(
        (e: React.MouseEvent<HTMLDivElement>) => {
            e.preventDefault();
            if (!containerRef.current) return;
            draggingRef.current = true;
            document.body.style.cursor = "col-resize";
            document.body.style.userSelect = "none";

            const onMove = (ev: MouseEvent) => {
                if (!draggingRef.current || !containerRef.current) return;
                const rect = containerRef.current.getBoundingClientRect();
                const rawWidth = rect.right - ev.clientX;
                const max = Math.max(minWidth, rect.width * maxFraction);
                setWidth(Math.min(Math.max(rawWidth, minWidth), max));
            };
            const onUp = () => {
                draggingRef.current = false;
                document.body.style.cursor = "";
                document.body.style.userSelect = "";
                window.removeEventListener("mousemove", onMove);
                window.removeEventListener("mouseup", onUp);
            };
            window.addEventListener("mousemove", onMove);
            window.addEventListener("mouseup", onUp);
        },
        [minWidth, maxFraction]
    );

    return { containerRef, width, handleDragStart };
}
