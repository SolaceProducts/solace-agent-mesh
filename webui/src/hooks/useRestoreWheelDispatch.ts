// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";

export function useRestoreWheelDispatch() {
    useEffect(() => {
        const keepWheelOnMainThread = () => {};
        document.addEventListener("wheel", keepWheelOnMainThread, { capture: true, passive: false });
        document.addEventListener("wheel", keepWheelOnMainThread, { passive: false });
        return () => {
            document.removeEventListener("wheel", keepWheelOnMainThread, { capture: true });
            document.removeEventListener("wheel", keepWheelOnMainThread);
        };
    }, []);
}
