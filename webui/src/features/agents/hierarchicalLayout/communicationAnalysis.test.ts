// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any -- AgentCardInfoFE has many optional fields irrelevant to communication analysis. */
import { describe, expect, it } from "vitest";

import type { AgentCardInfoFE } from "@/types/agent";

import { analyzeAgentCommunication, canAgentCall, getCommunicationSummary } from "./communicationAnalysis";

function makeAgent(name: string, peerAgents: string[] = []): AgentCardInfoFE {
    return { name, peerAgents } as any;
}

describe("canAgentCall", () => {
    it("returns true when the target is in peerAgents", () => {
        expect(canAgentCall(makeAgent("A", ["B"]), "B")).toBe(true);
    });

    it("returns false when the target is not in peerAgents", () => {
        expect(canAgentCall(makeAgent("A", ["B"]), "C")).toBe(false);
    });

    it("returns false when peerAgents is empty", () => {
        expect(canAgentCall(makeAgent("A", []), "B")).toBe(false);
    });

    it("returns false when peerAgents is undefined", () => {
        expect(canAgentCall(makeAgent("A"), "B")).toBe(false);
    });
});

describe("analyzeAgentCommunication", () => {
    it("returns empty nodes/edges for an empty agent list", () => {
        expect(analyzeAgentCommunication([])).toEqual({ nodes: [], edges: [] });
    });

    it("creates a single bidirectional edge when two agents list each other", () => {
        const result = analyzeAgentCommunication([makeAgent("A", ["B"]), makeAgent("B", ["A"])]);
        expect(result.edges).toHaveLength(1);
        expect(result.edges[0].data?.communicationType).toBe("bidirectional");
    });

    it("creates a single unidirectional edge when only one side lists the other", () => {
        const result = analyzeAgentCommunication([makeAgent("A", ["B"]), makeAgent("B")]);
        expect(result.edges).toHaveLength(1);
        expect(result.edges[0].data?.communicationType).toBe("unidirectional");
        expect(result.edges[0].source).toBe("A");
        expect(result.edges[0].target).toBe("B");
    });

    it("creates no edges when no agent lists any peer", () => {
        const result = analyzeAgentCommunication([makeAgent("A"), makeAgent("B")]);
        expect(result.edges).toEqual([]);
    });
});

describe("getCommunicationSummary", () => {
    it("returns all zeros for an empty agent list", () => {
        expect(getCommunicationSummary([])).toEqual({
            totalAgents: 0,
            totalConnections: 0,
            bidirectionalConnections: 0,
            unidirectionalConnections: 0,
            isolatedAgents: 0,
        });
    });

    it("counts isolated agents when none have peers", () => {
        const summary = getCommunicationSummary([makeAgent("A"), makeAgent("B"), makeAgent("C")]);
        expect(summary.totalAgents).toBe(3);
        expect(summary.totalConnections).toBe(0);
        expect(summary.isolatedAgents).toBe(3);
    });

    it("counts a bidirectional pair as one connection and zero isolated", () => {
        const summary = getCommunicationSummary([makeAgent("A", ["B"]), makeAgent("B", ["A"])]);
        expect(summary.totalConnections).toBe(1);
        expect(summary.bidirectionalConnections).toBe(1);
        expect(summary.unidirectionalConnections).toBe(0);
        expect(summary.isolatedAgents).toBe(0);
    });

    it("separates connected agents from isolated ones in a mixed graph", () => {
        const summary = getCommunicationSummary([makeAgent("A", ["B"]), makeAgent("B", ["A"]), makeAgent("C"), makeAgent("D")]);
        expect(summary.totalAgents).toBe(4);
        expect(summary.bidirectionalConnections).toBe(1);
        expect(summary.unidirectionalConnections).toBe(0);
        expect(summary.isolatedAgents).toBe(2);
    });
});
