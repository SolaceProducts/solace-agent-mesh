// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { LOGIN_POPUP_NAME } from "@/utils/loginRedirect";
import type { TeamsSessionTokens } from "@/utils/teamsAuth";
import { signInJustCompleted } from "@/utils/url";

import AuthCallback from "./authCallback";

const { completeTeamsSignIn, failTeamsSignIn } = vi.hoisted(() => ({
    completeTeamsSignIn: vi.fn<(tokens: TeamsSessionTokens) => Promise<boolean>>(),
    failTeamsSignIn: vi.fn<(reason: string) => Promise<void>>(),
}));
vi.mock("@/utils/teamsAuth", async importActual => ({
    ...(await importActual<typeof import("@/utils/teamsAuth")>()),
    completeTeamsSignIn,
    failTeamsSignIn,
}));

const settle = () =>
    act(async () => {
        await Promise.resolve();
    });

const LOGIN_URL = "/api/v1/auth/login";
const RETRY_COUNT_KEY = "sam_auth_retry_count";
const POST_LOGIN_REDIRECT_KEY = "sam_post_login_redirect";
const MAX_AUTO_RETRIES = 5;

function setHash(hash: string) {
    window.history.replaceState({}, "", `/auth-callback#${hash}`);
}

function nameWindowAsSignInPopup() {
    window.name = LOGIN_POPUP_NAME;
}

function stubClosingWindow() {
    const close = vi.fn();
    vi.stubGlobal("close", close);
    vi.stubGlobal("closed", true);
    return close;
}

describe("AuthCallback", () => {
    beforeEach(() => {
        vi.spyOn(console, "error").mockImplementation(() => {});
        vi.spyOn(console, "warn").mockImplementation(() => {});
        completeTeamsSignIn.mockResolvedValue(false);
        failTeamsSignIn.mockResolvedValue(undefined);
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
        completeTeamsSignIn.mockReset();
        failTeamsSignIn.mockReset();
        localStorage.clear();
        sessionStorage.clear();
        window.name = "";
        window.history.replaceState({}, "", "/");
    });

    it("shows an actionable error (not a permanent spinner) when the IdP returns no token", () => {
        setHash("");
        render(<AuthCallback />);

        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
        expect(screen.queryByText(/Signing you in/i)).not.toBeInTheDocument();
        expect(screen.getByRole("link", { name: /Try again/i })).toHaveAttribute("href", LOGIN_URL);
    });

    it("surfaces the IdP error_description", () => {
        setHash("error=access_denied&error_description=User%20declined%20access");
        render(<AuthCallback />);

        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
        expect(screen.getByText("User declined access")).toBeInTheDocument();
    });

    it("auto-retries a transient callback failure instead of stranding the user", () => {
        setHash("error=service_unavailable&error_description=Authorization%20service%20unavailable&retryable=1");
        render(<AuthCallback />);

        expect(screen.getByText("Finishing sign-in…")).toBeInTheDocument();
        expect(screen.getByText(/Retrying automatically in \d+s/i)).toBeInTheDocument();
        expect(screen.queryByText("Sign-in failed")).not.toBeInTheDocument();
        expect(sessionStorage.getItem(RETRY_COUNT_KEY)).toBe("1");
    });

    it("stops auto-retrying once the retry budget is exhausted", () => {
        sessionStorage.setItem(RETRY_COUNT_KEY, String(MAX_AUTO_RETRIES));
        setHash("error=service_unavailable&retryable=1");
        render(<AuthCallback />);

        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
        expect(screen.queryByText("Finishing sign-in…")).not.toBeInTheDocument();
        expect(sessionStorage.getItem(RETRY_COUNT_KEY)).toBeNull();
    });

    it("does not auto-retry when storage can't bound the loop (private mode)", () => {
        vi.stubGlobal("sessionStorage", {
            getItem: vi.fn(() => null),
            setItem: vi.fn(() => {
                throw new DOMException("storage disabled", "SecurityError");
            }),
            removeItem: vi.fn(() => {
                throw new DOMException("storage disabled", "SecurityError");
            }),
            clear: vi.fn(),
        });
        setHash("error=service_unavailable&retryable=1");
        render(<AuthCallback />);

        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
        expect(screen.queryByText("Finishing sign-in…")).not.toBeInTheDocument();
    });

    it("hands the session to the opener and closes when it ran in a sign-in popup", () => {
        const postMessage = vi.fn();
        vi.stubGlobal("opener", { postMessage });
        const close = stubClosingWindow();
        nameWindowAsSignInPopup();
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, `${window.location.origin}/chat?agent=demo`);
        setHash("access_token=abc123&sam_access_token=sam1&refresh_token=r1");

        render(<AuthCallback />);

        expect(postMessage).toHaveBeenCalledWith(
            {
                type: "sam:auth-complete",
                accessToken: "abc123",
                samAccessToken: "sam1",
                refreshToken: "r1",
            },
            window.location.origin
        );
        expect(close).toHaveBeenCalled();
        expect(localStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBe(`${window.location.origin}/chat?agent=demo`);
        expect(window.location.pathname).toBe("/auth-callback");
    });

    it("stays in this window when the opener is itself (not a popup)", async () => {
        const postMessage = vi.spyOn(window, "postMessage").mockImplementation(() => {});
        const close = vi.fn();
        vi.stubGlobal("opener", window.self);
        vi.stubGlobal("close", close);
        setHash("access_token=abc123");

        render(<AuthCallback />);
        await settle();

        expect(postMessage).not.toHaveBeenCalled();
        expect(close).not.toHaveBeenCalled();
        expect(localStorage.getItem("access_token")).toBe("abc123");
    });

    it("completes in a window that has an opener but was not opened as the sign-in popup", async () => {
        const postMessage = vi.fn();
        const close = vi.fn();
        vi.stubGlobal("opener", { postMessage });
        vi.stubGlobal("close", close);
        setHash("access_token=abc123");

        render(<AuthCallback />);
        await settle();

        expect(postMessage).toHaveBeenCalledWith(expect.objectContaining({ type: "sam:auth-complete", accessToken: "abc123" }), window.location.origin);
        expect(close).not.toHaveBeenCalled();
        expect(localStorage.getItem("access_token")).toBe("abc123");
        expect(screen.queryByText("Sign-in failed")).not.toBeInTheDocument();
    });

    it("completes in this window when the opener cannot be messaged", async () => {
        const postMessage = vi.fn(() => {
            throw new DOMException("target origin mismatch", "SecurityError");
        });
        const close = vi.fn();
        vi.stubGlobal("opener", { postMessage });
        vi.stubGlobal("close", close);
        nameWindowAsSignInPopup();
        setHash("access_token=abc123");

        render(<AuthCallback />);
        await settle();

        expect(close).not.toHaveBeenCalled();
        expect(localStorage.getItem("access_token")).toBe("abc123");
        expect(screen.queryByText("Sign-in failed")).not.toBeInTheDocument();
    });

    it("finishes the sign-in itself when the popup survives the close", async () => {
        const postMessage = vi.fn();
        vi.stubGlobal("opener", { postMessage });
        vi.stubGlobal("close", vi.fn());
        nameWindowAsSignInPopup();
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, `${window.location.origin}/chat?agent=demo`);
        setHash("access_token=abc123");

        render(<AuthCallback />);
        await settle();

        expect(postMessage).toHaveBeenCalledWith(expect.objectContaining({ type: "sam:auth-complete", accessToken: "abc123" }), window.location.origin);
        expect(localStorage.getItem("access_token")).toBe("abc123");
        expect(localStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBeNull();
        expect(screen.queryByText("Sign-in failed")).not.toBeInTheDocument();
    });

    it("finishes the sign-in itself when closing the popup throws", async () => {
        const postMessage = vi.fn();
        vi.stubGlobal("opener", { postMessage });
        vi.stubGlobal("close", () => {
            throw new DOMException("scripts may not close windows they did not open", "InvalidAccessError");
        });
        nameWindowAsSignInPopup();
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, `${window.location.origin}/chat?agent=demo`);
        setHash("access_token=abc123");

        render(<AuthCallback />);
        await settle();

        expect(postMessage).toHaveBeenCalledWith(expect.objectContaining({ type: "sam:auth-complete", accessToken: "abc123" }), window.location.origin);
        expect(localStorage.getItem("access_token")).toBe("abc123");
        expect(localStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBeNull();
        expect(screen.queryByText("Sign-in failed")).not.toBeInTheDocument();
    });

    it("leaves the sign-in to the opener when the popup is really closing", () => {
        const postMessage = vi.fn();
        vi.stubGlobal("opener", { postMessage });
        stubClosingWindow();
        nameWindowAsSignInPopup();
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, `${window.location.origin}/chat?agent=demo`);
        setHash("access_token=abc123");

        render(<AuthCallback />);

        expect(localStorage.getItem("access_token")).toBeNull();
        expect(localStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBe(`${window.location.origin}/chat?agent=demo`);
        expect(window.location.pathname).toBe("/auth-callback");
    });

    it("still hands off to the opener when the popup's own storage is blocked", () => {
        const postMessage = vi.fn();
        vi.stubGlobal("opener", { postMessage });
        const close = stubClosingWindow();
        nameWindowAsSignInPopup();
        setHash("access_token=abc123");
        vi.stubGlobal("localStorage", {
            getItem: vi.fn(() => null),
            setItem: vi.fn(() => {
                throw new DOMException("storage disabled", "SecurityError");
            }),
            removeItem: vi.fn(),
            clear: vi.fn(),
        });

        render(<AuthCallback />);

        expect(postMessage).toHaveBeenCalledWith(expect.objectContaining({ type: "sam:auth-complete", accessToken: "abc123" }), window.location.origin);
        expect(close).toHaveBeenCalled();
        expect(screen.queryByText("Sign-in failed")).not.toBeInTheDocument();
    });

    it("hands the session to Teams when Teams hosted this window", async () => {
        vi.stubGlobal("opener", { postMessage: vi.fn() });
        completeTeamsSignIn.mockResolvedValue(true);
        localStorage.setItem(POST_LOGIN_REDIRECT_KEY, `${window.location.origin}/chat?agent=demo`);
        setHash("access_token=abc123&sam_access_token=sam1&refresh_token=r1");

        render(<AuthCallback />);
        await settle();

        expect(completeTeamsSignIn).toHaveBeenCalledWith({
            accessToken: "abc123",
            samAccessToken: "sam1",
            refreshToken: "r1",
        });
        expect(localStorage.getItem("access_token")).toBeNull();
        expect(localStorage.getItem(POST_LOGIN_REDIRECT_KEY)).toBe(`${window.location.origin}/chat?agent=demo`);
        expect(window.location.pathname).toBe("/auth-callback");
    });

    it("completes in this window when Teams declines the session", async () => {
        vi.stubGlobal("opener", { postMessage: vi.fn() });
        completeTeamsSignIn.mockResolvedValue(false);
        setHash("access_token=abc123&sam_access_token=sam1&refresh_token=r1");

        render(<AuthCallback />);
        await settle();

        expect(completeTeamsSignIn).toHaveBeenCalled();
        expect(localStorage.getItem("access_token")).toBe("abc123");
        expect(screen.queryByText("Sign-in failed")).not.toBeInTheDocument();
    });

    it("hands the session to Teams from a frameless Teams client window", async () => {
        vi.stubGlobal("opener", null);
        vi.stubGlobal("nativeInterface", {});
        completeTeamsSignIn.mockResolvedValue(true);
        setHash("access_token=abc123");

        render(<AuthCallback />);
        await settle();

        expect(completeTeamsSignIn).toHaveBeenCalledWith(expect.objectContaining({ accessToken: "abc123" }));
        expect(localStorage.getItem("access_token")).toBeNull();
        expect(window.location.pathname).toBe("/auth-callback");
    });

    it("hands the session to Teams when it embeds this window rather than opening it", async () => {
        vi.stubGlobal("opener", null);
        vi.stubGlobal("top", {});
        completeTeamsSignIn.mockResolvedValue(true);
        setHash("access_token=abc123");

        render(<AuthCallback />);
        await settle();

        expect(completeTeamsSignIn).toHaveBeenCalledWith(expect.objectContaining({ accessToken: "abc123" }));
        expect(localStorage.getItem("access_token")).toBeNull();
    });

    it("leaves a handed-off window on a closable end state, tokens unpersisted", async () => {
        vi.stubGlobal("opener", { postMessage: vi.fn() });
        completeTeamsSignIn.mockResolvedValue(true);
        setHash("access_token=abc123&sam_access_token=sam1");

        render(<AuthCallback />);
        await settle();

        expect(screen.getByText("You can close this window.")).toBeInTheDocument();
        expect(screen.queryByText(/Signing you in/i)).not.toBeInTheDocument();
        expect(localStorage.getItem("access_token")).toBeNull();
        expect(localStorage.getItem("sam_access_token")).toBeNull();
    });

    it("tells Teams the sign-in failed instead of leaving the tab waiting", async () => {
        vi.stubGlobal("opener", { postMessage: vi.fn() });
        setHash("error=access_denied&error_description=User%20declined%20access");

        render(<AuthCallback />);
        await settle();

        expect(failTeamsSignIn).toHaveBeenCalledWith("idp_error");
        expect(failTeamsSignIn).not.toHaveBeenCalledWith(expect.stringContaining("User declined access"));
        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
    });

    it("tells Teams the callback returned no token at all", async () => {
        vi.stubGlobal("opener", { postMessage: vi.fn() });
        setHash("token_type=Bearer&expires_in=3600");

        render(<AuthCallback />);
        await settle();

        expect(failTeamsSignIn).toHaveBeenCalledWith("no_access_token");
        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
    });

    it("does not reach for Teams when an ordinary top-level sign-in fails", async () => {
        vi.stubGlobal("opener", null);
        setHash("error=access_denied");

        render(<AuthCallback />);
        await settle();

        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
        expect(failTeamsSignIn).not.toHaveBeenCalled();
    });

    it("keeps Teams waiting while a transient failure is being retried", async () => {
        vi.stubGlobal("opener", { postMessage: vi.fn() });
        setHash("error=service_unavailable&retryable=1");

        render(<AuthCallback />);
        await settle();

        expect(screen.getByText("Finishing sign-in…")).toBeInTheDocument();
        expect(failTeamsSignIn).not.toHaveBeenCalled();
    });

    it("never waits on Teams for an ordinary top-level sign-in (no opener, not framed)", () => {
        vi.stubGlobal("opener", null);
        setHash("access_token=abc123");

        render(<AuthCallback />);

        expect(completeTeamsSignIn).not.toHaveBeenCalled();
        expect(localStorage.getItem("access_token")).toBe("abc123");
    });

    it("completes in this window when it is not a popup", () => {
        vi.stubGlobal("opener", null);
        setHash("access_token=abc123");

        render(<AuthCallback />);

        expect(localStorage.getItem("access_token")).toBe("abc123");
        expect(signInJustCompleted()).toBe(true);
    });

    it("clears a stale sam_access_token when the new session arrives without one", () => {
        vi.stubGlobal("opener", null);
        localStorage.setItem("sam_access_token", "stale-sam");
        setHash("access_token=fresh-access&refresh_token=r1");

        render(<AuthCallback />);

        expect(localStorage.getItem("sam_access_token")).toBeNull();
        expect(localStorage.getItem("access_token")).toBe("fresh-access");
    });

    it("shows an error (not a hang) when token storage is unavailable", () => {
        setHash("access_token=abc123");
        vi.stubGlobal("localStorage", {
            getItem: vi.fn(() => null),
            setItem: vi.fn(() => {
                throw new DOMException("storage disabled", "SecurityError");
            }),
            removeItem: vi.fn(),
            clear: vi.fn(),
        });

        render(<AuthCallback />);

        expect(screen.getByText("Sign-in failed")).toBeInTheDocument();
        expect(screen.getByText(/private browsing|site storage/i)).toBeInTheDocument();
    });
});
