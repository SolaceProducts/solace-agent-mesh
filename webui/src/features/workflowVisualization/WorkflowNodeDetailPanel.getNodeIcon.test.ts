// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Bot, CircleCheckBig, Play, Signpost } from "lucide-react";
import { describe, expect, test } from "vitest";

import { getNodeIcon } from "./WorkflowNodeDetailPanel";

describe("getNodeIcon", () => {
    test("maps node types to their header icon", () => {
        expect(getNodeIcon("end")?.type).toBe(CircleCheckBig);
        expect(getNodeIcon("start")?.type).toBe(Play);
        expect(getNodeIcon("agent")?.type).toBe(Bot);
        expect(getNodeIcon("decision")?.type).toBe(Signpost);
    });
});
