// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useLayoutEffect } from "react";

export function useReportBuildInterrupted(interrupted: boolean, report: ((interrupted: boolean) => void) | undefined): void {
    useLayoutEffect(() => {
        report?.(interrupted);
        return () => report?.(false);
    }, [interrupted, report]);
}
