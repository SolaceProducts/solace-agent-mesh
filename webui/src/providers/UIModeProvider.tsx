// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type ReactNode, useState } from "react";

import { agentUIMode, FULL_UI_MODE, type UIMode, UIModeContext } from "@/providers/contexts/UIModeContext";

import { getHashPath, getHashQueryParams } from "@/utils/url";

export const AGENT_MODE_ROUTE_PREFIX = "/agent-mode/";

function computeUIMode(): UIMode {
    const path = getHashPath();

    if (path.startsWith(AGENT_MODE_ROUTE_PREFIX)) {
        return agentUIMode(getHashQueryParams().get("agent"));
    }

    return FULL_UI_MODE;
}

interface UIModeProviderProps {
    children: ReactNode;
}

export const UIModeProvider: React.FC<UIModeProviderProps> = ({ children }) => {
    const [uiMode] = useState<UIMode>(computeUIMode);
    return <UIModeContext.Provider value={uiMode}>{children}</UIModeContext.Provider>;
};
