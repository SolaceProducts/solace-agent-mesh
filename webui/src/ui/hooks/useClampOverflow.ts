// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useLayoutEffect, useRef, useState } from "react";

export function useClampOverflow<T extends HTMLElement = HTMLDivElement>(clampActive: boolean, deps: ReadonlyArray<unknown> = []): { ref: React.RefObject<T | null>; isOverflowing: boolean } {
    const ref = useRef<T | null>(null);
    const [isOverflowing, setIsOverflowing] = useState(false);

    useLayoutEffect(() => {
        const el = ref.current;
        if (!el) return;
        const measure = () => {
            if (clampActive) {
                setIsOverflowing(el.scrollHeight > el.clientHeight + 1);
            }
        };
        measure();
        const ro = new ResizeObserver(measure);
        ro.observe(el);
        return () => ro.disconnect();
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [clampActive, ...deps]);

    return { ref, isOverflowing };
}
