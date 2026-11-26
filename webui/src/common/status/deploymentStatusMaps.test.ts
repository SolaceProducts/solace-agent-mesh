// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import type { RuntimeStatusValue } from "@/types";

import { isOperationalStatus } from "./deploymentStatusMaps";

describe("isOperationalStatus", () => {
    it("treats 'running' as operational", () => {
        expect(isOperationalStatus("running")).toBe(true);
    });

    it("treats 'degraded' as operational — still up, just reporting a problem", () => {
        expect(isOperationalStatus("degraded")).toBe(true);
    });

    it.each<RuntimeStatusValue | undefined>(["starting", "disconnected", "stopped", undefined])("treats %s as not operational", status => {
        expect(isOperationalStatus(status)).toBe(false);
    });
});
