// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { DEFAULT_WELCOME_MESSAGE, resolveWelcomeMessage } from "./welcomeMessage";

describe("resolveWelcomeMessage", () => {
    test.each([
        ["agent card wins over config, full mode", "Agent says hi", "Configured welcome", "full" as const, "Agent says hi"],
        ["agent card wins over config, agent mode", "Agent says hi", "Configured welcome", "agentMode" as const, "Agent says hi"],
        ["no agent card, full mode falls to config", undefined, "Configured welcome", "full" as const, "Configured welcome"],
        ["no agent card, agent mode skips config entirely", undefined, "Configured welcome", "agentMode" as const, DEFAULT_WELCOME_MESSAGE],
        ["nothing configured anywhere, full mode falls to default", undefined, null, "full" as const, DEFAULT_WELCOME_MESSAGE],
        ["nothing configured anywhere, agent mode falls to default", undefined, null, "agentMode" as const, DEFAULT_WELCOME_MESSAGE],
        ["an explicitly blank config message suppresses the greeting rather than falling to the default", undefined, "", "full" as const, ""],
        ["empty agent welcome_message is treated as absent", "", "Configured welcome", "full" as const, "Configured welcome"],
    ])("%s", (_name, agentWelcomeMessage, configWelcomeMessage, uiMode, expected) => {
        expect(resolveWelcomeMessage(agentWelcomeMessage, configWelcomeMessage, uiMode)).toBe(expected);
    });
});
