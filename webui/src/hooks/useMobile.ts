// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

export const MOBILE_BREAKPOINT = 768;

export function isMobileViewport(): boolean {
    return globalThis.innerWidth < MOBILE_BREAKPOINT;
}

export function useIsMobile() {
    const [isMobile, setIsMobile] = React.useState<boolean | undefined>(undefined);

    React.useEffect(() => {
        const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
        const onChange = () => setIsMobile(isMobileViewport());
        mql.addEventListener("change", onChange);
        onChange();
        return () => mql.removeEventListener("change", onChange);
    }, []);

    return !!isMobile;
}
