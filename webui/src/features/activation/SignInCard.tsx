// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useRef, useState } from "react";
import { Sparkles } from "lucide-react";

import { type Activation, useCancelSignIn, useSignIn } from "@/api/activation";
import type { ApiError } from "@/api/client";

import { MessageBanner } from "@/common/banners/MessageBanner";
import { report } from "@/common/product-analytics/report";

import { getErrorMessage } from "@/utils/api";

import { Button } from "@/ui";
import { cn } from "@/utils";

import { AgentMeshBrand } from "./AgentMeshBrand";
import { PoweredByClaude } from "./PoweredByClaude";
import { useReturnToAppRetry } from "./useReturnToAppRetry";

const LOST_SIGN_IN_MESSAGE = "Solace Agent Mesh lost track of the sign-in. If you finished signing in, this screen updates on its own. Otherwise, sign in again.";

function signInErrorMessage(err: unknown): string {
    if ((err as ApiError).status === undefined) return LOST_SIGN_IN_MESSAGE;
    return getErrorMessage(err, "The Solace ID sign-in failed. Try again.");
}

function reportStartFailure(err: unknown) {
    const { status } = err as ApiError;
    let signInFailure: "request_failed" | "unavailable" | "failed" = "failed";
    if (status === undefined) signInFailure = "request_failed";
    else if (status === 503) signInFailure = "unavailable";
    report("activation:sign_in_failed", { signInFailure });
}

export function SignInCard({ activation }: Readonly<{ activation: Activation }>) {
    const [error, setError] = useState<string | null>(null);
    const [startedAt, setStartedAt] = useState(0);
    const signIn = useSignIn();
    const cancelSignIn = useCancelSignIn();
    const latestAttempt = useRef(0);

    const waiting = signIn.isPending || activation.signingIn;
    const failure = error ?? (waiting ? null : (activation.signInFailure?.message ?? null));
    const canRetry = useReturnToAppRetry(waiting, startedAt);

    const startSignIn = async () => {
        const attempt = ++latestAttempt.current;
        report("activation:sign_in_clicked");
        setError(null);
        setStartedAt(Date.now());
        try {
            if (waiting) await cancelSignIn.mutateAsync();
            await signIn.mutateAsync();
        } catch (err) {
            reportStartFailure(err);
            if (attempt === latestAttempt.current) setError(signInErrorMessage(err));
        }
    };

    return (
        <dialog open aria-modal="true" aria-labelledby="activation-sign-in-title" className="static flex max-h-none w-full max-w-none flex-col gap-4 rounded-lg bg-(--background-w10) p-6 text-inherit shadow-xl">
            <AgentMeshBrand />
            <h1 id="activation-sign-in-title" className={cn("text-xl leading-snug font-semibold text-balance text-(--primary-text-wMain)", waiting && "sr-only")}>
                Sign in with your Solace ID to start building your first agent.
            </h1>
            <div hidden={waiting} className="flex flex-col gap-2 rounded-lg bg-(--secondary-w10) p-4 text-sm text-(--primary-text-wMain)">
                <p className="flex items-start gap-2">
                    <Sparkles className="mt-0.5 size-4 shrink-0 text-(--primary-text-wMain)" aria-hidden />
                    <span>
                        <strong className="font-medium">Free model access comes with your Solace ID.</strong> Start prompting and building agents right away, no API keys needed.
                    </span>
                </p>
                <PoweredByClaude className="self-end" />
            </div>
            {failure && <MessageBanner variant="error" message={failure} />}
            {waiting && !failure && <output className="block text-sm text-(--secondary-text-wMain)">Finish signing in with Solace ID in your browser. This screen updates on its own.</output>}
            <div className="flex flex-col gap-4">
                <Button className="w-full" disabled={waiting && !canRetry} loading={waiting && !canRetry} onClick={startSignIn}>
                    {waiting ? "Sign In Again" : "Sign In"}
                </Button>
                <p className="text-center text-xs text-(--secondary-text-wMain)">New to Solace? You can create a free Solace ID on the next page.</p>
            </div>
        </dialog>
    );
}
