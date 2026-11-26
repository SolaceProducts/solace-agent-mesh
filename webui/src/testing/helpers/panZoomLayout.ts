// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { vi } from "vitest";

export const PAN_ZOOM_CONTENT_PADDING = 40;

export interface PanZoomLayout {
    canvasWidth: number;
    workflowWidth: number;
}

export function mockPanZoomLayout(layout: PanZoomLayout) {
    const contentBoxWidth = layout.workflowWidth + PAN_ZOOM_CONTENT_PADDING * 2;
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(layout.canvasWidth);
    vi.spyOn(HTMLElement.prototype, "offsetWidth", "get").mockImplementation(function (this: HTMLElement) {
        return this.style.transform ? contentBoxWidth : layout.workflowWidth;
    });
}

export function transformOf(container: HTMLElement): string {
    return container.querySelector<HTMLElement>('[style*="translate"]')!.style.transform;
}

export function workflowCentreOf(container: HTMLElement, workflowWidth: number): number {
    const transform = transformOf(container);
    const x = Number(/translate\((.+?)px/.exec(transform)?.[1]);
    const scale = Number(/scale\((.+?)\)/.exec(transform)?.[1]);
    return x + (PAN_ZOOM_CONTENT_PADDING + workflowWidth / 2) * scale;
}
