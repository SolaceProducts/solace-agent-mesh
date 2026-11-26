// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";

import { api } from "@/api/client";

const PLACEHOLDER_TITLE = "New Chat";

const POLL_DELAYS_MS = [1500, 3000, 5000, 7000];

const inFlightSessions = new Set<string>();

function dispatchTitleEvent(name: string, detail: Record<string, unknown>) {
    globalThis.dispatchEvent(new CustomEvent(name, { detail }));
}

export const useTitleGeneration = () => {
    const generateTitle = useCallback(async (sessionId: string, userMessage: string, agentResponse: string, currentTitle?: string, force: boolean = false): Promise<void> => {
        if (!sessionId || sessionId === "" || sessionId === "null") {
            console.warn("[useTitleGeneration] Invalid session ID, skipping title generation");
            return;
        }

        if (!force && !userMessage) {
            console.warn("[useTitleGeneration] Missing user message, skipping title generation");
            return;
        }

        const guarded = !force;
        if (guarded) {
            if (inFlightSessions.has(sessionId)) {
                console.debug("[useTitleGeneration] Generation already in flight for session, skipping duplicate");
                return;
            }
            inFlightSessions.add(sessionId);
        }
        const releaseGuard = () => {
            if (guarded) inFlightSessions.delete(sessionId);
        };

        const initialTitle = currentTitle || PLACEHOLDER_TITLE;

        dispatchTitleEvent("session-title-generating", { sessionId, isGenerating: true });

        let skipped: boolean;
        try {
            const resp = await api.webui.post<{ skipped?: boolean }>(`/api/v1/sessions/${sessionId}/generateTitle`, {
                userMessage,
                agentResponse,
                force,
            });
            skipped = resp?.skipped === true;
        } catch (error) {
            console.warn("[useTitleGeneration] Title generation failed:", error);
            dispatchTitleEvent("session-title-generating", { sessionId, isGenerating: false });
            releaseGuard();
            return;
        }

        if (skipped) {
            dispatchTitleEvent("session-title-generating", { sessionId, isGenerating: false });
            dispatchTitleEvent("session-title-updated", { sessionId });
            releaseGuard();
            return;
        }

        console.debug("[useTitleGeneration] Title generation triggered, polling for update...");

        const pollForTitle = async () => {
            for (const delay of POLL_DELAYS_MS) {
                await new Promise(resolve => setTimeout(resolve, delay));

                try {
                    const sessionData = await api.webui.get<{ name?: string | null }>(`/api/v1/sessions/${sessionId}`);
                    const currentName = sessionData?.name;

                    if (currentName && currentName !== initialTitle) {
                        dispatchTitleEvent("session-title-generating", { sessionId, isGenerating: false });
                        dispatchTitleEvent("session-title-updated", { sessionId });
                        return;
                    }
                } catch (error) {
                    console.error("[useTitleGeneration] Error polling for title:", error);
                }
            }

            console.warn("[useTitleGeneration] Title generation polling timed out - dispatching event anyway");
            dispatchTitleEvent("session-title-generating", { sessionId, isGenerating: false });
            dispatchTitleEvent("session-title-updated", { sessionId });
        };

        pollForTitle()
            .catch(error => {
                console.error("[useTitleGeneration] Unexpected error in polling:", error);
                dispatchTitleEvent("session-title-generating", { sessionId, isGenerating: false });
            })
            .finally(() => {
                releaseGuard();
            });
    }, []);

    return { generateTitle };
};
