// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { KeyboardEvent } from "react";
import { describe, expect, it } from "vitest";

import { isImeComposing } from "./ime";

const keyEvent = (nativeEvent: Partial<globalThis.KeyboardEvent>): KeyboardEvent => ({ nativeEvent }) as KeyboardEvent;

describe("isImeComposing", () => {
    it("is true while composing via isComposing", () => {
        expect(isImeComposing(keyEvent({ isComposing: true, keyCode: 13 }))).toBe(true);
    });

    it("is true while composing via keyCode 229", () => {
        expect(isImeComposing(keyEvent({ isComposing: false, keyCode: 229 }))).toBe(true);
    });

    it("is false when no composition is active", () => {
        expect(isImeComposing(keyEvent({ isComposing: false, keyCode: 13 }))).toBe(false);
    });
});
