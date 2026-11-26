// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const CLIENT_SURFACE_HEADER = { "X-Sam-Client-Surface": "ui" } as const;

import { reportingSessionId } from "@/common/product-analytics/report";

import { getApiBearerToken, getSamAccessToken } from "@/utils/api";
import { browserTimezone } from "@/utils/browserTimezone";
import { navigateToLogin } from "@/utils/loginRedirect";
import { stashPostLoginRedirect } from "@/utils/url";

const clientSessionHeader = (): Record<string, string> => {
    const session = reportingSessionId();
    return session ? { "X-Sam-Client-Session": session } : {};
};

const userTimezoneHeader = (): Record<string, string> => {
    const tz = browserTimezone();
    return tz ? { "X-Sam-User-Timezone": tz } : {};
};

interface RequestOptions {
    headers?: HeadersInit;
    signal?: AbortSignal;
    keepalive?: boolean;
    credentials?: RequestCredentials;
}

/* eslint-disable @typescript-eslint/no-explicit-any -- API responses vary; callers can specify types for safety */
interface HttpMethods {
    get: {
        <T = any>(endpoint: string, options?: RequestOptions): Promise<T>;
        (endpoint: string, options: RequestOptions & { fullResponse: true }): Promise<Response>;
    };
    post: {
        <T = any>(endpoint: string, body?: unknown, options?: RequestOptions): Promise<T>;
        (endpoint: string, body: unknown, options: RequestOptions & { fullResponse: true }): Promise<Response>;
    };
    put: {
        <T = any>(endpoint: string, body?: unknown, options?: RequestOptions): Promise<T>;
        (endpoint: string, body: unknown, options: RequestOptions & { fullResponse: true }): Promise<Response>;
    };
    delete: {
        <T = any>(endpoint: string, options?: RequestOptions): Promise<T>;
        (endpoint: string, options: RequestOptions & { fullResponse: true }): Promise<Response>;
    };
    patch: {
        <T = any>(endpoint: string, body?: unknown, options?: RequestOptions): Promise<T>;
        (endpoint: string, body: unknown, options: RequestOptions & { fullResponse: true }): Promise<Response>;
    };
    getFullUrl: (endpoint: string) => string;
}
/* eslint-enable @typescript-eslint/no-explicit-any */

const getRefreshToken = () => localStorage.getItem("refresh_token");

const setTokens = (accessToken: string, samAccessToken: string, refreshToken: string) => {
    localStorage.setItem("access_token", accessToken);
    localStorage.setItem("refresh_token", refreshToken);
    if (samAccessToken) {
        localStorage.setItem("sam_access_token", samAccessToken);
    } else {
        localStorage.removeItem("sam_access_token");
    }
    scheduleProactiveRefresh();
};

const clearTokens = () => {
    localStorage.removeItem("access_token");
    localStorage.removeItem("sam_access_token");
    localStorage.removeItem("refresh_token");
    cancelProactiveRefresh();
};

let pendingRefresh: Promise<string | null> | null = null;

const PROACTIVE_REFRESH_MARGIN_MS = 5 * 60 * 1000;
const MIN_PROACTIVE_REFRESH_DELAY_MS = 30 * 1000;
let proactiveRefreshTimer: ReturnType<typeof setTimeout> | null = null;

const getTokenExpMs = (token: string | null): number | null => {
    if (!token) return null;
    try {
        const payload = token.split(".")[1];
        if (!payload) return null;
        const base64 = payload.replace(/-/g, "+").replace(/_/g, "/");
        const decoded = JSON.parse(atob(base64));
        if (typeof decoded.exp === "number") {
            return decoded.exp * 1000;
        }
    } catch {}
    return null;
};

const scheduleProactiveRefresh = () => {
    scheduleIdleWatchdog();
    if (proactiveRefreshTimer) {
        clearTimeout(proactiveRefreshTimer);
        proactiveRefreshTimer = null;
    }

    const samExp = getTokenExpMs(getSamAccessToken());
    const accessExp = getTokenExpMs(localStorage.getItem("access_token"));
    const expirations = [samExp, accessExp].filter((e): e is number => e !== null);

    if (expirations.length === 0) return;

    const earliestExp = Math.min(...expirations);
    const now = Date.now();
    const delay = earliestExp - now - PROACTIVE_REFRESH_MARGIN_MS;

    if (delay <= 0) {
        const floorDelay = MIN_PROACTIVE_REFRESH_DELAY_MS;
        console.debug(`[api/client] Token near expiry, scheduling proactive refresh in ${floorDelay / 1000}s`);
        proactiveRefreshTimer = setTimeout(() => {
            proactiveRefreshTimer = null;
            console.debug("[api/client] Proactive token refresh triggered (near-expiry)");
            void refreshToken();
        }, floorDelay);
        return;
    }

    console.debug(`[api/client] Scheduling proactive token refresh in ${Math.round(delay / 1000)}s`);
    proactiveRefreshTimer = setTimeout(() => {
        proactiveRefreshTimer = null;
        console.debug("[api/client] Proactive token refresh triggered");
        void refreshToken();
    }, delay);
};

const cancelProactiveRefresh = () => {
    cancelIdleWatchdog();
    if (proactiveRefreshTimer) {
        clearTimeout(proactiveRefreshTimer);
        proactiveRefreshTimer = null;
    }
};

const SESSION_IDLE_TIMEOUT_EVENT = "sam:session-idle-timeout";
const ACTIVITY_EVENTS = ["pointerdown", "keydown", "wheel", "touchstart"] as const;
const SHARED_LAST_ACTIVITY_KEY = "sam_last_activity_at";
const IDLE_SIGNED_OUT_KEY = "sam_idle_signed_out_at";
const SHARED_ACTIVITY_WRITE_INTERVAL_MS = 15 * 1000;
const MAX_TIMER_DELAY_MS = 2 ** 31 - 1;

let sessionIdleTimeoutMs = 0;
let lastActivityAt = Date.now();
let idleWatchdogTimer: ReturnType<typeof setTimeout> | null = null;
let activityListenersInstalled = false;

const readSharedActivityAt = (): number => {
    try {
        return Number(localStorage.getItem(SHARED_LAST_ACTIVITY_KEY)) || 0;
    } catch {
        return 0;
    }
};

const noteActivity = () => {
    lastActivityAt = Date.now();
    if (sessionIdleTimeoutMs <= 0 || lastActivityAt - readSharedActivityAt() < SHARED_ACTIVITY_WRITE_INTERVAL_MS) return;
    try {
        localStorage.setItem(SHARED_LAST_ACTIVITY_KEY, String(lastActivityAt));
    } catch {}
};

const latestActivityAt = () => Math.max(lastActivityAt, readSharedActivityAt());

const idleExceeded = () => sessionIdleTimeoutMs > 0 && Date.now() - latestActivityAt() >= sessionIdleTimeoutMs;

const onVisibilityChange = () => {
    if (document.visibilityState !== "visible") return;
    if (idleExceeded()) {
        signOutForInactivity();
        return;
    }
    noteActivity();
};

const onStorageChange = (event: StorageEvent) => {
    if (event.key !== IDLE_SIGNED_OUT_KEY || !event.newValue) return;
    cancelIdleWatchdog();
    globalThis.dispatchEvent(new Event(SESSION_IDLE_TIMEOUT_EVENT));
};

const installActivityListeners = () => {
    if (activityListenersInstalled) return;
    activityListenersInstalled = true;
    for (const name of ACTIVITY_EVENTS) {
        globalThis.addEventListener(name, noteActivity, { passive: true, capture: true });
    }
    document.addEventListener("visibilitychange", onVisibilityChange);
    globalThis.addEventListener("storage", onStorageChange);
};

const configureSessionIdleTimeout = (seconds: number) => {
    sessionIdleTimeoutMs = Math.max(0, seconds) * 1000;
    if (sessionIdleTimeoutMs > 0) {
        installActivityListeners();
        noteActivity();
    }
    scheduleIdleWatchdog();
};

const scheduleIdleWatchdog = () => {
    cancelIdleWatchdog();
    if (sessionIdleTimeoutMs <= 0 || !getApiBearerToken()) return;
    const delay = Math.min(Math.max(latestActivityAt() + sessionIdleTimeoutMs - Date.now(), 0), MAX_TIMER_DELAY_MS);
    idleWatchdogTimer = setTimeout(() => {
        idleWatchdogTimer = null;
        if (idleExceeded()) {
            signOutForInactivity();
        } else {
            scheduleIdleWatchdog();
        }
    }, delay);
};

const cancelIdleWatchdog = () => {
    if (idleWatchdogTimer) {
        clearTimeout(idleWatchdogTimer);
        idleWatchdogTimer = null;
    }
};

const revokeSessionCookie = (bearer: string) => {
    void fetch(api.webui.getFullUrl("/api/v1/auth/logout"), {
        method: "POST",
        keepalive: true,
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${bearer}` },
        body: "{}",
    }).catch(() => undefined);
};

const signOutForInactivity = () => {
    const bearer = getApiBearerToken();
    if (!bearer && !getRefreshToken()) return;
    if (bearer) revokeSessionCookie(bearer);
    try {
        localStorage.setItem(IDLE_SIGNED_OUT_KEY, String(Date.now()));
    } catch {}
    clearTokens();
    globalThis.dispatchEvent(new Event(SESSION_IDLE_TIMEOUT_EVENT));
};

const endLapsedSession = () => {
    const bearer = getApiBearerToken();
    if (bearer) revokeSessionCookie(bearer);
    clearTokens();
};

const refusalErrorType = async (response: Response): Promise<string | null> => {
    try {
        const body = (await response.json()) as { errorType?: unknown };
        return typeof body?.errorType === "string" ? body.errorType : null;
    } catch {
        return null;
    }
};

const refreshToken = async () => {
    if (sessionStorage.getItem("logout_in_progress") === "true") {
        return null;
    }

    const token = getRefreshToken();
    if (!token) {
        return null;
    }

    if (pendingRefresh) {
        return pendingRefresh;
    }

    pendingRefresh = (async () => {
        const samToken = getSamAccessToken();
        const response = await fetch(api.webui.getFullUrl("/api/v1/auth/refresh"), {
            method: "POST",
            headers: { "Content-Type": "application/json", ...(samToken ? { Authorization: `Bearer ${samToken}` } : {}) },
            body: JSON.stringify({ refresh_token: token }),
        });

        if (!getRefreshToken()) {
            return null;
        }

        if (response.ok) {
            const data = await response.json();
            setTokens(data.access_token, data.sam_access_token, data.refresh_token);
            return getApiBearerToken();
        }

        if (response.status === 401 && (await refusalErrorType(response)) === "session_expired") {
            if (idleExceeded()) {
                signOutForInactivity();
            } else {
                endLapsedSession();
            }
            return null;
        }

        clearTokens();
        stashPostLoginRedirect();
        navigateToLogin(api.webui.getFullUrl("/api/v1/auth/login"));
        return null;
    })()
        .catch(() => null)
        .finally(() => {
            pendingRefresh = null;
        });

    return pendingRefresh;
};

interface ParsedErrorBody {
    text: string;
    json?: Record<string, unknown>;
}

const readErrorBody = async (response: Response): Promise<ParsedErrorBody> => {
    let text = "";
    try {
        text = await response.text();
    } catch {
        return { text };
    }
    try {
        const parsed = JSON.parse(text);
        return parsed && typeof parsed === "object" ? { text, json: parsed as Record<string, unknown> } : { text };
    } catch {
        return { text };
    }
};

const fallbackErrorMessage = (response: Response): string => {
    const status = response.statusText || `HTTP ${response.status}`;
    return `Request failed: ${status}`;
};

const errorMessageFromBody = (response: Response, { text, json }: ParsedErrorBody): string => {
    const fallbackMessage = fallbackErrorMessage(response);
    if (!text) return fallbackMessage;
    if (!json) return text.length < 500 ? text : fallbackMessage;

    const fieldErrors = Object.entries(validationDetailsFromBody(json) ?? {})
        .map(([field, errs]) => `${field}: ${errs.join(", ")}`)
        .join("; ");
    if (fieldErrors) {
        return `${(json.message as string | undefined) ?? "Validation error"}: ${fieldErrors}`;
    }

    if (json.error && typeof json.error === "object") {
        const { message: rpcMessage } = json.error as { message?: string };
        if (rpcMessage) return rpcMessage;
    }

    return (json.message as string | undefined) || fallbackMessage;
};

const getErrorFromResponse = async (response: Response): Promise<string> => errorMessageFromBody(response, await readErrorBody(response));

const authenticatedFetch = async (url: string, options: RequestInit = {}) => {
    let bearerToken = getApiBearerToken();

    if (!bearerToken) {
        if (getRefreshToken()) {
            const newToken = await refreshToken();
            if (!newToken) {
                return new Response(JSON.stringify({ message: "Not authenticated", errorType: "authentication_required" }), { status: 401 });
            }
            bearerToken = newToken;
        }
    }

    const response = await fetch(url, {
        ...options,
        headers: {
            ...options.headers,
            ...CLIENT_SURFACE_HEADER,
            ...clientSessionHeader(),
            ...userTimezoneHeader(),
            Authorization: `Bearer ${bearerToken}`,
        },
    });

    if (response.status === 401) {
        const newBearerToken = await refreshToken();
        if (newBearerToken) {
            const retryResponse = await fetch(url, {
                ...options,
                headers: {
                    ...options.headers,
                    ...CLIENT_SURFACE_HEADER,
                    ...clientSessionHeader(),
                    ...userTimezoneHeader(),
                    Authorization: `Bearer ${newBearerToken}`,
                },
            });

            if (retryResponse.status === 401) {
                clearTokens();
                stashPostLoginRedirect();
                navigateToLogin(api.webui.getFullUrl("/api/v1/auth/login"));
                return retryResponse;
            }

            return retryResponse;
        }
    }

    return response;
};

const validationDetailsFromBody = (json: ParsedErrorBody["json"]): Record<string, string[]> | undefined => {
    const details = json?.validationDetails;
    if (!details || typeof details !== "object" || Array.isArray(details)) return undefined;
    const out: Record<string, string[]> = {};
    for (const [field, msgs] of Object.entries(details as Record<string, unknown>)) {
        const list = (Array.isArray(msgs) ? msgs : [msgs]).filter((m): m is string => typeof m === "string" && m !== "");
        if (list.length > 0) out[field] = list;
    }
    return Object.keys(out).length > 0 ? out : undefined;
};

const rpcErrorCodeFromBody = (json: ParsedErrorBody["json"]): number | undefined => {
    const code = (json?.error as { code?: unknown } | undefined)?.code;
    return typeof code === "number" ? code : undefined;
};

export type ApiError = Error & { status?: number; validationDetails?: Record<string, string[]>; errorId?: string; rpcErrorCode?: number };

const errorIdFromBody = (json: ParsedErrorBody["json"]): string | undefined => (typeof json?.errorId === "string" && json.errorId !== "" ? json.errorId : undefined);

const fetchWithError = async (url: string, options: RequestInit = {}) => {
    const response = await authenticatedFetch(url, options);

    if (!response.ok) {
        const body = await readErrorBody(response);
        const validationDetails = validationDetailsFromBody(body.json);
        const rpcErrorCode = rpcErrorCodeFromBody(body.json);
        const errorId = errorIdFromBody(body.json);
        const error = new Error(errorMessageFromBody(response, body)) as ApiError;
        error.status = response.status;
        if (errorId) error.errorId = errorId;
        if (validationDetails) error.validationDetails = validationDetails;
        if (rpcErrorCode !== undefined) error.rpcErrorCode = rpcErrorCode;
        throw error;
    }

    return response;
};

const fetchJsonWithError = async (url: string, options: RequestInit = {}) => {
    const response = await fetchWithError(url, options);
    if (response.status === 204) {
        return undefined;
    }
    const text = await response.text();
    return text ? JSON.parse(text) : undefined;
};

type InternalRequestOptions = RequestOptions & RequestInit & { fullResponse?: boolean };

class ApiClient {
    private webuiBaseUrl = "";
    private platformBaseUrl = "";

    webui: HttpMethods;
    platform: HttpMethods;

    constructor() {
        this.webui = this.createHttpMethods(() => this.webuiBaseUrl);
        this.platform = this.createHttpMethods(() => this.platformBaseUrl);
    }

    configure(webuiUrl: string, platformUrl: string) {
        this.webuiBaseUrl = webuiUrl;
        this.platformBaseUrl = platformUrl;
    }

    platformOrigin(): string {
        return this.platformBaseUrl || globalThis.location.origin;
    }

    private async request(baseUrl: string, endpoint: string, options?: InternalRequestOptions) {
        const url = `${baseUrl}${endpoint}`;
        const { fullResponse, ...fetchOptions } = options || {};

        if (fullResponse) {
            return authenticatedFetch(url, fetchOptions);
        }

        return fetchJsonWithError(url, fetchOptions);
    }

    private buildRequestWithBody(method: string, body: unknown, options?: InternalRequestOptions): InternalRequestOptions {
        if (body instanceof FormData) {
            return { ...options, method, body };
        }
        if (body === undefined || body === null) {
            return { ...options, method };
        }
        return {
            ...options,
            method,
            headers: { "Content-Type": "application/json", ...options?.headers },
            body: JSON.stringify(body),
        };
    }

    private createHttpMethods(getBaseUrl: () => string): HttpMethods {
        return {
            get: ((endpoint: string, options?: InternalRequestOptions) => this.request(getBaseUrl(), endpoint, options)) as HttpMethods["get"],

            post: ((endpoint: string, body?: unknown, options?: InternalRequestOptions) => this.request(getBaseUrl(), endpoint, this.buildRequestWithBody("POST", body, options))) as HttpMethods["post"],

            put: ((endpoint: string, body?: unknown, options?: InternalRequestOptions) => this.request(getBaseUrl(), endpoint, this.buildRequestWithBody("PUT", body, options))) as HttpMethods["put"],

            delete: ((endpoint: string, options?: InternalRequestOptions) => this.request(getBaseUrl(), endpoint, { ...options, method: "DELETE" })) as HttpMethods["delete"],

            patch: ((endpoint: string, body?: unknown, options?: InternalRequestOptions) => this.request(getBaseUrl(), endpoint, this.buildRequestWithBody("PATCH", body, options))) as HttpMethods["patch"],

            getFullUrl: (endpoint: string) => `${getBaseUrl()}${endpoint}`,
        };
    }
}

export const api = new ApiClient();
export { cancelProactiveRefresh, configureSessionIdleTimeout, getErrorFromResponse, refreshToken, scheduleProactiveRefresh, SESSION_IDLE_TIMEOUT_EVENT, setTokens };
