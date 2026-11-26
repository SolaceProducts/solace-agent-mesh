// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";
import { type ConsentState, productAnalyticsKeys } from "@/api/product-analytics";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts/ConfigContext";

import { PrivacySettings } from "@/features/settings/PrivacySettings";

import { productAnalyticsClient } from "./client";
import { ProductAnalyticsGate, ProductAnalyticsPageViews } from "./ProductAnalyticsGate";

let setFeatureSpy: ReturnType<typeof vi.spyOn>;
let setConsentSpy: ReturnType<typeof vi.spyOn>;
let trackSpy: ReturnType<typeof vi.spyOn>;
let getSpy: ReturnType<typeof vi.spyOn>;
let putSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    setFeatureSpy = vi.spyOn(productAnalyticsClient, "setFeatureEnabled").mockImplementation(() => {});
    setConsentSpy = vi.spyOn(productAnalyticsClient, "setConsent").mockImplementation(() => {});
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    getSpy = vi.spyOn(api.webui, "get");
    putSpy = vi.spyOn(api.webui, "put");
});

afterEach(() => {
    vi.restoreAllMocks();
});

function makeConfigValue(productAnalyticsEnabled: boolean): ConfigContextValue {
    return {
        webuiServerUrl: "",
        platformServerUrl: "",
        configAuthLoginUrl: "",
        configUseAuthorization: false,
        configWelcomeMessage: "",
        configDisclaimerText: "",
        configRedirectUrl: "",
        configCollectFeedback: false,
        configAppName: "",
        configLogoUrl: "",
        configSmallLogoUrl: "",
        frontend_use_authorization: false,
        platformConfigured: false,
        identityServiceType: null,
        claimKey: "",
        refetch: async () => {},
        productAnalyticsEnabled,
    };
}

function renderGate(flagValue: boolean, consentState: ConsentState = "unset") {
    getSpy.mockResolvedValue({ state: consentState });
    return renderGateWithClient(flagValue);
}

function renderGateWithClient(flagValue: boolean, sibling?: ReactNode) {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createMemoryRouter(
        [
            {
                path: "/",
                element: (
                    <QueryClientProvider client={queryClient}>
                        <ProductAnalyticsGate />
                        <ProductAnalyticsPageViews />
                        {sibling}
                    </QueryClientProvider>
                ),
            },
        ],
        { initialEntries: ["/"] }
    );

    render(
        <ConfigContext.Provider value={makeConfigValue(flagValue)}>
            <RouterProvider router={router} />
        </ConfigContext.Provider>
    );
    return queryClient;
}

async function settledToggle() {
    const toggle = await screen.findByRole("switch");
    await waitFor(() => expect(toggle).toBeEnabled());
    return toggle;
}

async function awaitConsentLoaded(client: QueryClient) {
    await waitFor(() => expect(client.getQueryState(productAnalyticsKeys.consent())?.status).toBe("success"));
}

describe("ProductAnalyticsGate", () => {
    test("calls setFeatureEnabled(true) when this build carries product analytics", () => {
        renderGate(true);
        expect(setFeatureSpy).toHaveBeenCalledWith(true);
    });

    test("calls setFeatureEnabled(false) in a build with no collection path", () => {
        renderGate(false);
        expect(setFeatureSpy).toHaveBeenCalledWith(false);
    });

    test("does not read consent when the flag is off", () => {
        renderGate(false);
        expect(getSpy).not.toHaveBeenCalled();
    });

    test("opens the client's consent gate once the backend reports granted", async () => {
        renderGate(true, "granted");
        await waitFor(() => expect(setConsentSpy).toHaveBeenCalledWith(true));
    });

    test("keeps the client's consent gate closed when consent is denied", async () => {
        const queryClient = renderGate(true, "denied");
        await awaitConsentLoaded(queryClient);
        expect(setConsentSpy).not.toHaveBeenCalledWith(true);
    });

    test("opens the client's consent gate while consent is unset", async () => {
        renderGate(true, "unset");
        await waitFor(() => expect(setConsentSpy).toHaveBeenCalledWith(true));
    });

    test("tracks page_view once consent is granted", async () => {
        renderGate(true, "granted");
        await waitFor(() => expect(trackSpy).toHaveBeenCalledWith("page_view", expect.objectContaining({ path: expect.any(String) })));
    });

    test("tracks page_view while consent is unset", async () => {
        renderGate(true, "unset");
        await waitFor(() => expect(trackSpy).toHaveBeenCalledWith("page_view", expect.objectContaining({ path: expect.any(String) })));
    });

    test("does not track page_view once consent is denied", async () => {
        const queryClient = renderGate(true, "denied");
        await awaitConsentLoaded(queryClient);
        expect(trackSpy).not.toHaveBeenCalled();
    });

    test.each([
        ["a response with no state", {}],
        ["a state this build does not know", { state: "bogus" }],
        ["a null state", { state: null }],
    ])("keeps collection off for %s", async (_label, body) => {
        getSpy.mockResolvedValue(body as never);
        const queryClient = renderGateWithClient(true);

        await awaitConsentLoaded(queryClient);

        expect(setConsentSpy).not.toHaveBeenCalledWith(true);
        expect(trackSpy).not.toHaveBeenCalled();
    });

    test.each(["unset", "granted", "denied"] as ConsentState[])("shows no consent prompt when consent is %s", async state => {
        getSpy.mockResolvedValue({ state });
        const queryClient = renderGateWithClient(true);

        await awaitConsentLoaded(queryClient);

        expect(document.body).not.toHaveTextContent(/usage data|analytics/i);
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
        expect(screen.queryByRole("region")).not.toBeInTheDocument();
    });

    test("fails closed when the consent read errors", async () => {
        const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        getSpy.mockRejectedValue(new Error("gateway down"));
        renderGateWithClient(true);

        await waitFor(() => expect(consoleSpy).toHaveBeenCalledWith("Failed to read the usage analytics consent state:", expect.any(Error)));

        expect(setConsentSpy).not.toHaveBeenCalledWith(true);
        expect(trackSpy).not.toHaveBeenCalled();
        expect(screen.queryByRole("region")).not.toBeInTheDocument();
    });

    test("closes the client's consent gate when consent is revoked", async () => {
        const queryClient = renderGate(true, "granted");
        await waitFor(() => expect(setConsentSpy).toHaveBeenCalledWith(true));

        act(() => {
            queryClient.setQueryData(productAnalyticsKeys.consent(), { state: "denied" });
        });

        await waitFor(() => expect(setConsentSpy).toHaveBeenCalledWith(false));
    });

    test("applies a decision through the real mutation even when the refetch fails", async () => {
        getSpy.mockResolvedValueOnce({ state: "denied" }).mockRejectedValue(new Error("gateway down"));
        putSpy.mockResolvedValue({ state: "granted" });
        renderGateWithClient(true, <PrivacySettings />);

        const toggle = await settledToggle();
        expect(toggle).not.toBeChecked();

        await userEvent.click(toggle);

        await waitFor(() => expect(setConsentSpy).toHaveBeenCalledWith(true));
        expect(putSpy).toHaveBeenCalledWith("/api/v1/productAnalytics/consent", { granted: true });
    });

    test("closes the client's feature gate when productAnalyticsEnabled flips off mid-session", async () => {
        getSpy.mockResolvedValue({ state: "granted" });
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const router = createMemoryRouter(
            [
                {
                    path: "/",
                    element: (
                        <QueryClientProvider client={queryClient}>
                            <ProductAnalyticsGate />
                        </QueryClientProvider>
                    ),
                },
            ],
            { initialEntries: ["/"] }
        );
        const gate = (enabled: boolean) => (
            <ConfigContext.Provider value={makeConfigValue(enabled)}>
                <RouterProvider router={router} />
            </ConfigContext.Provider>
        );
        const view = render(gate(true));
        await waitFor(() => expect(setFeatureSpy).toHaveBeenCalledWith(true));

        act(() => {
            view.rerender(gate(false));
        });

        await waitFor(() => expect(setFeatureSpy).toHaveBeenCalledWith(false));
    });
});
