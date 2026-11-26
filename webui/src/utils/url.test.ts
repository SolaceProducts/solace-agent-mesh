// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, it, vi } from "vitest";

import { clearSignInCompleted, consumePostLoginRedirect, getHashPath, getHashQueryParams, isHttpUrl, markSignInCompleted, signInJustCompleted, stashPostLoginRedirect } from "./url";

const POST_LOGIN_REDIRECT_KEY = "sam_post_login_redirect";

describe("getHashQueryParams", () => {
    afterEach(() => {
        window.history.replaceState({}, "", "/");
    });

    it("parses the query segment that follows the hash route", () => {
        window.history.replaceState({}, "", "/#/agent-mode/chat?agent=Foo");
        expect(getHashQueryParams().get("agent")).toBe("Foo");
    });

    it("returns no params when the hash route carries no query", () => {
        window.history.replaceState({}, "", "/#/agent-mode/chat");
        expect(getHashQueryParams().get("agent")).toBeNull();
    });

    it("ignores a real query string before the hash (reads hash, not search)", () => {
        window.history.replaceState({}, "", "/?agent=Foo#/agent-mode/chat");
        expect(window.location.search).toBe("?agent=Foo");
        expect(getHashQueryParams().get("agent")).toBeNull();
    });
});

describe("getHashPath", () => {
    afterEach(() => {
        window.history.replaceState({}, "", "/");
    });

    it("returns the route path without the leading # or query segment", () => {
        window.history.replaceState({}, "", "/#/agent-mode/chat?agent=Foo");
        expect(getHashPath()).toBe("/agent-mode/chat");
    });

    it("returns the path when there is no query", () => {
        window.history.replaceState({}, "", "/#/chat");
        expect(getHashPath()).toBe("/chat");
    });

    it("returns empty string when there is no hash", () => {
        window.history.replaceState({}, "", "/");
        expect(getHashPath()).toBe("");
    });
});

describe("post-login redirect stash/restore", () => {
    afterEach(() => {
        localStorage.removeItem(POST_LOGIN_REDIRECT_KEY);
        window.history.replaceState({}, "", "/");
    });

    it("stashes the current URL and restores it once", () => {
        window.history.replaceState({}, "", "/#/agent-mode/chat?agent=Foo");
        stashPostLoginRedirect();
        expect(localStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toContain("/agent-mode/chat");

        const restored = consumePostLoginRedirect();
        expect(restored).toContain("/#/agent-mode/chat?agent=Foo");
    });

    it("is one-shot — the key is cleared after the first read", () => {
        window.history.replaceState({}, "", "/#/agent-mode/chat?agent=Foo");
        stashPostLoginRedirect();

        consumePostLoginRedirect();
        expect(localStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBeNull();
        expect(consumePostLoginRedirect()).toBe("/");
    });

    it("falls back to / when nothing was stashed", () => {
        expect(consumePostLoginRedirect()).toBe("/");
    });

    it("rejects a cross-origin stashed value (open-redirect guard)", () => {
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, "https://evil.example.com/#/agent-mode/chat?agent=Foo");
        expect(consumePostLoginRedirect()).toBe("/");
    });

    it("rejects an unparseable stashed value", () => {
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, "not-a-url");
        expect(consumePostLoginRedirect()).toBe("/");
    });
});

describe("sign-in completed marker", () => {
    afterEach(() => {
        vi.useRealTimers();
        sessionStorage.clear();
    });

    it("counts as just completed only for a minute, and not once cleared", () => {
        vi.useFakeTimers();
        markSignInCompleted();
        expect(signInJustCompleted()).toBe(true);
        vi.advanceTimersByTime(59_000);
        expect(signInJustCompleted()).toBe(true);
        vi.advanceTimersByTime(2_000);
        expect(signInJustCompleted()).toBe(false);

        markSignInCompleted();
        clearSignInCompleted();
        expect(signInJustCompleted()).toBe(false);
    });

    it("ignores a value that is not a timestamp", () => {
        sessionStorage.setItem("sam_sign_in_completed", "true");
        expect(signInJustCompleted()).toBe(false);
    });
});

describe("isHttpUrl", () => {
    it("accepts well-formed http(s) URLs", () => {
        expect(isHttpUrl("https://api.example.com")).toBe(true);
        expect(isHttpUrl("https://api.example.com/v1")).toBe(true);
        expect(isHttpUrl("http://localhost:8080")).toBe(true);
    });

    it("treats blank as valid (compose with a separate required check)", () => {
        expect(isHttpUrl("")).toBe(true);
        expect(isHttpUrl(null)).toBe(true);
        expect(isHttpUrl(undefined)).toBe(true);
        expect(isHttpUrl("   ")).toBe(true);
    });

    it("rejects a scheme with no //", () => {
        expect(isHttpUrl("https:lite-llm.example.com")).toBe(false);
    });

    it("rejects a bare hostname with no scheme", () => {
        expect(isHttpUrl("lite-llm.example.com")).toBe(false);
        expect(isHttpUrl("api.example.com/v1")).toBe(false);
    });

    it("rejects embedded whitespace", () => {
        expect(isHttpUrl("https://foo bar.com")).toBe(false);
        expect(isHttpUrl("https:// foo.com")).toBe(false);
    });

    it("rejects unsupported schemes", () => {
        expect(isHttpUrl("ftp://api.example.com")).toBe(false);
        expect(isHttpUrl("ws://api.example.com")).toBe(false);
    });

    it("rejects http(s) with no host", () => {
        expect(isHttpUrl("https://")).toBe(false);
        expect(isHttpUrl("http://")).toBe(false);
        expect(isHttpUrl("https:")).toBe(false);
    });
});
