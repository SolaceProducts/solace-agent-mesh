// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { CsrfContext, type CsrfContextValue } from "@/providers/contexts/CsrfContext";

import { LOGIN_POPUP_NAME } from "@/utils/loginRedirect";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";

import { AuthProvider } from "./AuthProvider";

const { initialize, authenticate } = vi.hoisted(() => ({
    initialize: vi.fn<() => Promise<void>>(() => new Promise(() => {})),
    authenticate: vi.fn<(options: unknown) => Promise<string>>(() => new Promise(() => {})),
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
    Object.defineProperty(window, "location", { value: { ...original, origin: ORIGIN, href: `${ORIGIN}/`, reload: vi.fn() }, writable: true, configurable: true });
    return () => Object.defineProperty(window, "location", { value: original, writable: true, configurable: true });
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

const asFramelessNativeWindow = () => {
    Object.defineProperty(window, "nativeInterface", { value: {}, writable: true, configurable: true });
    return () => {
        delete (window as unknown as Record<string, unknown>).nativeInterface;
    };
};

let restoreLocation: () => void;
let openSpy: ReturnType<typeof vi.spyOn>;

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());

beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    authenticate.mockClear();
    restoreLocation = withMockedLocation();
    openSpy = vi.spyOn(window, "open").mockReturnValue({ focus: vi.fn() } as unknown as Window);
});

afterEach(() => {
    openSpy.mockRestore();
    restoreLocation();
    server.resetHandlers();
    localStorage.clear();
    sessionStorage.clear();
});

describe("sign-in from an embed that is not Teams", () => {
    test("never asks the host on an unframed page", async () => {
        await renderSignedOut();

        expect(initialize).toHaveBeenCalledTimes(0);
    });

    test("never asks the host in a window the app itself opened", async () => {
        const originalOpener = globalThis.opener;
        Object.defineProperty(globalThis, "opener", { value: {}, writable: true, configurable: true });
        try {
            await renderSignedOut();

            expect(initialize).toHaveBeenCalledTimes(0);
        } finally {
            Object.defineProperty(globalThis, "opener", { value: originalOpener, writable: true, configurable: true });
        }
    });

    test("asks the host in a frameless native window", async () => {
        const restoreNative = asFramelessNativeWindow();
        try {
            await renderSignedOut();

            expect(initialize).toHaveBeenCalledTimes(1);
        } finally {
            restoreNative();
        }
    });

    test("opens the popup without waiting on the host", async () => {
        const restoreTop = asFramed();
        try {
            await renderSignedOut();
            await clickSignIn();

            expect(openSpy).toHaveBeenCalledWith(LOGIN_URL, LOGIN_POPUP_NAME, expect.stringContaining("popup=yes"));
            expect(authenticate).not.toHaveBeenCalled();
        } finally {
            restoreTop();
        }
    });

    test("opens the popup in the click's own task", async () => {
        const restoreTop = asFramed();
        try {
            await renderSignedOut();

            fireEvent.click(screen.getByRole("button", { name: /^(sign in|login)$/i }));
            expect(openSpy).toHaveBeenCalled();
        } finally {
            restoreTop();
        }
    });
});
