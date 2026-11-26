// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

export interface ZoomContextValue {
    zoom: number;
    isDesktop: boolean;
    setZoom: (value: number) => void;
    zoomIn: () => void;
    zoomOut: () => void;
    reset: () => void;
}

export const ZoomContext = createContext<ZoomContextValue | null>(null);

export const ZOOM_MIN = 0.5;
export const ZOOM_MAX = 2.0;
export const ZOOM_STEP = 0.1;
export const ZOOM_DEFAULT = 1.0;

export function useZoomContext(): ZoomContextValue {
    const context = useContext(ZoomContext);
    if (context === null) {
        throw new Error("useZoomContext must be used within a ZoomProvider");
    }
    return context;
}
