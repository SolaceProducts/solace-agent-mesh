// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";
import { type ConsentState, productAnalyticsKeys } from "@/api/product-analytics";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts/ConfigContext";

import { PrivacySettings } from "./PrivacySettings";

let getSpy: ReturnType<typeof vi.spyOn>;
let putSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    vi.restoreAllMocks();
    getSpy = vi.spyOn(api.webui, "get");
    putSpy = vi.spyOn(api.webui, "put");
});

function makeConfigValue(productAnalyticsEnabled: boolean, activationGateEnabled = false): ConfigContextValue {
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
        activationGateEnabled,
    };
}

function renderSettingsComponent(productAnalyticsEnabled = true, activationGateEnabled = false) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
        <ConfigContext.Provider value={makeConfigValue(productAnalyticsEnabled, activationGateEnabled)}>
            <QueryClientProvider client={client}>
                <PrivacySettings />
            </QueryClientProvider>
        </ConfigContext.Provider>
    );
    return client;
}

function renderSettings(state: ConsentState) {
    getSpy.mockResolvedValue({ state });
    return renderSettingsComponent();
}

async function awaitConsentLoaded(client: QueryClient) {
    await waitFor(() => expect(client.getQueryState(productAnalyticsKeys.consent())?.status).toBe("success"));
}

describe("PrivacySettings", () => {
    test("renders nothing when the product analytics flag is off", async () => {
        getSpy.mockResolvedValue({ state: "unset" });
        renderSettingsComponent(false);

        await waitFor(() => expect(screen.queryByRole("switch")).not.toBeInTheDocument());
        expect(screen.queryByText(/usage analytics/i)).not.toBeInTheDocument();
        expect(getSpy).not.toHaveBeenCalled();
    });

    test("says the data is anonymous on a build without the Solace ID sign-in", async () => {
        getSpy.mockResolvedValue({ state: "unset" });
        renderSettingsComponent(true, false);

        expect(await screen.findByText(/we collect anonymous usage data/)).toBeInTheDocument();
    });

    test("says the data is linked to the Solace ID account once the license is accepted", async () => {
        getSpy.mockResolvedValue({ state: "unset" });
        renderSettingsComponent(true, true);

        expect(await screen.findByText(/linked to your Solace ID account and email address/)).toBeInTheDocument();
        expect(screen.queryByText(/anonymous usage data/)).not.toBeInTheDocument();
    });

    test("reflects granted consent in the toggle", async () => {
        renderSettings("granted");
        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
    });

    test("shows the toggle on when no decision has been recorded", async () => {
        renderSettings("unset");
        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
        expect(screen.getByRole("switch")).toBeEnabled();
    });

    test("shows the toggle off when consent is denied", async () => {
        const client = renderSettings("denied");
        await awaitConsentLoaded(client);
        expect(screen.getByRole("switch")).not.toBeChecked();
        expect(screen.getByRole("switch")).toBeEnabled();
    });

    test("opts out on the first click for an install with no decision recorded", async () => {
        putSpy.mockResolvedValue({ state: "denied" });
        renderSettings("unset");
        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());

        await userEvent.click(screen.getByRole("switch"));

        await waitFor(() => expect(putSpy).toHaveBeenCalledWith("/api/v1/productAnalytics/consent", { granted: false }));
    });

    test.each([
        ["a response with no state", {}],
        ["a state this build does not know", { state: "bogus" }],
    ])("does not present %s as a confident off", async (_label, body) => {
        getSpy.mockResolvedValue(body as never);
        const client = renderSettingsComponent();

        await awaitConsentLoaded(client);

        expect(screen.getByRole("switch")).toBeDisabled();
        expect(screen.getByRole("switch")).not.toBeChecked();
        expect(putSpy).not.toHaveBeenCalled();
    });

    test("does not present an unreadable consent state as a confident off", async () => {
        getSpy.mockRejectedValue(new Error("gateway down"));
        renderSettingsComponent();

        expect(await screen.findByText(/could not read your usage analytics setting/i)).toBeInTheDocument();
        expect(screen.getByRole("switch")).toBeDisabled();
        expect(screen.getByRole("switch")).not.toBeChecked();
    });

    test("recovers the toggle when a retried read succeeds", async () => {
        getSpy.mockRejectedValueOnce(new Error("gateway down")).mockResolvedValue({ state: "granted" });
        renderSettingsComponent();

        await userEvent.click(await screen.findByRole("button", { name: "Retry" }));

        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
        expect(screen.getByRole("switch")).toBeEnabled();
        expect(screen.queryByText(/could not read your usage analytics setting/i)).not.toBeInTheDocument();
    });

    test("holds Retry disabled while a read is in flight", async () => {
        let resolveGet: (value: { state: ConsentState }) => void = () => {};
        getSpy
            .mockResolvedValueOnce({ state: "granted" })
            .mockRejectedValueOnce(new Error("gateway down"))
            .mockImplementation(() => new Promise(resolve => (resolveGet = resolve)));
        const client = renderSettingsComponent();

        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
        await act(async () => {
            await client.refetchQueries({ queryKey: productAnalyticsKeys.consent() });
        });

        await userEvent.click(await screen.findByRole("button", { name: "Retry" }));

        await waitFor(() => expect(screen.getByRole("button", { name: "Retry" })).toBeDisabled());

        resolveGet({ state: "denied" });
        await waitFor(() => expect(screen.getByRole("switch")).not.toBeChecked());
    });

    test("holds the toggle disabled while a decision is in flight", async () => {
        let resolvePut: (value: { state: ConsentState }) => void = () => {};
        putSpy.mockImplementation(() => new Promise(resolve => (resolvePut = resolve)));
        renderSettings("denied");
        await waitFor(() => expect(getSpy).toHaveBeenCalled());

        await userEvent.click(screen.getByRole("switch"));

        await waitFor(() => expect(screen.getByRole("switch")).toBeDisabled());
        expect(putSpy).toHaveBeenCalledTimes(1);

        resolvePut({ state: "granted" });
        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
    });

    test("keeps the last known value when a refetch fails, and says so", async () => {
        getSpy.mockResolvedValueOnce({ state: "granted" }).mockRejectedValue(new Error("gateway down"));
        const client = renderSettingsComponent();

        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());

        await act(async () => {
            await client.refetchQueries({ queryKey: productAnalyticsKeys.consent() });
        });

        expect(await screen.findByText(/could not confirm your usage analytics setting/i)).toBeInTheDocument();
        expect(screen.getByRole("switch")).toBeChecked();
    });

    test("applies the decision without depending on a follow-up read", async () => {
        getSpy.mockResolvedValueOnce({ state: "granted" }).mockRejectedValue(new Error("gateway down"));
        putSpy.mockResolvedValue({ state: "denied" });
        renderSettingsComponent();

        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());

        await userEvent.click(screen.getByRole("switch"));

        await waitFor(() => expect(screen.getByRole("switch")).not.toBeChecked());
    });

    test("opts in when the toggle is switched on", async () => {
        putSpy.mockResolvedValue({ state: "granted" });
        renderSettings("denied");
        await waitFor(() => expect(getSpy).toHaveBeenCalled());

        await userEvent.click(screen.getByRole("switch"));

        await waitFor(() => expect(putSpy).toHaveBeenCalledWith("/api/v1/productAnalytics/consent", { granted: true }));
    });

    test("opts out when the toggle is switched off", async () => {
        putSpy.mockResolvedValue({ state: "denied" });
        renderSettings("granted");
        await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());

        await userEvent.click(screen.getByRole("switch"));

        await waitFor(() => expect(putSpy).toHaveBeenCalledWith("/api/v1/productAnalytics/consent", { granted: false }));
    });

    test("surfaces a failure to save the choice", async () => {
        putSpy.mockRejectedValue(new Error("network down"));
        renderSettings("denied");
        await waitFor(() => expect(getSpy).toHaveBeenCalled());

        await userEvent.click(screen.getByRole("switch"));

        expect(await screen.findByText(/could not save your choice/i)).toBeInTheDocument();
    });

    test("names a refused write as a permission problem and stops offering the toggle", async () => {
        const refusal = new Error("insufficient scope") as Error & { status?: number };
        refusal.status = 403;
        putSpy.mockRejectedValue(refusal);
        renderSettings("denied");
        await waitFor(() => expect(getSpy).toHaveBeenCalled());

        await userEvent.click(screen.getByRole("switch"));

        expect(await screen.findByText(/do not have permission to change this setting/i)).toBeInTheDocument();
        expect(screen.queryByText(/could not save your choice/i)).not.toBeInTheDocument();
        expect(screen.getByRole("switch")).toBeDisabled();
    });

    test("does not leave a stale consent error after a later successful save", async () => {
        putSpy.mockRejectedValueOnce(new Error("network down")).mockResolvedValue({ state: "granted" });
        renderSettings("denied");
        await waitFor(() => expect(getSpy).toHaveBeenCalled());

        await userEvent.click(screen.getByRole("switch"));
        expect(await screen.findByText(/could not save your choice/i)).toBeInTheDocument();

        await userEvent.click(screen.getByRole("switch"));

        await waitFor(() => expect(screen.queryByText(/could not save your choice/i)).not.toBeInTheDocument());
    });
});
