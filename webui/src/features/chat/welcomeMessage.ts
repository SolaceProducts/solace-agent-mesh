// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UIModeName } from "@/providers/contexts/UIModeContext";

export const DEFAULT_WELCOME_MESSAGE = "What should we work on?";

export function resolveWelcomeMessage(agentWelcomeMessage: string | undefined, configWelcomeMessage: string | null, uiMode: UIModeName): string {
    if (agentWelcomeMessage) {
        return agentWelcomeMessage;
    }
    if (uiMode === "agentMode") {
        return DEFAULT_WELCOME_MESSAGE;
    }
    return configWelcomeMessage ?? DEFAULT_WELCOME_MESSAGE;
}
