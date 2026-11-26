// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useRef } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { api } from "@/api/client";

import { useBuilderContext, useChatContext } from "@/providers/contexts";
import { BUILDER_AGENT_NAME } from "@/providers/contexts/BuilderContext";

import { isEphemeralTestAgentName } from "@/utils/agentUtils";

export function BuilderTabAutoSwitch({ hasBuilderArtifact }: { hasBuilderArtifact: boolean }) {
    const { activeSidePanelTab, setActiveSidePanelTab } = useChatContext();
    const hasSwitchedRef = useRef(false);

    useEffect(() => {
        if (hasBuilderArtifact && !hasSwitchedRef.current && activeSidePanelTab !== "builder") {
            setActiveSidePanelTab("builder");
            hasSwitchedRef.current = true;
        }
        if (!hasBuilderArtifact) {
            hasSwitchedRef.current = false;
        }
    }, [hasBuilderArtifact, activeSidePanelTab, setActiveSidePanelTab]);

    return null;
}

export function BuilderModeEffects() {
    const { builderMode } = useBuilderContext();
    const { selectedAgentName, setSelectedAgentName, handleNewSession } = useChatContext();
    const navigate = useNavigate();
    const location = useLocation();
    const previousAgentRef = useRef<string | null>(null);
    const initialPathnameRef = useRef(location.pathname);
    const hasActivatedBuilderRef = useRef(false);

    const handleNewSessionRef = useRef(handleNewSession);
    handleNewSessionRef.current = handleNewSession;
    const selectedAgentNameRef = useRef(selectedAgentName);
    selectedAgentNameRef.current = selectedAgentName;
    const locationPathnameRef = useRef(location.pathname);
    locationPathnameRef.current = location.pathname;

    useEffect(() => {
        if (builderMode) {
            if (selectedAgentNameRef.current !== BUILDER_AGENT_NAME) {
                previousAgentRef.current = selectedAgentNameRef.current;
            }

            const onBuilderRoute = locationPathnameRef.current === "/builder";
            const hasPendingSessionRedirect = !!sessionStorage.getItem("sam_agent_session_redirect");
            const hasPendingTestSessionPin = !!sessionStorage.getItem("sam_test_session_pin");
            const isRefreshIntoBuilder = !hasActivatedBuilderRef.current && onBuilderRoute && initialPathnameRef.current === "/builder";
            hasActivatedBuilderRef.current = true;

            if (!isRefreshIntoBuilder && !hasPendingSessionRedirect && !hasPendingTestSessionPin) {
                handleNewSessionRef.current();
            }
            setSelectedAgentName(BUILDER_AGENT_NAME);
            navigate("/builder");
        } else if (previousAgentRef.current) {
            setSelectedAgentName(previousAgentRef.current);
            previousAgentRef.current = null;
            if (locationPathnameRef.current.startsWith("/builder")) {
                navigate("/chat");
            }
        }
    }, [builderMode, setSelectedAgentName, navigate]);

    useEffect(() => {
        if (builderMode && selectedAgentName !== BUILDER_AGENT_NAME) {
            setSelectedAgentName(BUILDER_AGENT_NAME);
        }
    }, [builderMode, selectedAgentName, setSelectedAgentName]);

    return null;
}

export function BuilderSessionRedirector({ navigate }: { navigate: (path: string) => void }) {
    const { builderMode } = useBuilderContext();
    const { sessionId, handleSwitchSession } = useChatContext();
    const location = useLocation();
    const prevSessionIdRef = useRef(sessionId);

    useEffect(() => {
        if (sessionId === prevSessionIdRef.current) return;
        prevSessionIdRef.current = sessionId;
        if (!sessionId || builderMode || location.pathname === "/builder") return;

        const controller = new AbortController();
        api.webui
            .get(`/api/v1/sessions/${sessionId}`, { signal: controller.signal })
            .then((res: unknown) => {
                if (controller.signal.aborted) return;
                const session = res as { agentId?: string; isEphemeralTest?: boolean } | null;
                const agentId = session?.agentId;
                if (agentId === "Builder") {
                    sessionStorage.setItem("sam_agent_session_redirect", sessionId);
                    navigate("/builder");
                } else if (session?.isEphemeralTest === true || isEphemeralTestAgentName(agentId)) {
                    sessionStorage.setItem("sam_test_session_pin", sessionId);
                    navigate("/builder");
                }
            })
            .catch(() => {});

        return () => controller.abort();
    }, [sessionId, builderMode, navigate, location.pathname]);

    useEffect(() => {
        const handler = (e: Event) => {
            const { sessionId: targetSessionId, route, agentId: clickedAgentId, isEphemeralTest: clickedIsEphemeral } = (e as CustomEvent).detail;
            const isTestSessionClick = clickedIsEphemeral === true || isEphemeralTestAgentName(clickedAgentId);
            if (location.pathname === route && !isTestSessionClick) {
                handleSwitchSession(targetSessionId);
            } else {
                if (isTestSessionClick) {
                    sessionStorage.setItem("sam_test_session_pin", targetSessionId);
                } else {
                    sessionStorage.setItem("sam_agent_session_redirect", targetSessionId);
                }
                navigate(route);
            }
        };
        window.addEventListener("agent-session-navigate", handler);
        return () => window.removeEventListener("agent-session-navigate", handler);
    }, [navigate, handleSwitchSession, location.pathname]);

    return null;
}
