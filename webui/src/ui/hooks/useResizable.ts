// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

interface UseResizableOptions {
    defaultWidth: number;
    minWidth: number;
    maxWidth: number;
    position?: "left" | "right";
    onWidthChange?: (width: number) => void;
}

export function useResizable({ defaultWidth, minWidth, maxWidth, position = "left", onWidthChange }: UseResizableOptions) {
    const [width, setWidth] = useState(defaultWidth);
    const [isResizing, setIsResizing] = useState(false);
    const startXRef = useRef<number>(0);
    const startWidthRef = useRef<number>(defaultWidth);

    const handleMouseDown = useCallback(
        (e: React.MouseEvent) => {
            e.preventDefault();
            setIsResizing(true);
            startXRef.current = e.clientX;
            startWidthRef.current = width;
        },
        [width]
    );

    const handleMouseMove = useCallback(
        (e: MouseEvent) => {
            if (!isResizing) return;

            const deltaX = e.clientX - startXRef.current;
            const adjustedDelta = position === "right" ? -deltaX : deltaX;
            const newWidth = Math.min(Math.max(startWidthRef.current + adjustedDelta, minWidth), maxWidth);

            setWidth(newWidth);
            onWidthChange?.(newWidth);
        },
        [isResizing, minWidth, maxWidth, position, onWidthChange]
    );

    const handleMouseUp = useCallback(() => {
        setIsResizing(false);
    }, []);

    const handleTouchStart = useCallback(
        (e: React.TouchEvent) => {
            e.preventDefault();
            setIsResizing(true);
            startXRef.current = e.touches[0].clientX;
            startWidthRef.current = width;
        },
        [width]
    );

    const handleTouchMove = useCallback(
        (e: TouchEvent) => {
            if (!isResizing) return;

            const deltaX = e.touches[0].clientX - startXRef.current;
            const adjustedDelta = position === "right" ? -deltaX : deltaX;
            const newWidth = Math.min(Math.max(startWidthRef.current + adjustedDelta, minWidth), maxWidth);

            setWidth(newWidth);
            onWidthChange?.(newWidth);
        },
        [isResizing, minWidth, maxWidth, position, onWidthChange]
    );

    const handleTouchEnd = useCallback(() => {
        setIsResizing(false);
    }, []);

    useEffect(() => {
        if (isResizing) {
            document.addEventListener("mousemove", handleMouseMove);
            document.addEventListener("mouseup", handleMouseUp);
            document.addEventListener("touchmove", handleTouchMove);
            document.addEventListener("touchend", handleTouchEnd);

            document.body.style.userSelect = "none";
            document.body.style.cursor = "col-resize";

            return () => {
                document.removeEventListener("mousemove", handleMouseMove);
                document.removeEventListener("mouseup", handleMouseUp);
                document.removeEventListener("touchmove", handleTouchMove);
                document.removeEventListener("touchend", handleTouchEnd);

                document.body.style.userSelect = "";
                document.body.style.cursor = "";
            };
        }
    }, [isResizing, handleMouseMove, handleMouseUp, handleTouchMove, handleTouchEnd]);

    return {
        width,
        isResizing,
        handleMouseDown,
        handleTouchStart,
        setWidth,
    };
}
