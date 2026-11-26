// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { EmptyState } from "@/common";

export const SIGN_IN_HANDOFF_CLOSE_DELAY_MS = 4000;

function tabIsScriptClosable(): boolean {
    return globalThis.history.length <= 1 || globalThis.opener != null;
}

interface SignedInHandoffProps {
    sourceLabel: string;
    onContinue: () => void;
}

export function SignedInHandoff({ sourceLabel, onContinue }: Readonly<SignedInHandoffProps>) {
    const [closeRefused, setCloseRefused] = useState(() => !tabIsScriptClosable());

    useEffect(() => {
        if (closeRefused) return;
        const timer = globalThis.setTimeout(() => {
            globalThis.close();
            if (!globalThis.closed) setCloseRefused(true);
        }, SIGN_IN_HANDOFF_CLOSE_DELAY_MS);
        return () => globalThis.clearTimeout(timer);
    }, [closeRefused]);

    const guidance = closeRefused ? `Close this window to return to ${sourceLabel}, or select the button below to open Agent Mesh.` : `This window will close shortly. Go back to ${sourceLabel} to continue.`;
    return (
        <div className="h-screen bg-(--background-w10)">
            <EmptyState variant="noImage" title="You're signed in" subtitle={<output>{guidance}</output>} buttons={closeRefused ? [{ text: "Continue to Agent Mesh", variant: "outline", onClick: onContinue }] : undefined} />
        </div>
    );
}

interface AlreadySignedInHandoffProps {
    sourceLabel: string;
    account: string | null;
    onContinue: () => void;
    onUseDifferentAccount: () => void;
}

export function AlreadySignedInHandoff({ sourceLabel, account, onContinue, onUseDifferentAccount }: Readonly<AlreadySignedInHandoffProps>) {
    const signedInAs = account ? `You're signed in as ${account}. ` : "";
    return (
        <div className="h-screen bg-(--background-w10)">
            <EmptyState
                variant="noImage"
                title="You're already signed in"
                subtitle={`${signedInAs}If ${sourceLabel} still asks you to sign in, this may not be the account your administrator registered.`}
                buttons={[
                    { text: "Use a different account", variant: "default", onClick: onUseDifferentAccount },
                    { text: "Continue to Agent Mesh", variant: "outline", onClick: onContinue },
                ]}
            />
        </div>
    );
}
