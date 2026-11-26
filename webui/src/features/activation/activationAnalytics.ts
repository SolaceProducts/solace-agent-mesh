// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef } from "react";

import type { Activation } from "@/api/activation";

import { report } from "@/common/product-analytics/report";
import type { ProductAnalyticsSetupPath, ProductAnalyticsSignInFailure } from "@/common/product-analytics/types";

const SIGN_IN_FAILURES: ReadonlySet<string> = new Set<ProductAnalyticsSignInFailure>([
    "canceled",
    "denied",
    "different_account",
    "failed",
    "grant_in_use",
    "no_callback_port",
    "no_refresh_token",
    "not_saved",
    "request_failed",
    "timed_out",
    "unavailable",
]);

export function signInFailure(code: string | undefined): ProductAnalyticsSignInFailure {
    return code && SIGN_IN_FAILURES.has(code) ? (code as ProductAnalyticsSignInFailure) : "other";
}

export function setupPath(freeModel: boolean): ProductAnalyticsSetupPath {
    return freeModel ? "free_model" : "own_model";
}

export function useSignInAnalytics(activation: Activation | undefined) {
    const previous = useRef<Activation | undefined>(undefined);
    useEffect(() => {
        const before = previous.current;
        previous.current = activation;
        if (!before || !activation || before.state !== "signed_out") return;
        if (activation.state === "eula_pending") {
            report("activation:signed_in");
            return;
        }
        if (before.signingIn && !activation.signingIn && activation.signInFailure) {
            report("activation:sign_in_failed", { signInFailure: signInFailure(activation.signInFailure.code) });
        }
    }, [activation]);
}
