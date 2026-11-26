// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef } from "react";

import type { DataPart, MessageFE } from "@/types";

interface UseChatDeployTriggerArgs {
    sessionId: string | null;
    messages: MessageFE[] | undefined;
    canBuildActivate: boolean;
    saveOnlyBuild: boolean;
    isBuildActivating: boolean;
    onBuildActivate?: () => void;
}

export function useChatDeployTrigger({ sessionId, messages, canBuildActivate, saveOnlyBuild, isBuildActivating, onBuildActivate }: UseChatDeployTriggerArgs): void {
    const seenRef = useRef<Set<string>>(new Set());
    const pendingRef = useRef(false);

    const lastSessionRef = useRef(sessionId);
    if (sessionId !== lastSessionRef.current) {
        lastSessionRef.current = sessionId;
        pendingRef.current = false;
    }

    useEffect(() => {
        if (!messages?.length) return;
        for (const m of messages) {
            if (m.isUser || !m.isComplete) continue;
            const key = m.taskId || m.metadata?.messageId;
            if (!key || seenRef.current.has(key)) continue;
            const hasSignal = m.parts?.some(p => p.kind === "data" && (p as DataPart).data?.type === "request_build_activate");
            if (hasSignal) {
                seenRef.current.add(key);
                if (!saveOnlyBuild) pendingRef.current = true;
            }
        }
    }, [messages, saveOnlyBuild]);

    useEffect(() => {
        if (pendingRef.current && saveOnlyBuild) {
            pendingRef.current = false;
            return;
        }
        if (pendingRef.current && canBuildActivate && !isBuildActivating && onBuildActivate) {
            pendingRef.current = false;
            onBuildActivate();
        }
    }, [messages, canBuildActivate, saveOnlyBuild, isBuildActivating, onBuildActivate]);
}
