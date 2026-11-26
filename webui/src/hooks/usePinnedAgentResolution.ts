// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

export const PINNED_AGENT_GRACE_MS = 10000;

export function usePinnedAgentResolution(isAwaiting: boolean, agentName?: string | null): { timedOut: boolean } {
    const [timedOut, setTimedOut] = useState(false);

    useEffect(() => {
        if (!isAwaiting) {
            setTimedOut(false);
            return;
        }
        const timer = setTimeout(() => {
            console.warn("pinned agent did not register within grace period", { agentName, graceMs: PINNED_AGENT_GRACE_MS });
            setTimedOut(true);
        }, PINNED_AGENT_GRACE_MS);
        return () => clearTimeout(timer);
    }, [isAwaiting, agentName]);

    return { timedOut };
}
