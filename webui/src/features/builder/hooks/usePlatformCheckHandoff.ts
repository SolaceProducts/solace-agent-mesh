// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { PLATFORM_CHECK_CHIP, type PlatformCheck, platformCheckMessage } from "../platformCheck";

import type { AutoFixEvent, AutoFixOutcome } from "./autoFixMachine";

export interface UsePlatformCheckHandoffArgs {
    check: PlatformCheck;
    key: string | null;
    idle: boolean;
    postable: boolean;
    sessionId: string;
    agentResponded: boolean;
    reportHeld: boolean;
    autoFixExhausted: boolean;
    dispatchAutoFix: (event: AutoFixEvent) => AutoFixOutcome;
    postToBuilder: (message: string, displayChip: string) => void;
}

export interface UsePlatformCheckHandoffResult {
    canAskBuilder: boolean;
    askBuilder: () => void;
}

const reportKeyOf = (checkKey: string) => `platform-check|${checkKey}`;

export function usePlatformCheckHandoff({ check, key, idle, postable, sessionId, agentResponded, reportHeld, autoFixExhausted, dispatchAutoFix, postToBuilder }: UsePlatformCheckHandoffArgs): UsePlatformCheckHandoffResult {
    const reportedKeys = useRef(new Set<string>());
    const lastRefusedKey = useRef<string | null>(null);
    const handoffSession = useRef(sessionId);
    const [declinedKey, setDeclinedKey] = useState<string | null>(null);
    const [askedKey, setAskedKey] = useState<string | null>(null);
    const [coldKey, setColdKey] = useState<string | null>(null);
    const askedKeyRef = useRef<string | null>(null);

    useEffect(() => {
        if (handoffSession.current !== sessionId) {
            handoffSession.current = sessionId;
            reportedKeys.current.clear();
            lastRefusedKey.current = null;
        }
        if (key === null) return;

        if (check.kind === "passed") {
            if (lastRefusedKey.current !== null && lastRefusedKey.current !== key && dispatchAutoFix({ type: "refusalFixed" }).accepted !== false) {
                lastRefusedKey.current = null;
                reportedKeys.current.clear();
            }
            return;
        }
        if (check.kind !== "refused") return;

        lastRefusedKey.current = key;
        if (!idle || reportHeld || reportedKeys.current.has(key)) return;
        if (!agentResponded) {
            setColdKey(key);
            return;
        }
        reportedKeys.current.add(key);
        setColdKey(null);

        const message = platformCheckMessage(check.problems);
        const outcome = dispatchAutoFix({ type: "report", fix: { message, displayChip: PLATFORM_CHECK_CHIP, key: reportKeyOf(key), sessionId, errorText: message }, responding: false });
        if (outcome.accepted === false) setDeclinedKey(key);
    }, [check, key, idle, sessionId, agentResponded, reportHeld, dispatchAutoFix]);

    const refusedKey = check.kind === "refused" ? key : null;
    const canAskBuilder = refusedKey !== null && askedKey !== refusedKey && (autoFixExhausted || declinedKey === refusedKey || coldKey === refusedKey);

    const askBuilder = useCallback(() => {
        if (!idle || !postable || check.kind !== "refused" || key === null || askedKeyRef.current === key) return;
        postToBuilder(platformCheckMessage(check.problems), PLATFORM_CHECK_CHIP);
        askedKeyRef.current = key;
        reportedKeys.current.add(key);
        setAskedKey(key);
    }, [check, key, idle, postable, postToBuilder]);

    return { canAskBuilder, askBuilder };
}
