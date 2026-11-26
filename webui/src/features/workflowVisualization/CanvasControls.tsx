// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Scan, ZoomIn, ZoomOut } from "lucide-react";
import type { FC } from "react";

import { Button } from "@/ui/button";

export interface CanvasControlsProps {
    zoomLevel: number;
    onZoomIn: () => void;
    onZoomOut: () => void;
    onFitToView: () => void;
    minZoom?: number;
    maxZoom?: number;
}

export const CanvasControls: FC<CanvasControlsProps> = ({ zoomLevel, onZoomIn, onZoomOut, onFitToView, minZoom = 0.25, maxZoom = 2 }) => {
    const zoomPercentage = Math.round(zoomLevel * 100);

    const isAtMinZoom = zoomLevel <= minZoom;
    const isAtMaxZoom = zoomLevel >= maxZoom;

    return (
        <div className="flex items-center justify-end gap-2 border-b px-4 py-2">
            <Button onClick={onFitToView} variant="ghost" size="sm" tooltip="Center Workflow">
                <Scan className="h-4 w-4" />
            </Button>
            <div className="h-6 w-px border-l" />
            <div className="flex items-center gap-1">
                <Button onClick={onZoomOut} disabled={isAtMinZoom} variant="ghost" size="sm" tooltip="Zoom out (10%)">
                    <ZoomOut className="h-4 w-4" />
                </Button>

                <span className="min-w-[3.5rem] text-center text-sm font-medium">{zoomPercentage}%</span>

                <Button onClick={onZoomIn} disabled={isAtMaxZoom} variant="ghost" size="sm" tooltip="Zoom in (10%)">
                    <ZoomIn className="h-4 w-4" />
                </Button>
            </div>
        </div>
    );
};

export default CanvasControls;
