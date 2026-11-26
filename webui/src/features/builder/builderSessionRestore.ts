// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type RefObject, useEffect, useRef } from "react";

import { api } from "@/api/client";

import { useChatContext } from "@/providers/contexts";
import { BUILDER_AGENT_NAME } from "@/providers/contexts/BuilderContext";

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

const loadedOnBuilder = globalThis.location.hash.replace(/^#/, "").split("?")[0] === "/builder";
let restoreClaimed = false;

export function rememberBuilderSession(sessionId: string): void {
    try {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.ACTIVE_BUILDER_SESSION, sessionId);
    } catch (err) {
        console.warn("[builder] could not remember the active session; a reload will start a new build", err);
    }
}

export function forgetBuilderSession(onlyIf?: string): void {
    try {
        if (onlyIf === undefined || sessionStorage.getItem(SESSION_STORAGE_KEYS.ACTIVE_BUILDER_SESSION) === onlyIf) {
            sessionStorage.removeItem(SESSION_STORAGE_KEYS.ACTIVE_BUILDER_SESSION);
        }
    } catch (err) {
        console.warn("[builder] could not forget the active session", err);
    }
}

function hasExplicitBuilderTarget(): boolean {
    return !!(sessionStorage.getItem("sam_agent_session_redirect") || sessionStorage.getItem("sam_test_session_pin") || sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT));
}

export function restorableBuilderSession(): string | null {
    if (!loadedOnBuilder || restoreClaimed) return null;
    try {
        return hasExplicitBuilderTarget() ? null : sessionStorage.getItem(SESSION_STORAGE_KEYS.ACTIVE_BUILDER_SESSION);
    } catch (err) {
        console.warn("[builder] could not read the remembered session", err);
        return null;
    }
}

export function claimBuilderSessionRestore(): void {
    restoreClaimed = true;
}

type SessionCheck = "reopenable" | "gone" | "unknown";

export async function checkBuilderSession(sessionId: string): Promise<SessionCheck> {
    try {
        const session = await api.webui.get<{ agentId?: string } | null>(`/api/v1/sessions/${sessionId}`);
        return session?.agentId === BUILDER_AGENT_NAME ? "reopenable" : "gone";
    } catch (err) {
        if ((err as { status?: number }).status === 404) return "gone";
        console.warn("[builder] could not check the remembered session; keeping it for the next reload", err);
        return "unknown";
    }
}

export function useBuilderSessionRestore(agentsLoading: boolean, redirectAppliedRef: RefObject<boolean>): void {
    const { sessionId, isResponding, isLoadingSession, handleSwitchSession } = useChatContext();
    const restoreSessionRef = useRef(restorableBuilderSession());
    const busyRef = useRef(false);
    busyRef.current = !!sessionId || isResponding || isLoadingSession;
    const mountedRef = useRef(false);

    useEffect(() => {
        mountedRef.current = true;
        claimBuilderSessionRestore();
        return () => {
            mountedRef.current = false;
        };
    }, []);

    useEffect(() => {
        if (sessionId) rememberBuilderSession(sessionId);
        else if (!restoreSessionRef.current) forgetBuilderSession();
    }, [sessionId]);

    useEffect(() => {
        const sid = restoreSessionRef.current;
        if (!sid || agentsLoading) return;
        restoreSessionRef.current = null;
        const reopen = async () => {
            const check = await checkBuilderSession(sid);
            if (check === "gone") forgetBuilderSession(sid);
            if (check !== "reopenable" || !mountedRef.current || busyRef.current) return;
            redirectAppliedRef.current = true;
            if (!(await handleSwitchSession(sid))) {
                console.warn("[builder] could not reopen the remembered session; keeping it for the next reload");
            }
        };
        reopen().catch(err => console.warn("[builder] reopening the remembered session failed", err));
    }, [agentsLoading, handleSwitchSession, redirectAppliedRef]);
}
