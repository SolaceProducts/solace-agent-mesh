// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type ReactNode, useCallback, useEffect, useMemo, useState } from "react";

import { ZOOM_DEFAULT, ZOOM_MAX, ZOOM_MIN, ZOOM_STEP, ZoomContext, type ZoomContextValue } from "@/providers/contexts/ZoomContext";

const LOCAL_STORAGE_KEY = "sam-zoom";

function isWailsRuntime(): boolean {
    if (typeof window === "undefined") return false;
    return typeof (window as unknown as { runtime?: unknown }).runtime !== "undefined";
}

function clamp(value: number): number {
    if (Number.isNaN(value)) return ZOOM_DEFAULT;
    return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, value));
}

function roundStep(value: number): number {
    return Math.round(value * 100) / 100;
}

function readInitialZoom(): number {
    if (typeof window === "undefined") return ZOOM_DEFAULT;
    try {
        const raw = window.localStorage.getItem(LOCAL_STORAGE_KEY);
        if (!raw) return ZOOM_DEFAULT;
        const parsed = Number.parseFloat(raw);
        return clamp(parsed);
    } catch {
        return ZOOM_DEFAULT;
    }
}

interface ZoomProviderProps {
    children: ReactNode;
}

export const ZoomProvider: React.FC<ZoomProviderProps> = ({ children }) => {
    const isDesktop = useMemo(() => isWailsRuntime(), []);
    const [zoom, setZoomState] = useState<number>(() => (isDesktop ? readInitialZoom() : ZOOM_DEFAULT));

    const setZoom = useCallback((value: number) => {
        setZoomState(clamp(roundStep(value)));
    }, []);

    const zoomIn = useCallback(() => {
        setZoomState(prev => clamp(roundStep(prev + ZOOM_STEP)));
    }, []);

    const zoomOut = useCallback(() => {
        setZoomState(prev => clamp(roundStep(prev - ZOOM_STEP)));
    }, []);

    const reset = useCallback(() => {
        setZoomState(ZOOM_DEFAULT);
    }, []);

    useEffect(() => {
        if (!isDesktop) return;
        document.documentElement.style.zoom = String(zoom);
        try {
            window.localStorage.setItem(LOCAL_STORAGE_KEY, String(zoom));
        } catch {}
    }, [isDesktop, zoom]);

    useEffect(() => {
        if (!isDesktop) return;
        const handler = (e: KeyboardEvent) => {
            if (!(e.metaKey || e.ctrlKey)) return;
            switch (e.key) {
                case "=":
                case "+":
                    e.preventDefault();
                    zoomIn();
                    break;
                case "-":
                case "_":
                    e.preventDefault();
                    zoomOut();
                    break;
                case "0":
                    e.preventDefault();
                    reset();
                    break;
            }
        };
        window.addEventListener("keydown", handler);
        return () => window.removeEventListener("keydown", handler);
    }, [isDesktop, zoomIn, zoomOut, reset]);

    const value = useMemo<ZoomContextValue>(() => ({ zoom, isDesktop, setZoom, zoomIn, zoomOut, reset }), [zoom, isDesktop, setZoom, zoomIn, zoomOut, reset]);

    return <ZoomContext.Provider value={value}>{children}</ZoomContext.Provider>;
};
