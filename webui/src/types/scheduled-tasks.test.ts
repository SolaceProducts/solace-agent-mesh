// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { deriveTaskStatus } from "@/types/scheduled-tasks";

describe("deriveTaskStatus", () => {
    it("returns 'active' when enabled with no failures", () => {
        expect(deriveTaskStatus(true, 0)).toBe("active");
    });

    it("returns 'error' when enabled with failures", () => {
        expect(deriveTaskStatus(true, 3)).toBe("error");
    });

    it("returns 'paused' when disabled with no failures", () => {
        expect(deriveTaskStatus(false, 0)).toBe("paused");
    });

    it("returns 'paused' (not 'error') when disabled with failures", () => {
        expect(deriveTaskStatus(false, 3)).toBe("paused");
    });
});
