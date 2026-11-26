// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import type { EventRule } from "@/types";

import { EventRuleCard } from "./EventRuleCard";
import type { AgentOption } from "./hooks";

let mockAgentOptions: AgentOption[] = [];

vi.mock("./hooks", async () => {
    const actual = await vi.importActual<typeof import("./hooks")>("./hooks");
    return { ...actual, useAgentOptions: () => mockAgentOptions };
});

const rule = (targetAgent: string): EventRule => ({ name: "r", targetAgent, subscriptions: [{ topic: "a/b" }], messageFormat: "json" }) as unknown as EventRule;

describe("EventRuleCard agent name resolution", () => {
    beforeEach(() => {
        mockAgentOptions = [];
    });

    test("prefers the useAgentOptions display name for a not-deployed target the discovery map misses", () => {
        mockAgentOptions = [{ name: "agent_019e", displayName: "My Custom Agent", deploymentStatus: "not_deployed" }];

        renderWithProviders(<EventRuleCard rule={rule("agent_019e")} />, {
            chatContextValues: { agentNameDisplayNameMap: {} },
        });

        expect(screen.getByText(/Agent: My Custom Agent/)).toBeInTheDocument();
        expect(screen.queryByText(/agent_019e/)).not.toBeInTheDocument();
    });

    test("falls back to the discovery map when the option list has no match", () => {
        renderWithProviders(<EventRuleCard rule={rule("broker-name")} />, {
            chatContextValues: { agentNameDisplayNameMap: { "broker-name": "Discovered Name" } },
        });

        expect(screen.getByText(/Agent: Discovered Name/)).toBeInTheDocument();
    });

    test("falls back to the raw target when neither source knows it", () => {
        renderWithProviders(<EventRuleCard rule={rule("unknown-agent")} />, {
            chatContextValues: { agentNameDisplayNameMap: {} },
        });

        expect(screen.getByText(/Agent: unknown-agent/)).toBeInTheDocument();
    });
});
