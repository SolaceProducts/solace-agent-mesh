// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { editAgentLoader, editExternalAgentLoader, newAgentLoader } from "./router";

describe("agent create/edit route loaders", () => {
    test("newAgentLoader flags a fresh agent", () => {
        expect(newAgentLoader()).toEqual({ isNew: true });
    });

    test("editAgentLoader carries the agent id and clears the new flag", () => {
        expect(editAgentLoader({ params: { id: "agent-1" } })).toEqual({
            agentId: "agent-1",
            isNew: false,
        });
    });

    test("editExternalAgentLoader carries the remote agent id in edit mode", () => {
        expect(editExternalAgentLoader({ params: { id: "remote-1" } })).toEqual({
            remoteAgentId: "remote-1",
            isEdit: true,
        });
    });
});
