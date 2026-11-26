// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { activeNavItemId } from "./activeNavItem";

describe("activeNavItemId", () => {
    test.each([
        ["/budgets", "budgets"],
        ["/models", "models"],
        ["/user-management", "user-management"],
        ["/agents/new", "agents"],
        ["/agent-mesh", "agentMesh"],
        ["/evaluations/lab", "eval-lab"],
        ["/evaluations", "eval-reports"],
        ["/feedback/history", "feedback"],
        ["/usage", "usage"],
    ])("%s highlights %s", (path, id) => {
        expect(activeNavItemId(path)).toBe(id);
    });

    test.each(["/", "/chat", "/agent-mode/chat", "/recent-chats", "/unknown"])("%s highlights chat", path => {
        expect(activeNavItemId(path)).toBe("chat");
    });
});
