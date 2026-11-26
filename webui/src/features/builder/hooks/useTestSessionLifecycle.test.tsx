// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

const BUILDER_SESSION = "builder-session-id";
const TEST_AGENT = "agent_test-uuid";
const TARGET_NAME = "MyAgent";
const PERSIST_KEY = `sam_test_session::${BUILDER_SESSION}::${TEST_AGENT}`;

const apiGet = vi.fn();
const apiPost = vi.fn();
const apiPatch = vi.fn();

vi.mock("@/api/client", () => ({
    api: {
        webui: {
            get: (...args: unknown[]) => apiGet(...args),
            post: (...args: unknown[]) => apiPost(...args),
            patch: (...args: unknown[]) => apiPatch(...args),
        },
    },
}));

const NEW_SESSION_ID = "00000000-0000-4000-8000-000000000001";
vi.mock("uuid", () => ({ v4: () => NEW_SESSION_ID }));

import { useTestSessionLifecycle } from "./useTestSessionLifecycle";

function renderLifecycle(overrides: Partial<Parameters<typeof useTestSessionLifecycle>[0]> = {}) {
    return renderHook(() =>
        useTestSessionLifecycle({
            builderSessionId: BUILDER_SESSION,
            testAgentName: TEST_AGENT,
            targetName: TARGET_NAME,
            ...overrides,
        })
    );
}

beforeEach(() => {
    localStorage.clear();
    apiGet.mockReset();
    apiPost.mockReset();
    apiPatch.mockReset();
    apiPost.mockResolvedValue(undefined);
    apiPatch.mockResolvedValue(undefined);
});

afterEach(() => {
    vi.useRealTimers();
});

describe("useTestSessionLifecycle", () => {
    test("no candidate → mints a fresh session and persists it", async () => {
        const { result } = renderLifecycle();

        await waitFor(() => {
            expect(result.current.testSessionId).toBe(NEW_SESSION_ID);
        });

        expect(apiGet).not.toHaveBeenCalled();
        expect(apiPost).toHaveBeenCalledWith("/api/v1/sessions", {
            id: NEW_SESSION_ID,
            agentId: TEST_AGENT,
            source: "builder_test",
        });
        expect(apiPatch).toHaveBeenCalledWith(`/api/v1/sessions/${NEW_SESSION_ID}`, { name: `Test: ${TARGET_NAME}` });
        expect(localStorage.getItem(PERSIST_KEY)).toBe(NEW_SESSION_ID);
    });

    test("persisted id, GET 404 → drops it, mints fresh, no parallel POST race", async () => {
        localStorage.setItem(PERSIST_KEY, "dead-id");
        apiGet.mockResolvedValueOnce({ ok: false, status: 404 });

        const { result } = renderLifecycle();

        await waitFor(() => {
            expect(apiGet).toHaveBeenCalledWith("/api/v1/sessions/dead-id", { fullResponse: true });
        });
        await waitFor(() => {
            expect(result.current.testSessionId).toBe(NEW_SESSION_ID);
        });

        expect(apiPost).toHaveBeenCalledTimes(1);
        expect(apiPost).toHaveBeenCalledWith("/api/v1/sessions", {
            id: NEW_SESSION_ID,
            agentId: TEST_AGENT,
            source: "builder_test",
        });
        expect(localStorage.getItem(PERSIST_KEY)).toBe(NEW_SESSION_ID);
        expect(result.current.testSessionId).not.toBe("dead-id");
    });

    test("persisted id, GET 200 → adopts it and does NOT mint a parallel session", async () => {
        localStorage.setItem(PERSIST_KEY, "alive-id");
        apiGet.mockResolvedValueOnce({ ok: true, status: 200 });

        const { result } = renderLifecycle();

        await waitFor(() => {
            expect(result.current.testSessionId).toBe("alive-id");
        });

        expect(apiGet).toHaveBeenCalledWith("/api/v1/sessions/alive-id", { fullResponse: true });
        expect(apiPost).not.toHaveBeenCalled();
        expect(apiPatch).not.toHaveBeenCalled();
        expect(localStorage.getItem(PERSIST_KEY)).toBe("alive-id");
    });

    test("persisted id, GET 500 → preserves localStorage, mints fresh as fallback", async () => {
        localStorage.setItem(PERSIST_KEY, "persisted-id");
        apiGet.mockResolvedValueOnce({ ok: false, status: 500 });
        let resolvePost: () => void = () => {};
        apiPost.mockReturnValueOnce(
            new Promise<void>(r => {
                resolvePost = r;
            })
        );

        const { result } = renderLifecycle();

        await waitFor(() => {
            expect(apiGet).toHaveBeenCalledWith("/api/v1/sessions/persisted-id", { fullResponse: true });
        });
        await waitFor(() => {
            expect(apiPost).toHaveBeenCalledWith("/api/v1/sessions", {
                id: NEW_SESSION_ID,
                agentId: TEST_AGENT,
                source: "builder_test",
            });
        });
        expect(localStorage.getItem(PERSIST_KEY)).toBe("persisted-id");

        await act(async () => {
            resolvePost();
        });
        await waitFor(() => {
            expect(result.current.testSessionId).toBe(NEW_SESSION_ID);
        });
    });

    test("pinned id always wins over a persisted id and is fed straight through validation", async () => {
        localStorage.setItem(PERSIST_KEY, "persisted-id");
        apiGet.mockResolvedValueOnce({ ok: true, status: 200 });
        const onPinApplied = vi.fn();

        const { result } = renderLifecycle({
            pinnedTestSessionId: "pinned-id",
            onPinnedTestSessionApplied: onPinApplied,
        });

        await waitFor(() => {
            expect(result.current.testSessionId).toBe("pinned-id");
        });
        expect(apiGet).toHaveBeenCalledWith("/api/v1/sessions/pinned-id", { fullResponse: true });
        await waitFor(() => {
            expect(onPinApplied).toHaveBeenCalledTimes(1);
        });
        expect(apiPost).not.toHaveBeenCalled();
    });

    test("mint POST failure surfaces a retryable error", async () => {
        apiPost.mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce(undefined);

        const { result } = renderLifecycle();

        await waitFor(() => {
            expect(result.current.sessionError).toBe("Failed to create test session.");
        });
        expect(result.current.testSessionId).toBeNull();

        act(() => {
            result.current.retry();
        });
        await waitFor(() => {
            expect(result.current.testSessionId).toBe(NEW_SESSION_ID);
        });
        expect(result.current.sessionError).toBeNull();
        expect(apiPost).toHaveBeenCalledTimes(2);
    });
});
