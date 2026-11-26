// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { UI_MODE, useUIModeContext } from "@/providers/contexts";
import { AGENT_MODE_ROUTE_PREFIX } from "@/providers/UIModeProvider";

import { useChatRoute } from "@/hooks";

export const UIModeGuard = () => {
    const { mode, pinnedAgentName } = useUIModeContext();

    const chatRoute = useChatRoute();
    const location = useLocation();
    const navigate = useNavigate();

    useEffect(() => {
        if (mode !== UI_MODE.agentMode) return;

        const offSurface = !location.pathname.startsWith(AGENT_MODE_ROUTE_PREFIX);
        const lostAgent = !!pinnedAgentName && !new URLSearchParams(location.search).get("agent");

        if (offSurface || lostAgent) {
            navigate(chatRoute, { replace: true });
        }
    }, [mode, pinnedAgentName, chatRoute, location.pathname, location.search, navigate]);

    return null;
};
