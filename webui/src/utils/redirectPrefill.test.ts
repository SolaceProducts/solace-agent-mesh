// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it } from "vitest";

import { clearPendingPrefill, consumePendingPrefill, stashPendingPrefill } from "./redirectPrefill";
import { SESSION_STORAGE_KEYS } from "./sessionStorageKeys";

describe("redirectPrefill", () => {
    afterEach(() => {
        sessionStorage.clear();
    });

    it("stashes an untargeted prefill and clears any stale target", () => {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION, "stale-target");
        stashPendingPrefill("hello", null);
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBe("hello");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBeNull();
    });

    it("consumes an untargeted prefill for any session", () => {
        stashPendingPrefill("hello", null);
        expect(consumePendingPrefill("whatever-session")).toBe("hello");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBeNull();
    });

    it("consumes a targeted prefill only for the target session, leaving it otherwise", () => {
        stashPendingPrefill("builder context", "dest-1");
        expect(consumePendingPrefill("other-session")).toBeNull();
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBe("builder context");

        expect(consumePendingPrefill("dest-1")).toBe("builder context");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBeNull();
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBeNull();
    });

    it("returns null and consumes nothing when no prefill is stashed", () => {
        expect(consumePendingPrefill("any")).toBeNull();
    });

    it("clearPendingPrefill removes both keys together", () => {
        stashPendingPrefill("text", "dest-1");
        clearPendingPrefill();
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBeNull();
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBeNull();
    });
});
