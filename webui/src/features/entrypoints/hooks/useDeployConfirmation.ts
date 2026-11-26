// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useRef, useState } from "react";

import type { Gateway } from "@/types";

interface Settle {
    resolve: () => void;
    reject: (error: unknown) => void;
}

export interface DeployConfirmation {
    pending: Gateway | null;
    ask: (gateway: Gateway) => Promise<void>;
    confirm: () => void;
    close: () => void;
}

export function useDeployConfirmation(saveAndDeploy: (gateway: Gateway) => Promise<void>): DeployConfirmation {
    const [pending, setPending] = useState<Gateway | null>(null);
    const settleRef = useRef<Settle | null>(null);

    const takeSettle = () => {
        const settle = settleRef.current;
        settleRef.current = null;
        setPending(null);
        return settle;
    };

    const ask = useCallback(
        (gateway: Gateway) =>
            new Promise<void>((resolve, reject) => {
                settleRef.current = { resolve, reject };
                setPending(gateway);
            }),
        []
    );

    const confirm = () => {
        if (!pending) return;
        const settle = takeSettle();
        saveAndDeploy(pending).then(settle?.resolve, settle?.reject);
    };

    const close = () => takeSettle()?.resolve();

    return { pending, ask, confirm, close };
}
