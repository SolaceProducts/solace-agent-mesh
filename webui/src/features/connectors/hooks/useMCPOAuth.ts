// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { mcpOAuthApi } from "@/api/mcp-oauth/versioned";

import type { MCPOAuthInitiateRequest, MCPOAuthMessage, MCPTool } from "@/types";

const POLL_INTERVAL_MS = 1500;
const OAUTH_TIMEOUT_MS = 5 * 60 * 1000;

export interface UseMCPOAuthReturn {
    startOAuthFlow: (request: MCPOAuthInitiateRequest) => Promise<void>;
    discoverTools: (sessionId: string, serverUrl: string, connectionType: string, customHeaders?: Record<string, string> | null) => Promise<MCPTool[] | null>;
    isAuthenticating: boolean;
    isDiscovering: boolean;
    error: string | null;
    sessionId: string | null;
    setError: (error: string | null) => void;
}

export const useMCPOAuth = (): UseMCPOAuthReturn => {
    const [isAuthenticating, setIsAuthenticating] = useState(false);
    const [isDiscovering, setIsDiscovering] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [sessionId, setSessionId] = useState<string | null>(null);

    const watchersRef = useRef<{
        poll?: ReturnType<typeof setInterval>;
        popup?: ReturnType<typeof setInterval>;
        timeout?: ReturnType<typeof setTimeout>;
    }>({});

    const isMountedRef = useRef(true);

    const stopWatchers = useCallback(() => {
        const w = watchersRef.current;
        if (w.poll) clearInterval(w.poll);
        if (w.popup) clearInterval(w.popup);
        if (w.timeout) clearTimeout(w.timeout);
        watchersRef.current = {};
    }, []);

    const handleOAuthSuccess = useCallback(
        (receivedSessionId: string) => {
            stopWatchers();
            setIsAuthenticating(false);
            setSessionId(receivedSessionId);
            setError(null);
        },
        [stopWatchers]
    );

    const handleOAuthError = useCallback(
        (errorDescription: string) => {
            stopWatchers();
            setError(errorDescription || "OAuth authorization failed");
            setIsAuthenticating(false);
            setSessionId(null);
        },
        [stopWatchers]
    );

    useEffect(() => {
        isMountedRef.current = true;
        return () => {
            isMountedRef.current = false;
            stopWatchers();
        };
    }, [stopWatchers]);

    useEffect(() => {
        const handleMessage = (event: MessageEvent<MCPOAuthMessage>) => {
            const data = event.data;

            if (data?.type === "mcp-oauth-callback") {
                handleOAuthSuccess(data.sessionId);
            } else if (data?.type === "mcp-oauth-error") {
                handleOAuthError(data.error);
            }
        };

        window.addEventListener("message", handleMessage);
        return () => window.removeEventListener("message", handleMessage);
    }, [handleOAuthSuccess, handleOAuthError]);

    const startOAuthFlow = useCallback(
        async (request: MCPOAuthInitiateRequest) => {
            setIsAuthenticating(true);
            setError(null);
            setSessionId(null);
            stopWatchers();

            try {
                const responseData = await mcpOAuthApi.initiate(request);
                if (!isMountedRef.current) return;
                const authorizationUrl = responseData.authorizationUrl;
                const pendingSessionId: string | undefined = responseData.sessionId;

                const popup = window.open(authorizationUrl, "mcp_oauth", "width=600,height=700,popup=yes,left=100,top=100");

                if (!popup) {
                    throw new Error("Popup blocked. Please allow popups for this site.");
                }

                if (pendingSessionId) {
                    watchersRef.current.poll = setInterval(async () => {
                        try {
                            const { status } = await mcpOAuthApi.getStatus(pendingSessionId);
                            if (!watchersRef.current.poll) return;
                            if (status === "completed") {
                                handleOAuthSuccess(pendingSessionId);
                            } else if (status === "expired") {
                                handleOAuthError("Authorization session expired or was not found. Please try again.");
                            }
                        } catch {}
                    }, POLL_INTERVAL_MS);

                    watchersRef.current.timeout = setTimeout(() => {
                        setIsAuthenticating(prev => {
                            if (prev) {
                                stopWatchers();
                                setError("Authorization timed out. Please try again.");
                            }
                            return false;
                        });
                    }, OAUTH_TIMEOUT_MS);
                }

                watchersRef.current.popup = setInterval(() => {
                    if (popup.closed) {
                        setIsAuthenticating(prev => {
                            if (prev) {
                                stopWatchers();
                                setError("Authorization failed. Possible causes: window was closed, server is rate limiting, or redirect URI needs whitelisting.");
                            }
                            return false;
                        });
                    }
                }, 500);
            } catch (err: unknown) {
                stopWatchers();
                const errorMessage = err instanceof Error ? err.message : "An unknown error occurred";
                setError(errorMessage);
                setIsAuthenticating(false);
            }
        },
        [handleOAuthSuccess, handleOAuthError, stopWatchers]
    );

    const discoverTools = useCallback(async (discoverySessionId: string, serverUrl: string, connectionType: string, customHeaders?: Record<string, string> | null): Promise<MCPTool[] | null> => {
        setIsDiscovering(true);
        setError(null);
        try {
            const responseData = await mcpOAuthApi.discover({
                sessionId: discoverySessionId,
                serverUrl,
                connectionType,
                ...(customHeaders ? { customHeaders } : {}),
            });

            const tools = responseData.tools || [];
            return tools;
        } catch (err: unknown) {
            const errorMessage = err instanceof Error ? err.message : "An unknown error occurred during tool discovery";
            setError(errorMessage);
            return null;
        } finally {
            setIsDiscovering(false);
        }
    }, []);

    return {
        startOAuthFlow,
        discoverTools,
        isAuthenticating,
        isDiscovering,
        error,
        sessionId,
        setError,
    };
};
