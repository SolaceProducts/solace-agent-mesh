// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { activationKeys, useActivation, useCancelSignIn, useFreeModelSignIn } from "@/api/activation";

import { report } from "@/common/product-analytics/report";

import { getErrorMessage } from "@/utils/api";

import { useIsActivationGateEnabled } from "@/hooks";

import { useReturnToAppRetry } from "./useReturnToAppRetry";

export function useFreeModelSignInAgain() {
    const queryClient = useQueryClient();
    const gateEnabled = useIsActivationGateEnabled();
    const { data: activation } = useActivation(gateEnabled);
    const signIn = useFreeModelSignIn();
    const cancelSignIn = useCancelSignIn();
    const [startedAt, setStartedAt] = useState(0);

    const started = startedAt > 0;
    const waiting = signIn.isPending || (started && !!activation?.signingIn);
    const canRetry = useReturnToAppRetry(waiting, startedAt);
    let failure: string | null = null;
    if (started && !waiting) {
        failure = signIn.isError ? getErrorMessage(signIn.error, "The Solace ID sign-in failed. Try again.") : (activation?.signInFailure?.message ?? null);
    }
    const signedIn = started && !waiting && !failure && signIn.isSuccess;

    useEffect(() => {
        if (signedIn) queryClient.invalidateQueries({ queryKey: activationKeys.freeModel() });
    }, [signedIn, queryClient]);

    const start = async () => {
        report("free_model:sign_in_again_clicked");
        setStartedAt(Date.now());
        if (waiting) await cancelSignIn.mutateAsync().catch(() => undefined);
        signIn.mutate();
    };

    return { waiting, canRetry, failure, signedIn, start };
}
