// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api, configureSessionIdleTimeout } from "@/api/client";

import type { BackendConfig, ConfigQueryResult } from "./types";

async function resolveDesktopApiBaseUrl(): Promise<string | null> {
    const app = (
        globalThis as unknown as {
            go?: { desktop?: { App?: { APIBaseURL?: () => Promise<string> } } };
        }
    ).go?.desktop?.App;
    if (!app?.APIBaseURL) {
        return null;
    }
    try {
        const base = await app.APIBaseURL();
        return base || null;
    } catch (err) {
        console.error("Failed to resolve desktop API base URL:", err);
        return null;
    }
}

async function fetchConfigWithCsrfRetry(fetchCsrfToken: () => Promise<string | null>): Promise<BackendConfig> {
    let response = await api.webui.get("/api/v1/config", {
        credentials: "include",
        headers: { Accept: "application/json" },
        fullResponse: true,
    });

    if (response.ok) {
        return response.json();
    }

    const errorText = await response.text();
    console.error("Initial config fetch failed:", response.status, errorText);
    if (response.status !== 403) {
        throw new Error(`Failed to fetch config: ${response.status} ${errorText}`);
    }

    console.log("Config fetch failed with 403, attempting to get CSRF token first...");
    const csrfToken = await fetchCsrfToken();
    if (!csrfToken) {
        throw new Error("Failed to obtain CSRF token after config fetch failed.");
    }
    console.log("Retrying config fetch with CSRF token...");
    response = await api.webui.get("/api/v1/config", {
        credentials: "include",
        headers: {
            "X-CSRF-TOKEN": csrfToken,
            Accept: "application/json",
        },
        fullResponse: true,
    });
    if (!response.ok) {
        const retryErrorText = await response.text();
        console.error("Config fetch retry failed:", response.status, retryErrorText);
        throw new Error(`Failed to fetch config on retry: ${response.status} ${retryErrorText}`);
    }
    return response.json();
}

export async function fetchConfig(fetchCsrfToken: () => Promise<string | null>): Promise<ConfigQueryResult> {
    const desktopApiBase = await resolveDesktopApiBaseUrl();
    if (desktopApiBase) {
        api.configure(desktopApiBase, desktopApiBase);
    }
    const data = await fetchConfigWithCsrfRetry(fetchCsrfToken);

    api.configure(desktopApiBase ?? data.frontendServerUrl, desktopApiBase ?? data.frontendPlatformServerUrl ?? "");
    configureSessionIdleTimeout(data.sessionIdleTimeoutSeconds ?? 0);
    return { data, desktopApiBase };
}
