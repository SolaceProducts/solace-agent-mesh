// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type FormEvent, useCallback, useEffect, useRef, useState } from "react";

import { useChatContext } from "@/providers/contexts";

import { configsWithoutManifest, finishBuildRequest, rewriteManifestRequest } from "./configsWithoutManifest";
import type { BuilderManifestState } from "./useBuilderManifest";

const REPORT_STUCK_AFTER_MS = 15_000;

export function useAutoRequestManifest(configCount: number, manifest: BuilderManifestState) {
    const { isResponding, handleSubmit, sessionId, isCancelling, selectedAgentName, addNotification } = useChatContext();
    const wasRespondingRef = useRef(false);
    const hasRequestedRef = useRef(false);
    const lastSessionRef = useRef(sessionId);
    const [turnCompleted, setTurnCompleted] = useState(false);
    const [reported, setReported] = useState(false);

    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        hasRequestedRef.current = false;
        if (!isResponding) wasRespondingRef.current = false;
        setTurnCompleted(false);
        setReported(false);
    }

    const stuck = configsWithoutManifest({
        configCount,
        manifest: manifest.manifest,
        manifestSettled: manifest.isSettled,
        manifestUnreadable: manifest.isUnreadable,
        isResponding,
    });

    useEffect(() => {
        if (wasRespondingRef.current && !isResponding) setTurnCompleted(true);
        wasRespondingRef.current = isResponding;
    }, [isResponding]);

    useEffect(() => {
        if (!stuck || !turnCompleted) return;

        if (hasRequestedRef.current) {
            setReported(true);
            return;
        }
        hasRequestedRef.current = true;
        const fakeEvent = new Event("submit") as unknown as FormEvent;
        handleSubmit(fakeEvent, [], finishBuildRequest(configCount), null, null, null, null, null, { hidden: true });

        const timer = setTimeout(() => setReported(true), REPORT_STUCK_AFTER_MS);
        return () => clearTimeout(timer);
    }, [stuck, turnCompleted, configCount, handleSubmit]);

    const requestAgain = useCallback(() => {
        if (isCancelling || !selectedAgentName) {
            addNotification("The builder isn't ready yet — try again in a moment.", "warning");
            return;
        }
        const fakeEvent = new Event("submit") as unknown as FormEvent;
        const request = manifest.isUnreadable ? rewriteManifestRequest(!!manifest.manifest) : finishBuildRequest(configCount);
        handleSubmit(fakeEvent, [], request, null, null, null, null, null);
    }, [handleSubmit, configCount, manifest.isUnreadable, manifest.manifest, isCancelling, selectedAgentName, addNotification]);

    const unreadable = manifest.isUnreadable && !isResponding;

    return { stuck: reported && stuck, unreadable, requestAgain };
}
