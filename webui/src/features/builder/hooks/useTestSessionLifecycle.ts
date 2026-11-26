// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";
import { v4 as uuidv4 } from "uuid";

import { api } from "@/api/client";

export interface UseTestSessionLifecycleArgs {
    builderSessionId: string;
    testAgentName: string;
    targetName: string;
    pinnedTestSessionId?: string | null;
    onPinnedTestSessionApplied?: () => void;
}

export interface UseTestSessionLifecycleResult {
    testSessionId: string | null;
    sessionError: string | null;
    retry: () => void;
}

export function useTestSessionLifecycle({ builderSessionId, testAgentName, targetName, pinnedTestSessionId, onPinnedTestSessionApplied }: UseTestSessionLifecycleArgs): UseTestSessionLifecycleResult {
    const persistKey = `sam_test_session::${builderSessionId}::${testAgentName}`;
    const readPersisted = (): string | null => {
        try {
            return localStorage.getItem(persistKey);
        } catch {
            return null;
        }
    };

    const [testSessionId, setTestSessionId] = useState<string | null>(null);
    const [sessionError, setSessionError] = useState<string | null>(null);
    const [adopted, setAdopted] = useState(false);
    const [retryCount, setRetryCount] = useState(0);
    const creatingRef = useRef(false);
    const pinnedAppliedRef = useRef(false);
    if (pinnedTestSessionId && !pinnedAppliedRef.current) {
        pinnedAppliedRef.current = true;
        queueMicrotask(() => onPinnedTestSessionApplied?.());
    }

    useEffect(() => {
        if (adopted) return;
        const candidate = pinnedTestSessionId ?? readPersisted();
        if (!candidate) {
            setAdopted(true);
            return;
        }
        let cancelled = false;
        api.webui
            .get(`/api/v1/sessions/${candidate}`, { fullResponse: true })
            .then(res => {
                if (cancelled) return;
                if (res.ok) {
                    setTestSessionId(candidate);
                } else if (res.status === 404) {
                    try {
                        localStorage.removeItem(persistKey);
                    } catch {}
                }
                setAdopted(true);
            })
            .catch(() => {
                if (cancelled) return;
                setAdopted(true);
            });
        return () => {
            cancelled = true;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    useEffect(() => {
        if (!testSessionId) return;
        try {
            localStorage.setItem(persistKey, testSessionId);
        } catch {}
    }, [testSessionId, persistKey]);

    useEffect(() => {
        if (!adopted) return;
        if (creatingRef.current || testSessionId) return;
        creatingRef.current = true;
        const newSessionId = uuidv4();
        const sessionName = `Test: ${targetName}`;
        api.webui
            .post("/api/v1/sessions", { id: newSessionId, agentId: testAgentName, source: "builder_test" })
            .then(() =>
                api.webui.patch(`/api/v1/sessions/${newSessionId}`, { name: sessionName }).catch(() => {
                    console.warn("[useTestSessionLifecycle] failed to rename test session");
                })
            )
            .then(() => setTestSessionId(newSessionId))
            .catch((err: unknown) => {
                console.error("[useTestSessionLifecycle] create test session", err);
                setSessionError("Failed to create test session.");
                creatingRef.current = false;
            });
    }, [adopted, builderSessionId, testAgentName, targetName, testSessionId, retryCount]);

    const retry = useCallback(() => {
        setSessionError(null);
        creatingRef.current = false;
        setTestSessionId(null);
        setRetryCount(c => c + 1);
    }, []);

    return { testSessionId, sessionError, retry };
}
