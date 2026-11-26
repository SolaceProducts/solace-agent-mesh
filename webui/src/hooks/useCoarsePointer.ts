// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { isMobileViewport, MOBILE_BREAKPOINT } from "./useMobile";

export function isTouchPrimaryDevice(): boolean {
    if (!globalThis.matchMedia) return false;
    if (globalThis.matchMedia("(pointer: coarse)")?.matches !== true) return false;
    if (isMobileViewport()) return true;
    return globalThis.matchMedia("(any-pointer: fine)")?.matches !== true;
}

export function useIsTouchPrimaryDevice() {
    const [touchPrimary, setTouchPrimary] = React.useState<boolean | undefined>(undefined);

    React.useEffect(() => {
        if (!globalThis.matchMedia) return;
        const coarseMql = globalThis.matchMedia("(pointer: coarse)");
        const finePointerMql = globalThis.matchMedia("(any-pointer: fine)");
        const mobileMql = globalThis.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
        const onChange = () => setTouchPrimary(isTouchPrimaryDevice());
        coarseMql.addEventListener("change", onChange);
        finePointerMql.addEventListener("change", onChange);
        mobileMql.addEventListener("change", onChange);
        onChange();
        return () => {
            coarseMql.removeEventListener("change", onChange);
            finePointerMql.removeEventListener("change", onChange);
            mobileMql.removeEventListener("change", onChange);
        };
    }, []);

    return !!touchPrimary;
}
