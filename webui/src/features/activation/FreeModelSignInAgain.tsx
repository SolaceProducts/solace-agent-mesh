// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { MessageBanner } from "@/common/banners/MessageBanner";

import { Button } from "@/ui";

import { useFreeModelSignInAgain } from "./useFreeModelSignInAgain";

interface FreeModelSignInAgainProps {
    signedInMessage: string;
    variant?: "default" | "outline" | "ghost";
    showWaitingHint?: boolean;
}

export function FreeModelSignInAgain({ signedInMessage, variant = "default", showWaitingHint = true }: Readonly<FreeModelSignInAgainProps>) {
    const { waiting, canRetry, failure, signedIn, start } = useFreeModelSignInAgain();

    if (signedIn) return <p className="text-sm text-(--secondary-text-wMain)">{signedInMessage}</p>;
    return (
        <div className="flex flex-col items-start gap-2">
            {failure && <MessageBanner variant="error" message={failure} />}
            {waiting && showWaitingHint && <output className="block text-sm text-(--secondary-text-wMain)">Finish signing in with Solace ID in your browser.</output>}
            <Button variant={variant} disabled={waiting && !canRetry} loading={waiting && !canRetry} onClick={start}>
                Sign In Again
            </Button>
        </div>
    );
}
