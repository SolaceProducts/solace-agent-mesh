// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useContext } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { AuthContext } from "@/providers/contexts/AuthContext";
import { CsrfContext, type CsrfContextValue } from "@/providers/contexts/CsrfContext";

import { FRAMED_LOGIN_BLOCKED_EVENT } from "@/utils/loginRedirect";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";

import { AuthProvider } from "./AuthProvider";

const { initialize, authenticate } = vi.hoisted(() => ({
    initialize: vi.fn<() => Promise<void>>(),
    authenticate: vi.fn<(options: unknown) => Promise<string>>(),
}));
vi.mock("@microsoft/teams-js", () => ({
    app: {
        get initialize() {
            return initialize;
        },
    },
    authentication: {
        get authenticate() {
            return authenticate;
        },
        notifySuccess: () => {},
        notifyFailure: () => {},
    },
}));

function ShellLoginButton() {
    const ctx = useContext(AuthContext);
    return (
        <button data-testid="shell-login" onClick={() => void ctx?.login()}>
            Login
        </button>
    );
}

const ORIGIN = "http://localhost:3000";
const LOGIN_URL = `${ORIGIN}/api/v1/auth/login`;

const server = setupServer();

const csrfValue: CsrfContextValue = {
    fetchCsrfToken: vi.fn().mockResolvedValue("test-csrf"),
    clearCsrfToken: vi.fn(),
};

function TestWrapper({ children }: { children: ReactNode }) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return (
        <QueryClientProvider client={client}>
            <MockConfigProvider mockValues={{ configUseAuthorization: true, configAuthLoginUrl: LOGIN_URL, webuiServerUrl: ORIGIN }}>
                <CsrfContext.Provider value={csrfValue}>
                    <AuthProvider>{children}</AuthProvider>
                </CsrfContext.Provider>
            </MockConfigProvider>
        </QueryClientProvider>
    );
}

const asFramed = () => {
    const originalTop = window.top;
    const fakeTop = {
        location: {
            get origin(): string {
                throw new Error("cross-origin: cannot read top.location.origin");
            },
            set href(_v: string) {},
        },
    };
    Object.defineProperty(window, "top", { value: fakeTop, writable: true, configurable: true });
    return () => Object.defineProperty(window, "top", { value: originalTop, writable: true, configurable: true });
};

const withMockedLocation = () => {
    const original = window.location;
    const reload = vi.fn();
    Object.defineProperty(window, "location", { value: { ...original, origin: ORIGIN, href: `${ORIGIN}/`, reload }, writable: true, configurable: true });
    return { reload, restore: () => Object.defineProperty(window, "location", { value: original, writable: true, configurable: true }) };
};

const renderSignedOut = async () => {
    server.use(http.get(`${ORIGIN}/api/v1/user`, () => new HttpResponse(null, { status: 401 })));
    render(
        <TestWrapper>
            <div>app</div>
        </TestWrapper>
    );
    await screen.findByText("Welcome to Solace Agent Mesh");
};

const clickSignIn = () => userEvent.setup().click(screen.getByRole("button", { name: /^(sign in|login)$/i }));

let restoreTop: () => void;
let location: ReturnType<typeof withMockedLocation>;
let openSpy: ReturnType<typeof vi.spyOn>;

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());

beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    initialize.mockResolvedValue(undefined);
    authenticate.mockReset();
    restoreTop = asFramed();
    location = withMockedLocation();
    openSpy = vi.spyOn(window, "open").mockReturnValue(null);
});

afterEach(() => {
    openSpy.mockRestore();
    location.restore();
    restoreTop();
    server.resetHandlers();
    localStorage.clear();
    sessionStorage.clear();
});

describe("sign-in inside a Teams tab", () => {
    test("takes the session from Teams rather than opening a popup", async () => {
        authenticate.mockResolvedValue(JSON.stringify({ accessToken: "teams-access", samAccessToken: "teams-sam", refreshToken: "teams-refresh" }));

        await renderSignedOut();
        await clickSignIn();

        await waitFor(() => expect(localStorage.getItem("access_token")).toBe("teams-access"));
        expect(localStorage.getItem("sam_access_token")).toBe("teams-sam");
        expect(localStorage.getItem("refresh_token")).toBe("teams-refresh");
        expect(openSpy).not.toHaveBeenCalled();
        expect(authenticate).toHaveBeenCalledWith(expect.objectContaining({ url: expect.stringContaining("/api/v1/auth/login") }));
        expect(location.reload).toHaveBeenCalled();
    });

    test("spends the account-chooser hint on a Teams sign-in", async () => {
        authenticate.mockResolvedValue(JSON.stringify({ accessToken: "teams-access" }));
        sessionStorage.setItem("sam_force_account_selection", "true");

        await renderSignedOut();
        await clickSignIn();

        await waitFor(() => expect(localStorage.getItem("access_token")).toBe("teams-access"));
        expect(sessionStorage.getItem("sam_force_account_selection")).toBeNull();
        expect(authenticate).toHaveBeenCalledWith(expect.objectContaining({ url: expect.stringContaining("prompt=select_account") }));
    });

    test("does not fall back to a popup when Teams hands back no session", async () => {
        authenticate.mockRejectedValue(new Error("CancelledByUser"));
        sessionStorage.setItem("sam_force_account_selection", "true");
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        try {
            await renderSignedOut();
            await clickSignIn();

            await waitFor(() => expect(authenticate).toHaveBeenCalled());
            expect(openSpy).not.toHaveBeenCalled();
            expect(localStorage.getItem("access_token")).toBeNull();
            expect(sessionStorage.getItem("sam_force_account_selection")).toBe("true");
        } finally {
            warn.mockRestore();
        }
    });

    test("says the sign-in did not finish when Teams hands back no session", async () => {
        authenticate.mockRejectedValue(new Error("CancelledByUser"));
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        try {
            await renderSignedOut();
            await clickSignIn();

            expect(await screen.findByText("Sign-in did not finish. Select Sign in to try again.")).toBeInTheDocument();
        } finally {
            warn.mockRestore();
        }
    });

    test("ignores a second call from a caller with nothing to disable", async () => {
        let settleFirst: (payload: string) => void = () => {};
        authenticate.mockReturnValueOnce(new Promise<string>(resolve => (settleFirst = resolve)));
        server.use(http.get(`${ORIGIN}/api/v1/user`, () => HttpResponse.json({ username: "u", email: "u@example.com" })));

        render(
            <TestWrapper>
                <ShellLoginButton />
            </TestWrapper>
        );
        const button = await screen.findByTestId("shell-login");

        const user = userEvent.setup();
        await user.click(button);
        await waitFor(() => expect(authenticate).toHaveBeenCalledTimes(1));

        await user.click(button);

        settleFirst(JSON.stringify({ accessToken: "teams-access" }));
        await waitFor(() => expect(location.reload).toHaveBeenCalled());

        expect(authenticate).toHaveBeenCalledTimes(1);
    });

    test("shows the sign-in as in progress while Teams holds its window open", async () => {
        let settleFirst: (payload: string) => void = () => {};
        authenticate.mockReturnValueOnce(new Promise<string>(resolve => (settleFirst = resolve)));

        await renderSignedOut();
        await clickSignIn();

        const busy = await screen.findByRole("button", { name: "Signing in…" });
        expect(busy).toBeDisabled();

        settleFirst(JSON.stringify({ accessToken: "teams-access" }));
        await waitFor(() => expect(localStorage.getItem("access_token")).toBe("teams-access"));
    });

    test("lets the user try again after Teams declines", async () => {
        authenticate.mockRejectedValue(new Error("CancelledByUser"));
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        try {
            await renderSignedOut();
            const user = userEvent.setup();
            const button = screen.getByRole("button", { name: /^(sign in|login)$/i });

            await user.click(button);
            await waitFor(() => expect(authenticate).toHaveBeenCalledTimes(1));

            await user.click(screen.getByRole("button", { name: /^(sign in|login)$/i }));

            await waitFor(() => expect(authenticate).toHaveBeenCalledTimes(2));
        } finally {
            warn.mockRestore();
        }
    });

    test("prefers the Teams outcome over a generic blocked-login signal", async () => {
        authenticate.mockRejectedValue(new Error("CancelledByUser"));
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        try {
            await renderSignedOut();
            await clickSignIn();
            await waitFor(() => expect(authenticate).toHaveBeenCalled());

            act(() => {
                globalThis.dispatchEvent(new CustomEvent(FRAMED_LOGIN_BLOCKED_EVENT, { detail: { reason: "no-gesture" } }));
            });

            expect(await screen.findByText("Sign-in did not finish. Select Sign in to try again.")).toBeInTheDocument();
            expect(screen.queryByText("Your session has ended. Select Sign in to continue.")).not.toBeInTheDocument();
        } finally {
            warn.mockRestore();
        }
    });

    test("gives the control back if the Teams window never answers", async () => {
        authenticate.mockReturnValue(new Promise(() => {}));

        await renderSignedOut();

        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
            await user.click(screen.getByRole("button", { name: /^(sign in|login)$/i }));
            expect(await screen.findByRole("button", { name: "Signing in…" })).toBeDisabled();

            await act(async () => {
                await vi.advanceTimersByTimeAsync(5 * 60_000 + 1_000);
            });

            expect(screen.getByRole("button", { name: /^(sign in|login)$/i })).toBeEnabled();
        } finally {
            vi.useRealTimers();
        }
    });

    test("lets a superseded attempt settle without disturbing the live one", async () => {
        let failFirst: (reason: Error) => void = () => {};
        authenticate.mockReturnValueOnce(new Promise<string>((_, reject) => (failFirst = reject)));
        authenticate.mockReturnValue(new Promise(() => {}));
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        await renderSignedOut();
        vi.useFakeTimers({ shouldAdvanceTime: true });
        try {
            const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
            await user.click(screen.getByRole("button", { name: /^(sign in|login)$/i }));
            expect(await screen.findByRole("button", { name: "Signing in…" })).toBeDisabled();

            await act(async () => {
                await vi.advanceTimersByTimeAsync(5 * 60_000 + 1_000);
            });
            await user.click(screen.getByRole("button", { name: /^(sign in|login)$/i }));
            expect(authenticate).toHaveBeenCalledTimes(2);
            expect(await screen.findByRole("button", { name: "Signing in…" })).toBeDisabled();

            await act(async () => {
                failFirst(new Error("CancelledByUser"));
                await Promise.resolve();
            });

            expect(screen.getByRole("button", { name: "Signing in…" })).toBeDisabled();
            expect(screen.queryByText("Sign-in did not finish. Select Sign in to try again.")).not.toBeInTheDocument();
        } finally {
            vi.useRealTimers();
            warn.mockRestore();
        }
    });

    test("drops the previous outcome when the user tries again", async () => {
        authenticate.mockRejectedValueOnce(new Error("CancelledByUser"));
        authenticate.mockReturnValue(new Promise(() => {}));
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});

        try {
            await renderSignedOut();
            const user = userEvent.setup();
            await user.click(screen.getByRole("button", { name: /^(sign in|login)$/i }));
            expect(await screen.findByText("Sign-in did not finish. Select Sign in to try again.")).toBeInTheDocument();

            await user.click(screen.getByRole("button", { name: /^(sign in|login)$/i }));

            await waitFor(() => expect(screen.queryByText("Sign-in did not finish. Select Sign in to try again.")).not.toBeInTheDocument());
        } finally {
            warn.mockRestore();
        }
    });

    test("does not blame pop-ups inside a Teams tab", async () => {
        await renderSignedOut();

        act(() => {
            globalThis.dispatchEvent(new CustomEvent(FRAMED_LOGIN_BLOCKED_EVENT, { detail: { reason: "popup-refused" } }));
        });

        expect(screen.queryByText(/Allow pop-ups for this site/)).not.toBeInTheDocument();
        expect(await screen.findByText("Your session has ended. Select Sign in to continue.")).toBeInTheDocument();
    });

    test("refuses a session Teams hands back without an access token", async () => {
        authenticate.mockResolvedValue(JSON.stringify({ samAccessToken: "teams-sam" }));

        await renderSignedOut();
        await clickSignIn();

        await waitFor(() => expect(authenticate).toHaveBeenCalled());
        expect(localStorage.getItem("access_token")).toBeNull();
    });
});
