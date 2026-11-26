// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { markSignInCompleted, signInJustCompleted } from "@/utils/url";

import { ModelSetupRequestContext } from "@/features/activation";
import type { FreeCreditsSummary } from "@/features/activation/freeCredits";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { activeNavItemId } from "./activeNavItem";
import AppLayout from "./AppLayout";
import { pendingSignInSource } from "./signInSource";

const mockModelConfigStatus = vi.fn<() => { data: { configured: boolean } | undefined }>();
const mockFreeCredits = vi.fn<() => FreeCreditsSummary | null>(() => null);
const twentyPercentUsed: FreeCreditsSummary = { used: 20, tone: "ok", label: "20% of free credits used" };

vi.mock("@/features/activation/freeCredits", async importOriginal => ({
    ...(await importOriginal<typeof import("@/features/activation/freeCredits")>()),
    useFreeCreditsSummary: () => mockFreeCredits(),
}));

vi.mock("@/providers/ChatProvider", () => ({
    ChatProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("@/api/models/hooks", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/models/hooks")>()),
    useModelConfigStatus: () => mockModelConfigStatus(),
    useModelConfigs: () => ({ data: [], isLoading: false, isError: false }),
    useSupportedModels: () => ({ data: [], isLoading: false, isError: false, isSuccess: true, refetch: vi.fn() }),
    useCreateModel: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useUpdateModel: () => ({ mutateAsync: vi.fn(), isPending: false }),
    useTestModelConnection: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/hooks/useLocalStorage", () => ({
    useLocalStorage: (_key: string, initial: unknown) => [initial, vi.fn()],
}));
vi.mock("@/hooks/useNotificationSSE", () => ({
    useNotificationSSE: () => {},
}));
vi.mock("@/hooks/useDesktopFileDialogErrors", () => ({
    useDesktopFileDialogErrors: () => {},
}));

describe("AppLayout model warning banner", () => {
    test("does not show warning when models are configured", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        renderWithProviders(<AppLayout />);
        expect(screen.queryByText(/No default model has been set up/)).not.toBeInTheDocument();
    });

    test("does not show warning when status is still loading", async () => {
        mockModelConfigStatus.mockReturnValue({ data: undefined });
        renderWithProviders(<AppLayout />);
        expect(screen.queryByText(/No default model has been set up/)).not.toBeInTheDocument();
    });

    test("shows warning when models not configured", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        renderWithProviders(<AppLayout />);
        expect(screen.getByText(/No default model has been set up/)).toBeInTheDocument();
    });

    test("shows Set Up Default Models button when user has write permission", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        renderWithProviders(<AppLayout />, {
            chatContextValues: { hasModelConfigWrite: true },
        });
        await waitFor(() => {
            const buttons = screen.getAllByRole("button", { name: /Set Up Default Models/i });
            expect(buttons.length).toBeGreaterThanOrEqual(1);
        });
        expect(screen.queryByText(/Ask your administrator/)).not.toBeInTheDocument();
    });

    test("shows contact admin text when user lacks write permission", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        renderWithProviders(<AppLayout />, {
            userContextValues: { userHasCapability: () => false },
        });
        expect(screen.getByText(/Ask your administrator/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /Set Up Default Models/i })).not.toBeInTheDocument();
    });
});

describe("AppLayout ModelSetupDialog auto-open", () => {
    test("opens ModelSetupDialog when model is not configured", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        renderWithProviders(<AppLayout />);
        await waitFor(() => {
            expect(screen.getByText("Configure Your AI Models")).toBeInTheDocument();
        });
    });

    test("keeps ModelSetupDialog closed when model is already configured", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        renderWithProviders(<AppLayout />);
        expect(screen.queryByText("Configure Your AI Models")).not.toBeInTheDocument();
    });
});

const freeDesktop = { activationGateEnabled: true };

describe("AppLayout first-run model setup in the free desktop", () => {
    test("opens when the person chose to add their own model, then closes", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        const clear = vi.fn();
        const appShown = vi.fn();
        renderWithProviders(
            <ModelSetupRequestContext value={{ requested: true, shown: vi.fn(), appShown, clear }}>
                <AppLayout />
            </ModelSetupRequestContext>,
            { configContextValues: freeDesktop }
        );

        expect(await screen.findByRole("heading", { name: "Welcome to Solace Agent Mesh" })).toBeInTheDocument();
        expect(appShown).toHaveBeenCalled();
        fireEvent.click(screen.getByRole("button", { name: "Close" }));

        expect(clear).toHaveBeenCalledTimes(1);
    });

    test("stays closed without that request, even with no models set up", async () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: false } });
        mockFreeCredits.mockReturnValue(twentyPercentUsed);
        renderWithProviders(<AppLayout />, { configContextValues: freeDesktop });

        expect(await screen.findByText("20% of free credits used")).toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: "Welcome to Solace Agent Mesh" })).not.toBeInTheDocument();
        expect(screen.queryByText("Configure Your AI Models")).not.toBeInTheDocument();
    });
});

describe("AppLayout free credits", () => {
    test("shows the credits used in the free desktop's navigation", () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        mockFreeCredits.mockReturnValue(twentyPercentUsed);
        renderWithProviders(<AppLayout />, { configContextValues: freeDesktop });

        expect(screen.getByRole("link", { name: "20% of free credits used" })).toBeInTheDocument();
    });

    test("does not show in a build without the activation gate", () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        mockFreeCredits.mockReturnValue(twentyPercentUsed);
        renderWithProviders(<AppLayout />);

        expect(screen.queryByText("20% of free credits used")).not.toBeInTheDocument();
    });
});

describe("AppLayout — navigation sidebar by UI mode (characterization)", () => {
    test("full UI renders the navigation sidebar", () => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        const { container } = renderWithProviders(<AppLayout />);
        expect(container.querySelector(".navigation-sidebar")).toBeInTheDocument();
    });
});

describe("AppLayout — browser tab title wiring", () => {
    beforeEach(() => {
        document.title = "Solace Agent Mesh";
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
    });

    test("sets the tab title from the real config context on an unmatched route", async () => {
        renderWithProviders(<AppLayout />, {
            configContextValues: { configAppName: "Acme Assistant" },
            initialPath: "/chat",
        });

        await waitFor(() => {
            expect(document.title).toBe("Acme Assistant");
        });
    });
});

describe("AppLayout — sign-in handoff for a chat app", () => {
    const signedIn = { useAuthorization: true, isAuthenticated: true };

    beforeEach(() => {
        mockModelConfigStatus.mockReturnValue({ data: { configured: true } });
        globalThis.history.replaceState(null, "", "/?signInFrom=whatsapp");
    });

    afterEach(() => {
        globalThis.history.replaceState(null, "", "/");
        sessionStorage.clear();
        vi.restoreAllMocks();
    });

    test("confirms a sign-in this tab just completed, settles the flag, then shows the app on Continue", () => {
        markSignInCompleted();
        vi.spyOn(globalThis.history, "length", "get").mockReturnValue(3);
        const { container } = renderWithProviders(<AppLayout />, { authContextValues: signedIn });

        expect(screen.getByText("You're signed in")).toBeInTheDocument();
        expect(container.querySelector(".navigation-sidebar")).not.toBeInTheDocument();
        expect(globalThis.location.search).toBe("");
        expect(signInJustCompleted()).toBe(false);

        fireEvent.click(screen.getByRole("button", { name: "Continue to Agent Mesh" }));
        expect(container.querySelector(".navigation-sidebar")).toBeInTheDocument();
    });

    test("checks the account of a tab that was already signed in, and switching remembers the source across the sign-out", () => {
        const logout = vi.fn();
        renderWithProviders(<AppLayout />, {
            authContextValues: { ...signedIn, logout, userInfo: { email: "ada@example.com" } },
        });

        expect(screen.getByText("You're already signed in")).toBeInTheDocument();
        expect(screen.getByText(/You're signed in as ada@example\.com\./)).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Use a different account" }));
        expect(logout).toHaveBeenCalledTimes(1);
        expect(pendingSignInSource("")).toEqual({ key: "whatsapp", label: "WhatsApp" });
    });

    test("names the account only by email, never by an opaque username", () => {
        renderWithProviders(<AppLayout />, {
            authContextValues: { ...signedIn, userInfo: { username: "00u1ab2cd3" } },
        });
        expect(screen.getByText("You're already signed in")).toBeInTheDocument();
        expect(screen.queryByText(/signed in as/)).not.toBeInTheDocument();
    });

    test("opens the app when sign-in is not enabled", () => {
        const { container } = renderWithProviders(<AppLayout />, {
            authContextValues: { useAuthorization: false, isAuthenticated: false },
        });
        expect(screen.queryByText(/You're (already )?signed in/)).not.toBeInTheDocument();
        expect(container.querySelector(".navigation-sidebar")).toBeInTheDocument();
    });

    test("never shows the handoff to a tab that is not signed in", () => {
        markSignInCompleted();
        renderWithProviders(<AppLayout />, {
            authContextValues: { useAuthorization: true, isAuthenticated: false },
        });
        expect(screen.queryByText(/You're (already )?signed in/)).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Login" })).toBeInTheDocument();
    });
});

describe("activeNavItemId", () => {
    test.each([
        ["/feedback", "feedback"],
        ["/feedback/history", "feedback"],
        ["/evaluations/lab", "eval-lab"],
        ["/evaluations", "eval-reports"],
        ["/agents", "agents"],
        ["/agent-mesh", "agentMesh"],
        ["/chat", "chat"],
        ["/", "chat"],
    ])("resolves %s to %s", (path, expected) => {
        expect(activeNavItemId(path)).toBe(expected);
    });

    test("does not fall back to chat on a feedback route", () => {
        expect(activeNavItemId("/feedback")).not.toBe("chat");
    });
});
