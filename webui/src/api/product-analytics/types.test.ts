// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { ConsentState } from "./types";
import { isCollecting, isKnownConsentState } from "./types";

const EVERY_STATE: Record<ConsentState, { collecting: boolean; known: boolean }> = {
    unset: { collecting: true, known: true },
    granted: { collecting: true, known: true },
    denied: { collecting: false, known: true },
};

describe("consent state predicates", () => {
    test.each(Object.entries(EVERY_STATE))("classifies %s", (state, want) => {
        const consent = { state: state as ConsentState };
        expect(isCollecting(consent)).toBe(want.collecting);
        expect(isKnownConsentState(consent)).toBe(want.known);
    });

    test.each([
        ["no response yet", undefined],
        ["a response with no state", {}],
        ["a state from a newer server", { state: "granted_limited" }],
        ["a null state", { state: null }],
    ])("treats %s as neither collecting nor known, so the gate stays shut and the toggle stays disabled", (_label, body) => {
        const consent = body as Parameters<typeof isCollecting>[0];
        expect(isCollecting(consent)).toBe(false);
        expect(isKnownConsentState(consent)).toBe(false);
    });
});
