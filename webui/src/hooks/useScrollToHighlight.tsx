// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import type { RefObject } from "react";

export function useScrollToHighlight<T extends HTMLElement = HTMLElement>(containerRef: RefObject<T | null>, selector: string, enabled: boolean, dependencies: React.DependencyList = []): void {
    useEffect(() => {
        if (!enabled) return;

        requestAnimationFrame(() => {
            if (!containerRef.current) return;

            const firstHighlight = containerRef.current.querySelector(selector);
            if (firstHighlight) {
                firstHighlight.scrollIntoView({ behavior: "smooth", block: "center" });
            }
        });
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [enabled, ...dependencies]);
}

export function scrollToElement(element: Element, onComplete?: () => void): void {
    requestAnimationFrame(() => {
        element.scrollIntoView({ behavior: "smooth", block: "center" });
        onComplete?.();
    });
}
