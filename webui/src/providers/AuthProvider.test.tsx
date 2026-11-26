// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useContext } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import * as apiClientModule from "@/api/client";
import { sessionKeys } from "@/api/sessions/keys";
import { shareKeys } from "@/api/share/keys";

import { AuthContext } from "@/providers/contexts/AuthContext";
import { CsrfContext, type CsrfContextValue } from "@/providers/contexts/CsrfContext";

import { AUTH_COMPLETE_MESSAGE, FRAMED_LOGIN_BLOCKED_EVENT, type FramedLoginBlockedDetail, LOGIN_POPUP_NAME } from "@/utils/loginRedirect";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";

import { AuthProvider } from "./AuthProvider";

vi.mock("@microsoft/teams-js", () => ({
    app: { initialize: () => new Promise(() => {}) },
    authentication: { authenticate: () => new Promise(() => {}), notifySuccess: () => {}, notifyFailure: () => {} },
}));

function makeJwt(exp: number): string {
    const header = btoa(JSON.stringify({ alg: "HS256", typ: "JWT" }));
    const payload = btoa(JSON.stringify({ exp, sub: "test-user", email: "test@example.com" }));
    return `${header}.${payload}.fake-signature`;
}

const server = setupServer();

const mockCsrfValue: CsrfContextValue = {
    fetchCsrfToken: vi.fn().mockResolvedValue("test-csrf"),
    clearCsrfToken: vi.fn(),
};

function MockCsrfProvider({ children }: { children: React.ReactNode }) {
    return <CsrfContext.Provider value={mockCsrfValue}>{children}</CsrfContext.Provider>;
}

function AuthConsumer() {
    const ctx = useContext(AuthContext);
    if (!ctx) return <div>No context</div>;
    return (
        <div>
            <span data-testid="authenticated">{String(ctx.isAuthenticated)}</span>
            <span data-testid="useAuth">{String(ctx.useAuthorization)}</span>
            <button data-testid="context-login-btn" onClick={ctx.login}>
                Context login
            </button>
            <button data-testid="logout-btn" onClick={ctx.logout}>
                Logout
            </button>
        </div>
    );
}

function TestWrapper({ children, useAuthorization = true, queryClient }: { children: React.ReactNode; useAuthorization?: boolean; queryClient?: QueryClient }) {
    const client = queryClient ?? new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return (
        <QueryClientProvider client={client}>
            <MockConfigProvider
                mockValues={{
                    configUseAuthorization: useAuthorization,
                    configAuthLoginUrl: "http://localhost:3000/api/v1/auth/login",
                    webuiServerUrl: "http://localhost:3000",
                }}
            >
                <MockCsrfProvider>
                    <AuthProvider>{children}</AuthProvider>
                </MockCsrfProvider>
            </MockConfigProvider>
        </QueryClientProvider>
    );
}

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
});

let scheduleSpy: ReturnType<typeof vi.spyOn>;
let cancelSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    (mockCsrfValue.fetchCsrfToken as ReturnType<typeof vi.fn>).mockClear();
    (mockCsrfValue.clearCsrfToken as ReturnType<typeof vi.fn>).mockClear();
    scheduleSpy = vi.spyOn(apiClientModule, "scheduleProactiveRefresh").mockImplementation(() => {});
    cancelSpy = vi.spyOn(apiClientModule, "cancelProactiveRefresh").mockImplementation(() => {});
});

afterEach(() => {
    server.resetHandlers();
    scheduleSpy.mockRestore();
    cancelSpy.mockRestore();
    localStorage.clear();
    sessionStorage.clear();
});

afterAll(() => {
    server.close();
});

describe("AuthProvider", () => {
    describe("proactive refresh lifecycle", () => {
        test("calls scheduleProactiveRefresh after successful authentication", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));

            server.use(
                http.get("http://localhost:3000/api/v1/user", () => {
                    return HttpResponse.json({
                        username: "test-user",
                        email: "test@example.com",
                    });
                })
            );

            render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );

            await waitFor(() => {
                expect(screen.getByTestId("authenticated").textContent).toBe("true");
            });

            expect(scheduleSpy).toHaveBeenCalled();
        });

        test("does not call scheduleProactiveRefresh when auth check fails", async () => {
            server.use(
                http.get("http://localhost:3000/api/v1/user", () => {
                    return new HttpResponse(null, { status: 401 });
                })
            );

            render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );

            await waitFor(() => {
                expect(screen.getByText("Welcome to Solace Agent Mesh")).toBeInTheDocument();
            });

            expect(scheduleSpy).not.toHaveBeenCalled();
        });

        test("calls cancelProactiveRefresh on logout", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));

            server.use(
                http.get("http://localhost:3000/api/v1/user", () => {
                    return HttpResponse.json({
                        username: "test-user",
                        email: "test@example.com",
                    });
                }),
                http.post("http://localhost:3000/api/v1/auth/logout", () => {
                    return HttpResponse.json({ success: true });
                })
            );

            const originalLocation = window.location;
            Object.defineProperty(window, "location", {
                value: { ...originalLocation, href: "" },
                writable: true,
                configurable: true,
            });

            render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );

            await waitFor(() => {
                expect(screen.getByTestId("authenticated").textContent).toBe("true");
            });

            cancelSpy.mockClear();

            const user = userEvent.setup();
            await act(async () => {
                await user.click(screen.getByTestId("logout-btn"));
            });

            expect(cancelSpy).toHaveBeenCalled();
            expect(localStorage.getItem("access_token")).toBeNull();
            expect(localStorage.getItem("sam_access_token")).toBeNull();
            expect(localStorage.getItem("refresh_token")).toBeNull();
            expect(sessionStorage.getItem("sam_force_account_selection")).toBe("true");

            Object.defineProperty(window, "location", {
                value: originalLocation,
                writable: true,
                configurable: true,
            });
        });

        test("evicts every cached query on logout so no prior-user data can leak", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));

            server.use(
                http.get("http://localhost:3000/api/v1/user", () => {
                    return HttpResponse.json({
                        username: "test-user",
                        email: "test@example.com",
                    });
                }),
                http.post("http://localhost:3000/api/v1/auth/logout", () => {
                    return HttpResponse.json({ success: true });
                })
            );

            const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
            const recentKey = sessionKeys.recent("alice", 10);
            const sharedKey = shareKeys.sharedWithMe("alice");
            queryClient.setQueryData(recentKey, [{ id: "sess-A", name: "User A's chat" }]);
            queryClient.setQueryData(sharedKey, [{ shareId: "share-A" }]);
            queryClient.setQueryData(["unrelated", "key"], { foo: "bar" });

            const originalLocation = window.location;
            Object.defineProperty(window, "location", {
                value: { ...originalLocation, href: "" },
                writable: true,
                configurable: true,
            });

            render(
                <TestWrapper queryClient={queryClient}>
                    <AuthConsumer />
                </TestWrapper>
            );

            await waitFor(() => {
                expect(screen.getByTestId("authenticated").textContent).toBe("true");
            });

            expect(queryClient.getQueryData(recentKey)).toBeDefined();
            expect(queryClient.getQueryData(sharedKey)).toBeDefined();
            expect(queryClient.getQueryData(["unrelated", "key"])).toBeDefined();

            const user = userEvent.setup();
            await act(async () => {
                await user.click(screen.getByTestId("logout-btn"));
            });

            expect(queryClient.getQueryCache().getAll()).toHaveLength(0);
            expect(queryClient.getQueryData(recentKey)).toBeUndefined();
            expect(queryClient.getQueryData(sharedKey)).toBeUndefined();
            expect(queryClient.getQueryData(["unrelated", "key"])).toBeUndefined();

            Object.defineProperty(window, "location", {
                value: originalLocation,
                writable: true,
                configurable: true,
            });
        });

        test("clears refresh_token and sets logout_in_progress before issuing the logout POST", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));
            localStorage.setItem("refresh_token", "user-A-refresh");

            let refreshTokenAtLogoutTime: string | null = "<not-captured>";
            let logoutFlagAtLogoutTime: string | null = "<not-captured>";

            server.use(
                http.get("http://localhost:3000/api/v1/user", () => {
                    return HttpResponse.json({
                        username: "test-user",
                        email: "test@example.com",
                    });
                }),
                http.post("http://localhost:3000/api/v1/auth/logout", () => {
                    refreshTokenAtLogoutTime = localStorage.getItem("refresh_token");
                    logoutFlagAtLogoutTime = sessionStorage.getItem("logout_in_progress");
                    return HttpResponse.json({ success: true });
                })
            );

            const originalLocation = window.location;
            Object.defineProperty(window, "location", {
                value: { ...originalLocation, href: "" },
                writable: true,
                configurable: true,
            });

            try {
                render(
                    <TestWrapper>
                        <AuthConsumer />
                    </TestWrapper>
                );

                await waitFor(() => {
                    expect(screen.getByTestId("authenticated").textContent).toBe("true");
                });

                const user = userEvent.setup();
                await act(async () => {
                    await user.click(screen.getByTestId("logout-btn"));
                });

                expect(refreshTokenAtLogoutTime).toBeNull();
                expect(logoutFlagAtLogoutTime).toBe("true");

                expect(sessionStorage.getItem("logout_in_progress")).toBeNull();
            } finally {
                Object.defineProperty(window, "location", {
                    value: originalLocation,
                    writable: true,
                    configurable: true,
                });
            }
        });

        test("calls cancelProactiveRefresh on unmount", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));

            server.use(
                http.get("http://localhost:3000/api/v1/user", () => {
                    return HttpResponse.json({
                        username: "test-user",
                        email: "test@example.com",
                    });
                })
            );

            const { unmount } = render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );

            await waitFor(() => {
                expect(screen.getByTestId("authenticated").textContent).toBe("true");
            });

            cancelSpy.mockClear();
            unmount();

            expect(cancelSpy).toHaveBeenCalled();
        });
    });

    describe("no-auth mode", () => {
        test("does not call scheduleProactiveRefresh when authorization is disabled", async () => {
            render(
                <TestWrapper useAuthorization={false}>
                    <AuthConsumer />
                </TestWrapper>
            );

            await waitFor(() => {
                expect(screen.getByTestId("authenticated").textContent).toBe("true");
            });

            expect(scheduleSpy).not.toHaveBeenCalled();
        });
    });

    describe("login() exit-to-IdP flow", () => {
        const CURRENT_URL = "http://localhost:3000/#/agent-mode/chat?agent=Foo";
        const LOGIN_URL = "http://localhost:3000/api/v1/auth/login";

        const setFrameElement = (el: HTMLIFrameElement | null) => {
            Object.defineProperty(window, "frameElement", { value: el, writable: true, configurable: true });
        };

        const renderUnauthenticatedLogin = async () => {
            server.use(http.get("http://localhost:3000/api/v1/user", () => new HttpResponse(null, { status: 401 })));
            render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );
            await screen.findByText("Welcome to Solace Agent Mesh");
            await userEvent.setup().click(screen.getByRole("button", { name: "Login" }));
        };

        test("stashes the current URL and navigates the current window to the IdP (not framed)", async () => {
            const originalLocation = window.location;
            Object.defineProperty(window, "location", {
                value: { ...originalLocation, href: CURRENT_URL, origin: "http://localhost:3000" },
                writable: true,
                configurable: true,
            });

            try {
                await renderUnauthenticatedLogin();

                expect(localStorage.getItem("sam_post_login_redirect")).toBe(CURRENT_URL);
                expect(window.location.href).toBe(LOGIN_URL);
            } finally {
                Object.defineProperty(window, "location", { value: originalLocation, writable: true, configurable: true });
            }
        });

        const withStubbedLocation = async (run: () => Promise<void>) => {
            const originalLocation = window.location;
            Object.defineProperty(window, "location", {
                value: { ...originalLocation, href: CURRENT_URL, origin: "http://localhost:3000" },
                writable: true,
                configurable: true,
            });
            try {
                await run();
            } finally {
                Object.defineProperty(window, "location", { value: originalLocation, writable: true, configurable: true });
            }
        };

        test("asks the IdP for an account chooser on the sign-in after a logout", async () => {
            sessionStorage.setItem("sam_force_account_selection", "true");

            await withStubbedLocation(async () => {
                await renderUnauthenticatedLogin();

                expect(window.location.href).toBe(`${LOGIN_URL}?prompt=select_account`);
                expect(sessionStorage.getItem("sam_force_account_selection")).toBeNull();
            });
        });

        test("does not ask for a chooser on an ordinary sign-in", async () => {
            await withStubbedLocation(async () => {
                await renderUnauthenticatedLogin();

                expect(window.location.href).toBe(LOGIN_URL);
            });
        });

        test("keeps the account chooser pending when the popup is blocked", async () => {
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
            setFrameElement(null);
            const openSpy = vi.spyOn(window, "open").mockReturnValue(null);
            sessionStorage.setItem("sam_force_account_selection", "true");

            try {
                await withStubbedLocation(async () => {
                    await renderUnauthenticatedLogin();

                    expect(openSpy).toHaveBeenCalledWith(`${LOGIN_URL}?prompt=select_account`, LOGIN_POPUP_NAME, expect.stringContaining("popup=yes"));
                    expect(sessionStorage.getItem("sam_force_account_selection")).toBe("true");
                });
            } finally {
                openSpy.mockRestore();
                Object.defineProperty(window, "top", { value: originalTop, writable: true, configurable: true });
            }
        });

        test("opens a popup rather than navigating a cross-origin embedder or the frame", async () => {
            const originalLocation = window.location;
            const originalTop = window.top;
            Object.defineProperty(window, "location", {
                value: { ...originalLocation, href: CURRENT_URL, origin: "http://localhost:3000" },
                writable: true,
                configurable: true,
            });
            let topNavAttempted = false;
            const fakeTop = {
                location: {
                    get origin(): string {
                        throw new Error("cross-origin: cannot read top.location.origin");
                    },
                    set href(_v: string) {
                        topNavAttempted = true;
                    },
                },
            };
            Object.defineProperty(window, "top", { value: fakeTop, writable: true, configurable: true });
            setFrameElement(null);
            const openSpy = vi.spyOn(window, "open").mockReturnValue({ focus: vi.fn() } as unknown as Window);

            try {
                await renderUnauthenticatedLogin();

                expect(openSpy).toHaveBeenCalledWith(LOGIN_URL, LOGIN_POPUP_NAME, expect.stringContaining("popup=yes"));
                expect(topNavAttempted).toBe(false);
                expect(window.location.href).toBe(CURRENT_URL);
                expect(localStorage.getItem("sam_post_login_redirect")).toBe(CURRENT_URL);
            } finally {
                openSpy.mockRestore();
                Object.defineProperty(window, "location", { value: originalLocation, writable: true, configurable: true });
                Object.defineProperty(window, "top", { value: originalTop, writable: true, configurable: true });
            }
        });
    });

    describe("embedded-frame sign-in", () => {
        const ORIGIN = "http://localhost:3000";

        const renderUnauthenticated = async () => {
            server.use(http.get(`${ORIGIN}/api/v1/user`, () => new HttpResponse(null, { status: 401 })));
            render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );
            await screen.findByText("Welcome to Solace Agent Mesh");
        };

        const withMockedLocation = () => {
            const original = window.location;
            const reload = vi.fn();
            Object.defineProperty(window, "location", {
                value: { ...original, origin: ORIGIN, href: `${ORIGIN}/`, reload },
                writable: true,
                configurable: true,
            });
            return {
                reload,
                restore: () => Object.defineProperty(window, "location", { value: original, writable: true, configurable: true }),
            };
        };

        interface SignInPopup {
            popup: Window;
            restore: () => void;
        }

        const openSignInPopup = async (trigger: string | RegExp = /^(sign in|login)$/i): Promise<SignInPopup> => {
            const originalTop = window.top;
            const popup = { focus: vi.fn() } as unknown as Window;
            const fakeTop = {
                location: {
                    get origin(): string {
                        throw new Error("cross-origin: cannot read top.location.origin");
                    },
                    set href(_v: string) {},
                },
            };
            Object.defineProperty(window, "top", { value: fakeTop, writable: true, configurable: true });
            const openSpy = vi.spyOn(window, "open").mockReturnValue(popup);

            await userEvent.setup().click(screen.getByRole("button", { name: trigger }));
            expect(openSpy).toHaveBeenCalledWith(`${ORIGIN}/api/v1/auth/login`, LOGIN_POPUP_NAME, expect.stringContaining("popup=yes"));

            return {
                popup,
                restore: () => {
                    openSpy.mockRestore();
                    Object.defineProperty(window, "top", { value: originalTop, writable: true, configurable: true });
                },
            };
        };

        const dispatchAuthComplete = (data: Record<string, unknown>, source?: Window, origin: string = ORIGIN) => {
            const event = new MessageEvent("message", { origin, data });
            if (source) Object.defineProperty(event, "source", { value: source, configurable: true });
            act(() => {
                window.dispatchEvent(event);
            });
        };

        test("adopts the session handed back by the sign-in popup", async () => {
            localStorage.clear();
            const loc = withMockedLocation();
            let signIn: SignInPopup | undefined;
            try {
                await renderUnauthenticated();
                signIn = await openSignInPopup();

                dispatchAuthComplete(
                    {
                        type: AUTH_COMPLETE_MESSAGE,
                        accessToken: "popup-access",
                        samAccessToken: "popup-sam",
                        refreshToken: "popup-refresh",
                    },
                    signIn.popup
                );

                await waitFor(() => expect(localStorage.getItem("access_token")).toBe("popup-access"));
                expect(localStorage.getItem("sam_access_token")).toBe("popup-sam");
                expect(localStorage.getItem("refresh_token")).toBe("popup-refresh");
                expect(loc.reload).toHaveBeenCalled();
            } finally {
                signIn?.restore();
                loc.restore();
                localStorage.clear();
            }
        });

        test("ignores an auth-complete message from a foreign origin", async () => {
            localStorage.clear();
            const loc = withMockedLocation();
            let signIn: SignInPopup | undefined;
            try {
                await renderUnauthenticated();
                signIn = await openSignInPopup();

                dispatchAuthComplete({ type: AUTH_COMPLETE_MESSAGE, accessToken: "injected" }, signIn.popup, "https://attacker.example");

                expect(localStorage.getItem("access_token")).toBeNull();
                expect(loc.reload).not.toHaveBeenCalled();
            } finally {
                signIn?.restore();
                loc.restore();
                localStorage.clear();
            }
        });

        test("ignores an auth-complete message from a same-origin window that is not the sign-in popup", async () => {
            localStorage.clear();
            const loc = withMockedLocation();
            let signIn: SignInPopup | undefined;
            try {
                await renderUnauthenticated();
                signIn = await openSignInPopup();

                dispatchAuthComplete({ type: AUTH_COMPLETE_MESSAGE, accessToken: "injected" }, {} as Window);

                expect(localStorage.getItem("access_token")).toBeNull();
                expect(loc.reload).not.toHaveBeenCalled();
            } finally {
                signIn?.restore();
                loc.restore();
                localStorage.clear();
            }
        });

        test("ignores an auth-complete message when no sign-in popup was ever opened", async () => {
            localStorage.clear();
            const loc = withMockedLocation();
            try {
                await renderUnauthenticated();

                dispatchAuthComplete({ type: AUTH_COMPLETE_MESSAGE, accessToken: "injected" }, {} as Window);
                dispatchAuthComplete({ type: AUTH_COMPLETE_MESSAGE, accessToken: "injected-anonymously" });

                expect(localStorage.getItem("access_token")).toBeNull();
                expect(loc.reload).not.toHaveBeenCalled();
            } finally {
                loc.restore();
                localStorage.clear();
            }
        });

        const dispatchLoginBlocked = (detail?: FramedLoginBlockedDetail) => {
            act(() => {
                window.dispatchEvent(detail ? new CustomEvent<FramedLoginBlockedDetail>(FRAMED_LOGIN_BLOCKED_EVENT, { detail }) : new Event(FRAMED_LOGIN_BLOCKED_EVENT));
            });
        };

        test("surfaces the sign-in control even when the session was already authenticated", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));
            server.use(http.get(`${ORIGIN}/api/v1/user`, () => HttpResponse.json({ username: "test-user" })));

            render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );
            await waitFor(() => expect(screen.getByTestId("authenticated").textContent).toBe("true"));

            dispatchLoginBlocked({ reason: "no-gesture" });

            await screen.findByText(/Your session has ended/i);
            expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
        });

        test("shows the inactivity notice and waits for a click when the idle timeout fires", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));
            server.use(http.get(`${ORIGIN}/api/v1/user`, () => HttpResponse.json({ username: "test-user" })));

            render(
                <TestWrapper>
                    <AuthConsumer />
                </TestWrapper>
            );
            await waitFor(() => expect(screen.getByTestId("authenticated").textContent).toBe("true"));

            act(() => {
                window.dispatchEvent(new Event(apiClientModule.SESSION_IDLE_TIMEOUT_EVENT));
            });

            await screen.findByText(/signed out after a period of inactivity/i);
            expect(screen.queryByTestId("authenticated")).not.toBeInTheDocument();
            expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
        });

        test("keeps the inactivity notice on screen while the click sends the browser to the IdP", async () => {
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));
            server.use(http.get(`${ORIGIN}/api/v1/user`, () => HttpResponse.json({ username: "test-user" })));
            const loc = withMockedLocation();
            try {
                render(
                    <TestWrapper>
                        <AuthConsumer />
                    </TestWrapper>
                );
                await waitFor(() => expect(screen.getByTestId("authenticated").textContent).toBe("true"));
                act(() => {
                    window.dispatchEvent(new Event(apiClientModule.SESSION_IDLE_TIMEOUT_EVENT));
                });
                await screen.findByText(/signed out after a period of inactivity/i);

                await userEvent.setup().click(screen.getByRole("button", { name: "Sign in" }));

                expect(window.location.href).toContain("/api/v1/auth/login");
                expect(screen.getByText(/signed out after a period of inactivity/i)).toBeInTheDocument();
            } finally {
                loc.restore();
                localStorage.clear();
            }
        });

        test("ignores an auth-complete message carrying no access token", async () => {
            localStorage.clear();
            const loc = withMockedLocation();
            let signIn: SignInPopup | undefined;
            try {
                await renderUnauthenticated();
                signIn = await openSignInPopup();

                dispatchAuthComplete({ type: AUTH_COMPLETE_MESSAGE }, signIn.popup);

                expect(localStorage.getItem("access_token")).toBeNull();
                expect(loc.reload).not.toHaveBeenCalled();
            } finally {
                signIn?.restore();
                loc.restore();
                localStorage.clear();
            }
        });

        test("tells the user to allow pop-ups when the pop-up was refused", async () => {
            await renderUnauthenticated();
            expect(screen.getByRole("button", { name: "Login" })).toBeInTheDocument();

            dispatchLoginBlocked({ reason: "popup-refused" });

            await screen.findByText(/Sign-in has to open in a separate window/i);
            expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
        });

        test("does not blame the pop-up blocker for a session that expired inside a Teams tab, where no pop-up was attempted", async () => {
            await renderUnauthenticated();

            dispatchLoginBlocked({ reason: "no-gesture" });

            await screen.findByText(/Your session has ended\. Select Sign in to continue\./i);
            expect(screen.queryByText(/Allow pop-ups for this site/i)).not.toBeInTheDocument();
            expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
        });

        test("falls back to the generic wording when the blocked signal carries no reason", async () => {
            await renderUnauthenticated();

            dispatchLoginBlocked();

            await screen.findByText(/Your session has ended/i);
            expect(screen.queryByText(/Allow pop-ups for this site/i)).not.toBeInTheDocument();
        });

        test("says so when the browser blocks storing the session the popup handed back", async () => {
            localStorage.clear();
            const exp = Math.floor((Date.now() + 3600000) / 1000);
            localStorage.setItem("sam_access_token", makeJwt(exp));
            localStorage.setItem("access_token", makeJwt(exp));
            server.use(http.get(`${ORIGIN}/api/v1/user`, () => HttpResponse.json({ username: "test-user" })));
            const loc = withMockedLocation();
            let signIn: SignInPopup | undefined;
            let setItem: ReturnType<typeof vi.spyOn> | undefined;
            let consoleError: ReturnType<typeof vi.spyOn> | undefined;

            try {
                render(
                    <TestWrapper>
                        <AuthConsumer />
                    </TestWrapper>
                );
                await waitFor(() => expect(screen.getByTestId("authenticated").textContent).toBe("true"));
                signIn = await openSignInPopup("Context login");

                setItem = vi.spyOn(globalThis.localStorage, "setItem").mockImplementation(() => {
                    throw new Error("storage is not available");
                });
                consoleError = vi.spyOn(console, "error").mockImplementation(() => {});

                dispatchAuthComplete({ type: AUTH_COMPLETE_MESSAGE, accessToken: "popup-access" }, signIn.popup);

                await screen.findByText(/Your browser blocked storing the session/i);
                expect(screen.getByRole("button", { name: "Sign in" })).toBeInTheDocument();
                expect(screen.queryByTestId("authenticated")).not.toBeInTheDocument();
                expect(loc.reload).not.toHaveBeenCalled();
            } finally {
                consoleError?.mockRestore();
                setItem?.mockRestore();
                signIn?.restore();
                loc.restore();
                localStorage.clear();
            }
        });

        test("drops the previous attempt's diagnosis when the retry fails for a different reason", async () => {
            localStorage.clear();
            const loc = withMockedLocation();
            const consoleError = vi.spyOn(console, "error").mockImplementation(() => {});
            const consoleWarn = vi.spyOn(console, "warn").mockImplementation(() => {});
            let signIn: SignInPopup | undefined;
            let setItem: ReturnType<typeof vi.spyOn> | undefined;
            let refusedOpen: ReturnType<typeof vi.spyOn> | undefined;

            try {
                await renderUnauthenticated();
                signIn = await openSignInPopup();

                setItem = vi.spyOn(globalThis.localStorage, "setItem").mockImplementation(() => {
                    throw new Error("storage is not available");
                });
                dispatchAuthComplete({ type: AUTH_COMPLETE_MESSAGE, accessToken: "popup-access" }, signIn.popup);
                await screen.findByText(/Your browser blocked storing the session/i);

                setItem.mockRestore();
                refusedOpen = vi.spyOn(window, "open").mockReturnValue(null);

                await userEvent.setup().click(screen.getByRole("button", { name: "Sign in" }));

                await screen.findByText(/Sign-in has to open in a separate window/i);
                expect(screen.queryByText(/Your browser blocked storing the session/i)).not.toBeInTheDocument();
            } finally {
                refusedOpen?.mockRestore();
                setItem?.mockRestore();
                signIn?.restore();
                consoleWarn.mockRestore();
                consoleError.mockRestore();
                loc.restore();
                localStorage.clear();
            }
        });
    });
});
