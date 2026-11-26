// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

const BROWSER_OPEN_GRACE_MS = 3_000;

export function useReturnToAppRetry(waiting: boolean, startedAt: number): boolean {
    const [returnedAt, setReturnedAt] = useState(0);

    useEffect(() => {
        if (!waiting) return;
        const markReturn = () => setReturnedAt(Date.now());
        const browserNeverOpened = setTimeout(() => {
            if (document.hasFocus()) markReturn();
        }, BROWSER_OPEN_GRACE_MS);
        window.addEventListener("focus", markReturn);
        return () => {
            clearTimeout(browserNeverOpened);
            window.removeEventListener("focus", markReturn);
        };
    }, [waiting, startedAt]);

    return waiting && returnedAt > startedAt;
}
