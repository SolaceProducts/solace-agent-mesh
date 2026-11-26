// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, Outlet, RouterProvider, useNavigate } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";
import { type ConsentState, productAnalyticsKeys } from "@/api/product-analytics";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts/ConfigContext";

import { productAnalyticsClient } from "./client";
import { ProductAnalyticsGate, ProductAnalyticsPageViews } from "./ProductAnalyticsGate";

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

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    localStorage.clear();
    productAnalyticsClient.destroy();
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    productAnalyticsClient.setConsent(false);
    productAnalyticsClient.setFeatureEnabled(false);
    productAnalyticsClient.destroy();
    vi.restoreAllMocks();
});

function eventsNamed(name: string): unknown[][] {
    return (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === name);
}

function Layout() {
    const navigate = useNavigate();
    return (
        <>
            <ProductAnalyticsGate />
            <ProductAnalyticsPageViews />
            <button onClick={() => navigate("/connectors")}>go-connectors</button>
            <button onClick={() => navigate("/models")}>go-models</button>
            <Outlet />
        </>
    );
}

function renderApp(consentState: ConsentState) {
    vi.spyOn(api.webui, "get").mockResolvedValue({ state: consentState } as never);

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const router = createMemoryRouter(
        [
            {
                path: "/",
                element: (
                    <ConfigContext.Provider value={makeConfigValue(true)}>
                        <QueryClientProvider client={queryClient}>
                            <Layout />
                        </QueryClientProvider>
                    </ConfigContext.Provider>
                ),
                children: [
                    { path: "connectors", element: <div>connectors</div> },
                    { path: "models", element: <div>models</div> },
                ],
            },
        ],
        { initialEntries: ["/"] }
    );

    render(<RouterProvider router={router} />);
    return queryClient;
}

async function awaitConsentLoaded(client: QueryClient) {
    await waitFor(() => expect(client.getQueryState(productAnalyticsKeys.consent())?.status).toBe("success"));
}

describe("app:launched", () => {
    test("is reported once consent is granted", async () => {
        renderApp("granted");

        await waitFor(() => expect(eventsNamed("app:launched")).toHaveLength(1));
        expect(eventsNamed("app:launched")[0]).toEqual(["app:launched", {}]);
    });

    test("survives route changes without reporting again, while page_view follows every one", async () => {
        const user = userEvent.setup();
        renderApp("granted");
        await waitFor(() => expect(eventsNamed("app:launched")).toHaveLength(1));

        await user.click(screen.getByRole("button", { name: "go-connectors" }));
        await waitFor(() => expect(eventsNamed("page_view").length).toBeGreaterThan(1));
        await user.click(screen.getByRole("button", { name: "go-models" }));
        await waitFor(() => expect(eventsNamed("page_view").length).toBeGreaterThan(2));

        expect(eventsNamed("app:launched")).toHaveLength(1);
    });

    test("is reported once consent resolves to unset", async () => {
        renderApp("unset");

        await waitFor(() => expect(eventsNamed("app:launched")).toHaveLength(1));
        expect(eventsNamed("app:launched")[0]).toEqual(["app:launched", {}]);
    });

    test("is not reported when consent is denied", async () => {
        const queryClient = renderApp("denied");

        await awaitConsentLoaded(queryClient);
        expect(eventsNamed("app:launched")).toHaveLength(0);
    });
});
