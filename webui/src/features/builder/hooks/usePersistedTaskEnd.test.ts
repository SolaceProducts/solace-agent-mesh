// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

import { usePersistedTaskEnd } from "./usePersistedTaskEnd";

const getMock = vi.hoisted(() => vi.fn<(path: string) => Promise<unknown>>());

vi.mock("@/api/client", () => ({ api: { webui: { get: getMock } } }));

const user = (taskId?: string) => ({ isUser: true, parts: [], taskId, metadata: { messageId: "u" } }) as unknown as MessageFE;
const agent = (taskId?: string) => ({ isUser: false, isComplete: true, parts: [], taskId, metadata: { messageId: "a" } }) as unknown as MessageFE;

type Props = { messages: MessageFE[] | undefined; enabled: boolean };

const reply = (status: string | undefined, isRunning = false) => ({ task: { id: "t", ...(status ? { status } : {}) }, isRunning });
const httpError = (status: number) => Object.assign(new Error(`HTTP ${status}`), { status });

const RETRY_DELAYS_MS = [1500, 4000, 8000];
const ALL_RETRIES_MS = RETRY_DELAYS_MS.reduce((a, b) => a + b, 0);

function setup(initial: Props) {
    return renderHook((p: Props) => usePersistedTaskEnd(p), { initialProps: initial });
}

const elapse = (ms = 0) =>
    act(async () => {
        await vi.advanceTimersByTimeAsync(ms);
    });

const messages = [user("t1"), agent("t1")];

beforeEach(() => {
    vi.useFakeTimers();
    getMock.mockReset();
});

afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
});

describe("usePersistedTaskEnd: what the gateway recorded for the latest task", () => {
    test.each([
        { status: "canceled", abnormal: true },
        { status: "failed", abnormal: true },
        { status: "timeout", abnormal: true },
        { status: "error", abnormal: true },
        { status: "completed", abnormal: false },
        { status: "rejected", abnormal: false },
    ])("a task recorded as $status ends abnormally: $abnormal", async ({ status, abnormal }) => {
        getMock.mockResolvedValue(reply(status));

        const { result } = setup({ messages, enabled: true });
        await elapse();

        expect(getMock).toHaveBeenCalledTimes(1);
        expect(result.current).toBe(abnormal);
    });

    test("reads the status of the task of the latest message that has one, with the id encoded", async () => {
        getMock.mockResolvedValue(reply("canceled"));

        setup({ messages: [user("t1"), agent("t1"), user("a/b?c"), agent(undefined)], enabled: true });
        await elapse();

        expect(getMock).toHaveBeenCalledWith("/api/v1/tasks/a%2Fb%3Fc/status");
    });
});

describe("usePersistedTaskEnd: a record that is not there yet", () => {
    test.each([
        { name: "a task with no status yet", answer: () => Promise.resolve(reply(undefined)) },
        { name: "a malformed reply", answer: () => Promise.resolve(null) },
        { name: "a row not written yet (404)", answer: () => Promise.reject(httpError(404)) },
        { name: "the gateway briefly unreachable (503)", answer: () => Promise.reject(httpError(503)) },
        { name: "a network error", answer: () => Promise.reject(new TypeError("Failed to fetch")) },
        { name: "a task the gateway still reports as running", answer: () => Promise.resolve(reply("timeout", true)) },
    ])("is read again, and gives no evidence once the retries run out: $name", async ({ answer }) => {
        vi.spyOn(console, "warn").mockImplementation(() => {});
        getMock.mockImplementation(answer);

        const { result } = setup({ messages, enabled: true });
        await elapse();
        expect(getMock).toHaveBeenCalledTimes(1);

        await elapse(ALL_RETRIES_MS);
        expect(getMock).toHaveBeenCalledTimes(1 + RETRY_DELAYS_MS.length);
        expect(result.current).toBe(false);

        await elapse(ALL_RETRIES_MS);
        expect(getMock).toHaveBeenCalledTimes(1 + RETRY_DELAYS_MS.length);
    });

    test.each([
        { name: "a row written after the reload (404 first)", first: () => Promise.reject(httpError(404)) },
        { name: "the gateway recovering (503 first)", first: () => Promise.reject(httpError(503)) },
        { name: "a task not yet terminal", first: () => Promise.resolve(reply(undefined)) },
    ])("a retry that finds the Stop is honoured: $name", async ({ first }) => {
        vi.spyOn(console, "warn").mockImplementation(() => {});
        getMock.mockImplementationOnce(first).mockResolvedValue(reply("canceled"));

        const { result } = setup({ messages, enabled: true });
        await elapse();
        expect(result.current).toBe(false);

        await elapse(RETRY_DELAYS_MS[0]);

        expect(getMock).toHaveBeenCalledTimes(2);
        expect(result.current).toBe(true);
    });

    test("retries wait, they do not fire back to back", async () => {
        getMock.mockResolvedValue(reply(undefined));

        setup({ messages, enabled: true });
        await elapse(RETRY_DELAYS_MS[0] - 1);

        expect(getMock).toHaveBeenCalledTimes(1);
    });

    test("a retry that finds a normal end stops retrying", async () => {
        getMock.mockResolvedValueOnce(reply(undefined)).mockResolvedValue(reply("completed"));

        const { result } = setup({ messages, enabled: true });
        await elapse(ALL_RETRIES_MS);

        expect(getMock).toHaveBeenCalledTimes(2);
        expect(result.current).toBe(false);
    });

    test("a failure other than a missing row is logged with the task id and status, a missing row is not", async () => {
        const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
        getMock.mockRejectedValueOnce(httpError(404));
        setup({ messages, enabled: true });
        await elapse();
        expect(warn).not.toHaveBeenCalled();

        getMock.mockReset();
        getMock.mockRejectedValueOnce(httpError(502));
        setup({ messages: [user("t9"), agent("t9")], enabled: true });
        await elapse();

        expect(warn).toHaveBeenCalledTimes(1);
        expect(warn.mock.calls[0]).toEqual([expect.stringContaining("[builder]"), "t9", 502]);
    });

    test("retries stop when the task changes or the builder starts responding", async () => {
        vi.spyOn(console, "warn").mockImplementation(() => {});
        getMock.mockRejectedValue(httpError(503));
        const { rerender, unmount } = setup({ messages, enabled: true });
        await elapse();

        rerender({ messages, enabled: false });
        await elapse(ALL_RETRIES_MS);
        expect(getMock).toHaveBeenCalledTimes(1);

        rerender({ messages, enabled: true });
        await elapse();
        expect(getMock).toHaveBeenCalledTimes(2);
        unmount();
        await elapse(ALL_RETRIES_MS);
        expect(getMock).toHaveBeenCalledTimes(2);
    });
});

describe("usePersistedTaskEnd: when it reads", () => {
    test.each([
        { name: "not enabled", messages, enabled: false },
        { name: "no messages yet", messages: undefined, enabled: true },
        { name: "no message carries a task id", messages: [user(), agent()], enabled: true },
    ])("makes no request and claims nothing: $name", async ({ messages, enabled }) => {
        getMock.mockResolvedValue(reply("canceled"));

        const { result } = setup({ messages, enabled });
        await elapse(ALL_RETRIES_MS);

        expect(getMock).not.toHaveBeenCalled();
        expect(result.current).toBe(false);
    });

    test("history that arrives after the first render is read once it has a task", async () => {
        getMock.mockResolvedValue(reply("canceled"));
        const { result, rerender } = setup({ messages: [], enabled: true });
        await elapse();
        expect(getMock).not.toHaveBeenCalled();

        rerender({ messages, enabled: true });
        await elapse();

        expect(result.current).toBe(true);
    });

    test("a record read while idle is withdrawn while the builder responds, and read again once it is idle", async () => {
        getMock.mockResolvedValue(reply("canceled"));
        const { result, rerender } = setup({ messages, enabled: true });
        await elapse();
        expect(result.current).toBe(true);

        rerender({ messages, enabled: false });
        expect(result.current).toBe(false);

        rerender({ messages, enabled: true });
        await elapse();
        expect(getMock).toHaveBeenCalledTimes(2);
        expect(result.current).toBe(true);
    });

    test("the record of an earlier task never counts for the next one, whatever the next one recorded", async () => {
        getMock.mockResolvedValueOnce(reply("canceled")).mockResolvedValueOnce(reply("completed"));
        const { result, rerender } = setup({ messages, enabled: true });
        await elapse();
        expect(result.current).toBe(true);

        rerender({ messages: [...messages, user("t2")], enabled: true });
        expect(result.current).toBe(false);

        await elapse();
        expect(getMock).toHaveBeenCalledWith("/api/v1/tasks/t2/status");
        expect(result.current).toBe(false);
    });

    test.each([
        { name: "a failed one", late: () => Promise.reject(httpError(404)) },
        { name: "one that read as normal", late: () => Promise.resolve(reply("completed")) },
    ])("a slow answer for a task that is no longer the latest cannot erase the record of the latest ($name)", async ({ late }) => {
        let answerFirst: () => void = () => {};
        getMock.mockReturnValueOnce(new Promise((resolve, reject) => (answerFirst = () => late().then(resolve, reject)))).mockResolvedValueOnce(reply("canceled"));
        const { result, rerender } = setup({ messages, enabled: true });

        rerender({ messages: [...messages, user("t2"), agent("t2")], enabled: true });
        await elapse();
        expect(result.current).toBe(true);
        answerFirst();
        await elapse(ALL_RETRIES_MS);

        expect(result.current).toBe(true);
        expect(getMock).toHaveBeenCalledTimes(2);
    });
});
