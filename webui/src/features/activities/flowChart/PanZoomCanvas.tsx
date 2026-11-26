// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { forwardRef, type MouseEvent, type PointerEvent, type ReactNode, useCallback, useEffect, useImperativeHandle, useRef, useState } from "react";

const DEFAULT_FIT_MAX_SCALE = 1.0;
const FIT_TOP_OFFSET = 60;

interface PanZoomCanvasProps {
    children: ReactNode;
    initialScale?: number;
    minScale?: number;
    maxScale?: number;
    onTransformChange?: (transform: { scale: number; x: number; y: number }) => void;
    onUserInteraction?: () => void;
}

export interface PanZoomCanvasRef {
    resetTransform: () => void;
    getTransform: () => { scale: number; x: number; y: number };
    fitToContent: (options?: { animated?: boolean; maxFitScale?: number }) => void;
    zoomIn: (options?: { animated?: boolean }) => void;
    zoomOut: (options?: { animated?: boolean }) => void;
    zoomTo: (scale: number, options?: { animated?: boolean; centerX?: number; centerY?: number }) => void;
    panToPoint: (contentX: number, contentY: number, options?: { animated?: boolean }) => void;
}

interface PointerState {
    x: number;
    y: number;
}

interface GestureState {
    centerX: number;
    centerY: number;
    distance: number;
}

const PanZoomCanvas = forwardRef<PanZoomCanvasRef, PanZoomCanvasProps>(({ children, initialScale = 1, minScale = 0.1, maxScale = 4, onTransformChange, onUserInteraction }, ref) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const contentBoxRef = useRef<HTMLDivElement>(null);
    const [transform, setTransform] = useState({
        scale: initialScale,
        x: 0,
        y: 0,
    });
    const [isAnimating, setIsAnimating] = useState(false);

    const pointersRef = useRef<Map<number, PointerState>>(new Map());
    const lastGestureRef = useRef<GestureState | null>(null);
    const isDraggingRef = useRef(false);
    const lastDragPosRef = useRef<{ x: number; y: number } | null>(null);

    const clampScale = useCallback((scale: number) => Math.min(Math.max(scale, minScale), maxScale), [minScale, maxScale]);

    useImperativeHandle(ref, () => ({
        resetTransform: () => {
            setTransform({ scale: initialScale, x: 0, y: 0 });
        },
        getTransform: () => transform,
        fitToContent: (options?: { animated?: boolean; maxFitScale?: number }) => {
            const availableWidth = containerRef.current?.clientWidth ?? 0;
            const contentBoxWidth = contentBoxRef.current?.offsetWidth ?? 0;
            if (availableWidth === 0 || contentBoxWidth === 0) return;

            const fitMaxScale = options?.maxFitScale ?? DEFAULT_FIT_MAX_SCALE;
            const newScale = Math.min(Math.max(availableWidth / contentBoxWidth, minScale), fitMaxScale);

            const newX = (availableWidth - contentBoxWidth * newScale) / 2;
            const newY = FIT_TOP_OFFSET;

            if (options?.animated) {
                setIsAnimating(true);
                setTimeout(() => setIsAnimating(false), 300);
            }

            setTransform({ scale: newScale, x: newX, y: newY });
        },
        zoomIn: (options?: { animated?: boolean }) => {
            const container = containerRef.current;
            if (!container) return;

            const rect = container.getBoundingClientRect();
            const centerX = rect.width / 2;
            const centerY = rect.height / 2;

            setTransform(prev => {
                const currentPercent = Math.round(prev.scale * 100);
                const roundedPercent = Math.round(currentPercent / 10) * 10;
                const targetPercent = Math.min(roundedPercent + 10, maxScale * 100);
                const newScale = clampScale(targetPercent / 100);
                const scaleRatio = newScale / prev.scale;

                const newX = centerX - (centerX - prev.x) * scaleRatio;
                const newY = centerY - (centerY - prev.y) * scaleRatio;

                return { scale: newScale, x: newX, y: newY };
            });

            if (options?.animated) {
                setIsAnimating(true);
                setTimeout(() => setIsAnimating(false), 300);
            }
        },
        zoomOut: (options?: { animated?: boolean }) => {
            const container = containerRef.current;
            if (!container) return;

            const rect = container.getBoundingClientRect();
            const centerX = rect.width / 2;
            const centerY = rect.height / 2;

            setTransform(prev => {
                const currentPercent = Math.round(prev.scale * 100);
                const roundedPercent = Math.round(currentPercent / 10) * 10;
                const targetPercent = Math.max(roundedPercent - 10, minScale * 100);
                const newScale = clampScale(targetPercent / 100);
                const scaleRatio = newScale / prev.scale;

                const newX = centerX - (centerX - prev.x) * scaleRatio;
                const newY = centerY - (centerY - prev.y) * scaleRatio;

                return { scale: newScale, x: newX, y: newY };
            });

            if (options?.animated) {
                setIsAnimating(true);
                setTimeout(() => setIsAnimating(false), 300);
            }
        },
        zoomTo: (targetScale: number, options?: { animated?: boolean; centerX?: number; centerY?: number }) => {
            const container = containerRef.current;
            if (!container) return;

            const rect = container.getBoundingClientRect();
            const centerX = options?.centerX ?? rect.width / 2;
            const centerY = options?.centerY ?? rect.height / 2;

            const newScale = clampScale(targetScale);

            setTransform(prev => {
                const scaleRatio = newScale / prev.scale;
                const newX = centerX - (centerX - prev.x) * scaleRatio;
                const newY = centerY - (centerY - prev.y) * scaleRatio;
                return { scale: newScale, x: newX, y: newY };
            });

            if (options?.animated) {
                setIsAnimating(true);
                setTimeout(() => setIsAnimating(false), 300);
            }
        },
        panToPoint: (contentX: number, contentY: number, options?: { animated?: boolean }) => {
            const container = containerRef.current;
            if (!container) return;

            const rect = container.getBoundingClientRect();
            const viewportCenterX = rect.width / 2;
            const viewportCenterY = rect.height / 2;

            setTransform(prev => {
                const newX = viewportCenterX - contentX * prev.scale;
                const newY = viewportCenterY - contentY * prev.scale;
                return { ...prev, x: newX, y: newY };
            });

            if (options?.animated) {
                setIsAnimating(true);
                setTimeout(() => setIsAnimating(false), 300);
            }
        },
    }));

    useEffect(() => {
        onTransformChange?.(transform);
    }, [transform, onTransformChange]);

    const calculateGestureState = useCallback((pointers: Map<number, PointerState>): GestureState | null => {
        const points = Array.from(pointers.values());
        if (points.length < 2) return null;

        const [p1, p2] = points;
        const centerX = (p1.x + p2.x) / 2;
        const centerY = (p1.y + p2.y) / 2;
        const distance = Math.sqrt(Math.pow(p2.x - p1.x, 2) + Math.pow(p2.y - p1.y, 2));

        return { centerX, centerY, distance };
    }, []);

    const handlePointerDown = useCallback(
        (e: PointerEvent) => {
            if (e.defaultPrevented) return;

            (e.target as HTMLElement).setPointerCapture(e.pointerId);

            pointersRef.current.set(e.pointerId, { x: e.clientX, y: e.clientY });

            if (pointersRef.current.size === 1) {
                isDraggingRef.current = true;
                lastDragPosRef.current = { x: e.clientX, y: e.clientY };
            } else if (pointersRef.current.size === 2) {
                isDraggingRef.current = false;
                lastDragPosRef.current = null;
                lastGestureRef.current = calculateGestureState(pointersRef.current);
            }
        },
        [calculateGestureState]
    );

    const handlePointerMove = useCallback(
        (e: PointerEvent) => {
            if (!pointersRef.current.has(e.pointerId)) return;

            pointersRef.current.set(e.pointerId, { x: e.clientX, y: e.clientY });

            const rect = containerRef.current?.getBoundingClientRect();
            if (!rect) return;

            if (pointersRef.current.size >= 2) {
                const currentGesture = calculateGestureState(pointersRef.current);
                const lastGesture = lastGestureRef.current;

                if (currentGesture && lastGesture) {
                    const panDeltaX = currentGesture.centerX - lastGesture.centerX;
                    const panDeltaY = currentGesture.centerY - lastGesture.centerY;

                    const zoomFactor = currentGesture.distance / lastGesture.distance;

                    const cursorX = currentGesture.centerX - rect.left;
                    const cursorY = currentGesture.centerY - rect.top;

                    setTransform(prev => {
                        const newScale = clampScale(prev.scale * zoomFactor);
                        const scaleRatio = newScale / prev.scale;

                        let newX = cursorX - (cursorX - prev.x) * scaleRatio;
                        let newY = cursorY - (cursorY - prev.y) * scaleRatio;

                        newX += panDeltaX;
                        newY += panDeltaY;

                        return { scale: newScale, x: newX, y: newY };
                    });

                    onUserInteraction?.();
                }

                lastGestureRef.current = currentGesture;
            } else if (isDraggingRef.current && lastDragPosRef.current) {
                const dx = e.clientX - lastDragPosRef.current.x;
                const dy = e.clientY - lastDragPosRef.current.y;

                setTransform(prev => ({
                    ...prev,
                    x: prev.x + dx,
                    y: prev.y + dy,
                }));

                lastDragPosRef.current = { x: e.clientX, y: e.clientY };
                onUserInteraction?.();
            }
        },
        [calculateGestureState, clampScale, onUserInteraction]
    );

    const handlePointerUp = useCallback((e: PointerEvent) => {
        (e.target as HTMLElement).releasePointerCapture(e.pointerId);
        pointersRef.current.delete(e.pointerId);

        if (pointersRef.current.size < 2) {
            lastGestureRef.current = null;
        }

        if (pointersRef.current.size === 1) {
            const remaining = Array.from(pointersRef.current.values())[0];
            isDraggingRef.current = true;
            lastDragPosRef.current = { x: remaining.x, y: remaining.y };
        } else if (pointersRef.current.size === 0) {
            isDraggingRef.current = false;
            lastDragPosRef.current = null;
        }
    }, []);

    useEffect(() => {
        const container = containerRef.current;
        if (!container) return;

        const handleWheel = (e: WheelEvent) => {
            e.preventDefault();

            const rect = container.getBoundingClientRect();

            const dx = e.deltaX;
            const dy = e.deltaY;
            const ctrlKey = e.ctrlKey || e.metaKey;
            const shiftKey = e.shiftKey;

            const isTrackpadSwipe = Math.abs(dx) > 1 && !ctrlKey;

            if (shiftKey) {
                setTransform(prev => ({
                    ...prev,
                    x: prev.x - dy,
                }));
                onUserInteraction?.();
            } else if (ctrlKey) {
                const zoomFactor = 1 - dy * 0.01;
                const cursorX = e.clientX - rect.left;
                const cursorY = e.clientY - rect.top;

                setTransform(prev => {
                    const newScale = clampScale(prev.scale * zoomFactor);
                    const scaleRatio = newScale / prev.scale;

                    let newX = cursorX - (cursorX - prev.x) * scaleRatio;
                    const newY = cursorY - (cursorY - prev.y) * scaleRatio;
                    newX -= dx;

                    return { scale: newScale, x: newX, y: newY };
                });
                onUserInteraction?.();
            } else if (isTrackpadSwipe) {
                setTransform(prev => ({
                    ...prev,
                    x: prev.x - dx,
                    y: prev.y - dy,
                }));
                onUserInteraction?.();
            } else {
                const zoomFactor = 1 - dy * 0.005;
                const cursorX = e.clientX - rect.left;
                const cursorY = e.clientY - rect.top;

                setTransform(prev => {
                    const newScale = clampScale(prev.scale * zoomFactor);
                    const scaleRatio = newScale / prev.scale;

                    const newX = cursorX - (cursorX - prev.x) * scaleRatio;
                    const newY = cursorY - (cursorY - prev.y) * scaleRatio;

                    return { scale: newScale, x: newX, y: newY };
                });
                onUserInteraction?.();
            }
        };

        container.addEventListener("wheel", handleWheel, { passive: false });

        return () => {
            container.removeEventListener("wheel", handleWheel);
        };
    }, [clampScale, onUserInteraction]);

    const handleDoubleClick = useCallback(
        (e: MouseEvent) => {
            e.preventDefault();

            const target = e.target as HTMLElement;
            if (target.closest("button") || target.closest("a") || target.closest("[data-no-zoom]") || target.closest("[role='button']")) {
                return;
            }

            const rect = containerRef.current?.getBoundingClientRect();
            if (!rect) return;

            const cursorX = e.clientX - rect.left;
            const cursorY = e.clientY - rect.top;

            setTransform(prev => {
                const currentPercent = Math.round(prev.scale * 100);
                const roundedPercent = Math.round(currentPercent / 10) * 10;
                const targetPercent = Math.min(roundedPercent + 10, maxScale * 100);
                const newScale = clampScale(targetPercent / 100);
                const scaleRatio = newScale / prev.scale;

                const newX = cursorX - (cursorX - prev.x) * scaleRatio;
                const newY = cursorY - (cursorY - prev.y) * scaleRatio;

                return { scale: newScale, x: newX, y: newY };
            });

            setIsAnimating(true);
            setTimeout(() => setIsAnimating(false), 300);
            onUserInteraction?.();
        },
        [clampScale, maxScale, onUserInteraction]
    );

    return (
        <div
            ref={containerRef}
            onPointerDown={handlePointerDown}
            onPointerMove={handlePointerMove}
            onPointerUp={handlePointerUp}
            onPointerCancel={handlePointerUp}
            onDoubleClick={handleDoubleClick}
            style={{
                width: "100%",
                height: "100%",
                overflow: "hidden",
                touchAction: "none",
                cursor: "grab",
                userSelect: "none",
            }}
        >
            <div
                ref={contentBoxRef}
                style={{
                    transform: `translate(${transform.x}px, ${transform.y}px) scale(${transform.scale})`,
                    transformOrigin: "0 0",
                    width: "fit-content",
                    height: "fit-content",
                    transition: isAnimating ? "transform 300ms ease-out" : "none",
                }}
            >
                {children}
            </div>
        </div>
    );
});

PanZoomCanvas.displayName = "PanZoomCanvas";

export default PanZoomCanvas;
