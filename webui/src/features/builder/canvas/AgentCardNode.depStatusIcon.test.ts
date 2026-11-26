// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleCheckBig, Clock, Loader2, Pause } from "lucide-react";
import { describe, expect, test } from "vitest";

import { DepStatusIcon } from "./AgentCardNode";

describe("DepStatusIcon", () => {
    test("maps each dependency status to its icon", () => {
        expect(DepStatusIcon({ status: "done" }).type).toBe(CircleCheckBig);
        expect(DepStatusIcon({ status: "generating" }).type).toBe(Loader2);
        expect(DepStatusIcon({ status: "waiting" }).type).toBe(Pause);
        expect(DepStatusIcon({ status: "queued" }).type).toBe(Clock);
    });
});
