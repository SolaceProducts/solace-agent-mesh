// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { RuntimeProblem, RuntimeStatus, RuntimeStatusValue } from "@/types";

import type { PaginatedResponse } from "./pagination";

export const PLATFORM_V2_PREFIX = "/api/v2/platform";

export type ValidationDetails = Record<string, string[]>;

export class ApiV2Error extends Error {
    readonly status: number;
    readonly errorId?: string;
    readonly validationDetails?: ValidationDetails;
    readonly location?: string;

    constructor(status: number, message: string, { errorId, validationDetails, location }: { errorId?: string; validationDetails?: ValidationDetails; location?: string } = {}) {
        super(message);
        this.name = "ApiV2Error";
        this.status = status;
        this.errorId = errorId;
        this.validationDetails = validationDetails;
        this.location = location;
    }
}

const V2_INSTANT = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

export function parseInstant(value: string | null): Date | null {
    if (value === null) return null;
    if (!V2_INSTANT.test(value)) {
        throw new TypeError(`not a v2 date-time: ${JSON.stringify(value)}`);
    }
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) {
        throw new TypeError(`not a v2 date-time: ${JSON.stringify(value)}`);
    }
    return date;
}

export const instantToIso = (instant: string | null) => parseInstant(instant)?.toISOString() ?? "";

type WithoutNulls<T> = { [K in keyof T]: null extends T[K] ? Exclude<T[K], null> | undefined : T[K] };

export function withoutNulls<T extends object>(value: T): WithoutNulls<T> {
    return Object.fromEntries(Object.entries(value).filter(([, member]) => member !== null)) as WithoutNulls<T>;
}

export interface RuntimeStatusV2 {
    status: RuntimeStatusValue;
    problems: { key: string; severity: RuntimeProblem["severity"]; detail: string; sinceTime: string | null }[];
}

export function toRuntimeStatus({ status, problems }: RuntimeStatusV2): RuntimeStatus {
    if (status !== "degraded") return { status };
    return { status, problems: problems.map(p => ({ id: p.key, severity: p.severity, detail: p.detail, since: instantToIso(p.sinceTime) })) };
}

export const pageQuery = ({ pageNumber, pageSize }: { pageNumber: number; pageSize: number }) => `pageNumber=${pageNumber}&pageSize=${pageSize}`;

interface V2Options {
    signal?: AbortSignal;
}

function withFieldErrors(message: string, details?: ValidationDetails): string {
    const fieldErrors = Object.entries(details ?? {})
        .map(([field, errs]) => `${field}: ${(errs ?? []).join(", ")}`)
        .join("; ");
    return fieldErrors ? `${message}: ${fieldErrors}` : message;
}

async function toError(response: Response): Promise<ApiV2Error> {
    const fallback = `Request failed: HTTP ${response.status}`;
    const location = response.headers.get("Location") ?? undefined;
    try {
        const body = (await response.json()) as { message?: string; errorId?: string; validationDetails?: ValidationDetails } | null;
        const message = withFieldErrors(body?.message || fallback, body?.validationDetails);
        return new ApiV2Error(response.status, message, { errorId: body?.errorId, validationDetails: body?.validationDetails, location });
    } catch {
        return new ApiV2Error(response.status, fallback, { location });
    }
}

async function send<T>(request: Promise<Response>): Promise<T> {
    const response = await request;
    if (!response.ok) throw await toError(response);
    return response.status === 204 ? (undefined as T) : ((await response.json()) as T);
}

async function sendResource<T>(request: Promise<Response>): Promise<T> {
    return (await send<{ data: T } | undefined>(request))?.data as T;
}

async function sendBlob(request: Promise<Response>): Promise<Blob> {
    const response = await request;
    if (!response.ok) throw await toError(response);
    return response.blob();
}

const url = (path: string) => PLATFORM_V2_PREFIX + path;
const full = (options?: V2Options) => ({ ...options, fullResponse: true as const });

export const platformV2 = {
    get: <T>(path: string, options?: V2Options) => sendResource<T>(api.platform.get(url(path), full(options))),
    list: <T>(path: string, options?: V2Options) => send<PaginatedResponse<T>>(api.platform.get(url(path), full(options))),
    listPut: <T>(path: string, body?: unknown, options?: V2Options) => send<PaginatedResponse<T>>(api.platform.put(url(path), body, full(options))),
    post: <T>(path: string, body?: unknown, options?: V2Options) => sendResource<T>(api.platform.post(url(path), body, full(options))),
    put: <T>(path: string, body?: unknown, options?: V2Options) => sendResource<T>(api.platform.put(url(path), body, full(options))),
    putList: <T>(path: string, body?: unknown, options?: V2Options) => send<PaginatedResponse<T>>(api.platform.put(url(path), body, full(options))),
    patch: <T>(path: string, body?: unknown, options?: V2Options) => sendResource<T>(api.platform.patch(url(path), body, full(options))),
    delete: (path: string, options?: V2Options) => send<void>(api.platform.delete(url(path), full(options))),
    blob: (path: string, options?: V2Options) => sendBlob(api.platform.get(url(path), full(options))),
    getResponse: async (path: string, options?: V2Options) => {
        const response: Response = await api.platform.get(url(path), full(options));
        if (!response.ok) throw await toError(response);
        return response;
    },
};

export async function postDeployment<T>(path: string, body: unknown): Promise<T> {
    try {
        return await platformV2.post<T>(path, body);
    } catch (error) {
        if (error instanceof ApiV2Error && error.location?.startsWith(PLATFORM_V2_PREFIX)) {
            return platformV2.get<T>(error.location.slice(PLATFORM_V2_PREFIX.length)).catch(() => {
                throw error;
            });
        }
        throw error;
    }
}
