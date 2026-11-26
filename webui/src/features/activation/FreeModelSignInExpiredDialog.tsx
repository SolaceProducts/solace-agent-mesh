// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { MessageBanner } from "@/common/banners/MessageBanner";

import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/ui";

import { useFreeModelSignInExpired } from "./freeModelSignInExpired";
import { useFreeModelSignInAgain } from "./useFreeModelSignInAgain";

export function FreeModelSignInExpiredDialog() {
    return useFreeModelSignInExpired() ? <SignInExpiredDialog /> : null;
}

function SignInExpiredDialog() {
    const [dismissed, setDismissed] = useState(false);
    const { waiting, canRetry, failure, signedIn, start } = useFreeModelSignInAgain();

    return (
        <Dialog open={!dismissed && !signedIn} onOpenChange={open => !open && setDismissed(true)}>
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>Sign In Again to Keep Using Free Credits</DialogTitle>
                    <DialogDescription>The Solace ID sign-in for the free model has expired. Chats and agents on the free model stop until you sign in again.</DialogDescription>
                </DialogHeader>
                {waiting && <output className="mt-4 block text-sm text-(--secondary-text-wMain)">Finish signing in with Solace ID in your browser.</output>}
                {failure && <MessageBanner className="mt-4" variant="error" message={failure} />}
                <DialogFooter>
                    <Button variant="ghost" onClick={() => setDismissed(true)}>
                        Not Now
                    </Button>
                    <Button autoFocus disabled={waiting && !canRetry} loading={waiting && !canRetry} onClick={start}>
                        Sign In Again
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
}
