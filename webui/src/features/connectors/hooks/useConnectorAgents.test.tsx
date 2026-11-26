// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { agentsApi } from "@/api/agents";

import type { Agent, Connector } from "@/types";

import { useConnectorAgents } from "./useConnectorAgents";

const connector = { id: "c1", agentCount: 1 } as Connector;

describe("useConnectorAgents", () => {
    afterEach(() => vi.restoreAllMocks());

    test("lists the connector's agents", async () => {
        const list = vi.spyOn(agentsApi, "getByConnector").mockResolvedValue([{ id: "a1" } as Agent]);

        const { result } = renderHook(() => useConnectorAgents({ connector }));

        await waitFor(() => expect(result.current.agents).toHaveLength(1));
        expect(list).toHaveBeenCalledWith("c1");
    });
});
