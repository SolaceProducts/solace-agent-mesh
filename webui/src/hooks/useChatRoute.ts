// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { UI_MODE, useUIModeContext } from "@/providers/contexts";

export function useChatRoute(): string {
    const { mode, pinnedAgentName } = useUIModeContext();
    if (mode !== UI_MODE.agentMode) return "/chat";
    return pinnedAgentName ? `/agent-mode/chat?agent=${encodeURIComponent(pinnedAgentName)}` : "/agent-mode/chat";
}
