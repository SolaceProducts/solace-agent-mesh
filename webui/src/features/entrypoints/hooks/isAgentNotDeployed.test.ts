// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { type AgentOption, isAgentNotDeployed } from "./useAgentOptions";

function makeAgent(overrides: Partial<AgentOption> = {}): AgentOption {
    return {
        name: "agent-broker-name",
        displayName: "My Agent",
        ...overrides,
    };
}

describe("isAgentNotDeployed", () => {
    describe("returns false (agent is usable)", () => {
        test("when option is undefined", () => {
            expect(isAgentNotDeployed(undefined)).toBe(false);
        });

        test("when deploymentStatus is 'deployed'", () => {
            const agent = makeAgent({ deploymentStatus: "deployed" });
            expect(isAgentNotDeployed(agent)).toBe(false);
        });

        test("when deploymentStatus is undefined (discovered-only agent on broker)", () => {
            const agent = makeAgent({ deploymentStatus: undefined });
            expect(isAgentNotDeployed(agent)).toBe(false);
        });
    });

    describe("returns true (agent is NOT deployed)", () => {
        test("when deploymentStatus is 'not_deployed'", () => {
            const agent = makeAgent({ deploymentStatus: "not_deployed" });
            expect(isAgentNotDeployed(agent)).toBe(true);
        });

        test("when deploymentStatus is any other non-deployed lifecycle value", () => {
            const agent = makeAgent({ deploymentStatus: "deploy_failed" });
            expect(isAgentNotDeployed(agent)).toBe(true);
        });

        test("when deploymentStatus is '' (backend sentinel for not-deployed)", () => {
            const agent = makeAgent({ deploymentStatus: "" });
            expect(isAgentNotDeployed(agent)).toBe(true);
        });
    });
});
