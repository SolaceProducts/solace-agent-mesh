// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { ApplyBuildResponse } from "@/api/builder/applyTypes";
import { previewBuild } from "@/api/builder/service";
import { api } from "@/api/client";

import { UNAVAILABLE_RETRY_BASE_MS, UNAVAILABLE_RETRY_MAX_MS, usePlatformCheck, type UsePlatformCheckArgs } from "./usePlatformCheck";

const BUILDS_PATH = "/api/internal/platform/builder/builds";
const dryRunWire = vi.fn();

const passed = (): ApplyBuildResponse => ({ dryRun: true, dropped: [], operations: [], summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 } });
const refused = (message: string): ApplyBuildResponse => ({
    dryRun: true,
    dropped: [],
    operations: [{ kind: "agent", name: "kept", action: "create", status: "failed", error: { message, httpStatus: 422, errorId: "err-1" } }],
    summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
});

let contents: Record<string, string>;
const fetchConfigByFilename = vi.fn(async (filename: string) => contents[filename] ?? null);

const agent = (name: string) => ({ name, type: "agent", filename: `${name}.yaml`, status: "validated" });

function argsOf(over: Partial<UsePlatformCheckArgs> = {}): UsePlatformCheckArgs {
    return {
        enabled: true,
        idle: true,
        buildName: "build",
        sessionId: "session-a",
        components: [agent("kept")],
        fetchConfigByFilename,
        applyBuild: (request, signal) => previewBuild(request, { signal }),
        ...over,
    };
}

function mount(initial: Partial<UsePlatformCheckArgs> = {}) {
    return renderHook((over: Partial<UsePlatformCheckArgs>) => usePlatformCheck(argsOf(over)), { initialProps: initial });
}

const notAllowedWire = (): ApplyBuildResponse => ({
    dryRun: true,
    dropped: [],
    operations: [{ kind: "agent", name: "kept", action: "create", status: "failed", error: { message: "not allowed", httpStatus: 403, errorId: "err-2" } }],
    summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
});

beforeEach(() => {
    vi.clearAllMocks();
    contents = { "kept.yaml": "apps:\n  - name: kept\n" };
    vi.spyOn(api.platform, "post").mockImplementation(((url: string, body: unknown) => (url === BUILDS_PATH ? dryRunWire(body) : Promise.reject(new Error(`unexpected POST ${url}`)))) as typeof api.platform.post);
    dryRunWire.mockResolvedValue(passed());
});

describe("usePlatformCheck", () => {
    test("runs one dry run of the build as it stands, and says what the platform answered", async () => {
        const { result } = mount();

        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        expect(dryRunWire).toHaveBeenCalledTimes(1);
        expect(dryRunWire).toHaveBeenCalledWith({ buildName: "build", dryRun: true, components: [expect.objectContaining({ kind: "agent", name: "kept", content: contents["kept.yaml"] })] });
        expect(JSON.parse(result.current.key!)).toEqual({ buildName: "build", components: [expect.objectContaining({ name: "kept", content: contents["kept.yaml"] })] });
    });

    test("does not ask again when it renders with the same build", async () => {
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));

        rerender({ components: [agent("kept")] });
        rerender({ idle: true });

        await act(async () => {});
        expect(dryRunWire).toHaveBeenCalledTimes(1);
    });

    test("asks once more for a validated state whose configs changed, and not for one it has answered", async () => {
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));

        rerender({ enabled: false });
        await waitFor(() => expect(result.current.check.kind).toBe("idle"));
        contents = { "kept.yaml": "apps:\n  - name: kept-edited\n" };
        rerender({ enabled: true });
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        expect(dryRunWire).toHaveBeenCalledTimes(2);

        rerender({ enabled: false });
        contents = { "kept.yaml": "apps:\n  - name: kept\n" };
        rerender({ enabled: true });
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        expect(dryRunWire).toHaveBeenCalledTimes(2);
    });

    test("keeps no answers from another session", async () => {
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));

        rerender({ sessionId: "session-b" });
        await waitFor(() => expect(dryRunWire).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
    });

    test("does not ask while the stamp is not current", async () => {
        const { result } = mount({ enabled: false });

        await act(async () => {});
        expect(dryRunWire).not.toHaveBeenCalled();
        expect(result.current.check).toEqual({ kind: "idle" });
        expect(result.current.key).toBeNull();
    });

    test("does not ask while an agent turn is running, and asks when it ends", async () => {
        const { result, rerender } = mount({ idle: false });

        await act(async () => {});
        expect(dryRunWire).not.toHaveBeenCalled();
        expect(result.current.check).toEqual({ kind: "idle" });

        rerender({ idle: true });
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        expect(dryRunWire).toHaveBeenCalledTimes(1);
    });

    test("says a refusal as the platform gave it", async () => {
        dryRunWire.mockResolvedValue(refused("model not allowed"));
        const { result } = mount();

        await waitFor(() => expect(result.current.check.kind).toBe("refused"));
        expect(result.current.check).toMatchObject({ problems: [{ kind: "agent", name: "kept", message: "model not allowed" }] });
    });

    test("checkAgain asks once more about the same build", async () => {
        const { result } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));

        act(() => result.current.checkAgain());

        await waitFor(() => expect(dryRunWire).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
    });

    test("a network error is unavailable, is not remembered, and checkAgain asks again", async () => {
        dryRunWire.mockRejectedValueOnce(new Error("Failed to fetch"));
        const { result } = mount();
        await waitFor(() => expect(result.current.check).toEqual({ kind: "unavailable", message: "Failed to fetch" }));

        act(() => result.current.checkAgain());

        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        expect(dryRunWire).toHaveBeenCalledTimes(2);
    });

    test("a config that cannot be read is unavailable and nothing is posted", async () => {
        contents = {};
        const { result } = mount();

        await waitFor(() => expect(result.current.check.kind).toBe("unavailable"));
        expect(result.current.check).toMatchObject({ message: expect.stringContaining("kept.yaml") });
        expect(result.current.key).toBeNull();
        expect(dryRunWire).not.toHaveBeenCalled();
    });

    test("drops the answer to a question the build has moved on from", async () => {
        let answerStale!: (response: ApplyBuildResponse) => void;
        dryRunWire.mockImplementationOnce(() => new Promise<ApplyBuildResponse>(resolve => (answerStale = resolve)));
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("checking"));

        rerender({ enabled: false });
        await act(async () => answerStale(refused("about the old build")));

        expect(result.current.check).toEqual({ kind: "idle" });
        rerender({ enabled: true });
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
    });

    test("an input change shows checking at once, not the answer to the inputs before it", async () => {
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));

        let answerNew!: (response: ApplyBuildResponse) => void;
        dryRunWire.mockImplementationOnce(() => new Promise<ApplyBuildResponse>(resolve => (answerNew = resolve)));
        rerender({ components: [agent("kept"), agent("added")] });

        expect(result.current.check).toEqual({ kind: "checking" });
        expect(result.current.key).toBeNull();
        contents = { ...contents, "added.yaml": "apps:\n  - name: added\n" };
        await waitFor(() => expect(dryRunWire).toHaveBeenCalledTimes(2));
        expect(result.current.check).toEqual({ kind: "checking" });
        await act(async () => answerNew(passed()));
        expect(result.current.check.kind).toBe("passed");
    });

    test("a session switch shows checking at once too", async () => {
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));

        rerender({ sessionId: "session-b" });

        expect(result.current.check).toEqual({ kind: "checking" });
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
    });

    test("checkAgain shows checking at once", async () => {
        dryRunWire.mockResolvedValue(refused("model not allowed"));
        const { result } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("refused"));

        dryRunWire.mockResolvedValue(passed());
        act(() => result.current.checkAgain());

        expect(result.current.check).toEqual({ kind: "checking" });
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
    });

    test("a permission refusal is asked again when the user asks to check again", async () => {
        dryRunWire.mockResolvedValueOnce({
            dryRun: true,
            dropped: [],
            operations: [{ kind: "agent", name: "kept", action: "create", status: "failed", error: { message: "not allowed", httpStatus: 403, errorId: "err-2" } }],
            summary: { created: 0, updated: 0, unchanged: 0, failed: 1, skipped: 0 },
        });
        const { result } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("notAllowed"));

        act(() => result.current.checkAgain());

        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        expect(dryRunWire).toHaveBeenCalledTimes(2);
    });
});

describe("usePlatformCheck: cancelling and not asking again", () => {
    const signalsOfDryRuns = () => vi.mocked(api.platform.post).mock.calls.map(call => (call[2] as { signal?: AbortSignal } | undefined)?.signal);

    test("a dry run is sent with a signal, and the signal is aborted when the check is abandoned", async () => {
        dryRunWire.mockReturnValue(new Promise(() => {}));
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("checking"));
        const [signal] = signalsOfDryRuns();
        expect(signal).toBeInstanceOf(AbortSignal);
        expect(signal?.aborted).toBe(false);

        rerender({ enabled: false });

        expect(signal?.aborted).toBe(true);
    });

    test("a dry run is aborted when the hook unmounts", async () => {
        dryRunWire.mockReturnValue(new Promise(() => {}));
        const { result, unmount } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("checking"));
        const [signal] = signalsOfDryRuns();

        unmount();

        expect(signal?.aborted).toBe(true);
    });

    test("a dry run is aborted when a turn starts, and the next one is sent with a fresh signal", async () => {
        dryRunWire.mockReturnValueOnce(new Promise(() => {})).mockResolvedValue(passed());
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("checking"));
        const [first] = signalsOfDryRuns();

        rerender({ idle: false });
        expect(first?.aborted).toBe(true);
        rerender({ idle: true });

        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        const [, second] = signalsOfDryRuns();
        expect(second).not.toBe(first);
        expect(second?.aborted).toBe(false);
    });

    test("a permission refusal is remembered: it is not asked again after another turn, and checkAgain asks anyway", async () => {
        dryRunWire.mockResolvedValue(notAllowedWire());
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.check.kind).toBe("notAllowed"));

        rerender({ idle: false });
        rerender({ idle: true });
        rerender({ idle: false });
        rerender({ idle: true });
        await act(async () => {});

        expect(dryRunWire).toHaveBeenCalledTimes(1);
        expect(result.current.check.kind).toBe("notAllowed");

        dryRunWire.mockResolvedValue(passed());
        act(() => result.current.checkAgain());
        await waitFor(() => expect(result.current.check.kind).toBe("passed"));
        expect(dryRunWire).toHaveBeenCalledTimes(2);
    });

    describe("when the platform cannot answer", () => {
        beforeEach(() => {
            vi.useFakeTimers();
        });
        afterEach(() => {
            vi.useRealTimers();
        });

        const flush = () => act(async () => vi.advanceTimersByTimeAsync(0));
        const advance = (ms: number) => act(async () => vi.advanceTimersByTimeAsync(ms));

        test("another turn does not ask again, and the question is retried after a delay that doubles up to a cap", async () => {
            dryRunWire.mockRejectedValue(new Error("Failed to fetch"));
            const { result, rerender } = mount();
            await flush();
            expect(result.current.check.kind).toBe("unavailable");
            expect(dryRunWire).toHaveBeenCalledTimes(1);

            rerender({ idle: false });
            rerender({ idle: true });
            await flush();
            expect(dryRunWire).toHaveBeenCalledTimes(1);
            expect(result.current.check.kind).toBe("unavailable");

            const STEP_MS = 1000;
            const untilNextAsk = async () => {
                const before = dryRunWire.mock.calls.length;
                let elapsed = 0;
                while (dryRunWire.mock.calls.length === before && elapsed < UNAVAILABLE_RETRY_MAX_MS * 2) {
                    await advance(STEP_MS);
                    elapsed += STEP_MS;
                }
                return elapsed;
            };
            const waits: number[] = [];
            for (let i = 0; i < 7; i++) waits.push(await untilNextAsk());

            expect(waits).toEqual([UNAVAILABLE_RETRY_BASE_MS, 2 * UNAVAILABLE_RETRY_BASE_MS, 4 * UNAVAILABLE_RETRY_BASE_MS, 8 * UNAVAILABLE_RETRY_BASE_MS, 16 * UNAVAILABLE_RETRY_BASE_MS, UNAVAILABLE_RETRY_MAX_MS, UNAVAILABLE_RETRY_MAX_MS]);
        });

        test("checkAgain asks at once, and a retry that succeeds ends the wait", async () => {
            dryRunWire.mockRejectedValueOnce(new Error("Failed to fetch")).mockResolvedValue(passed());
            const { result } = mount();
            await flush();
            expect(result.current.check.kind).toBe("unavailable");

            act(() => result.current.checkAgain());
            await flush();

            expect(dryRunWire).toHaveBeenCalledTimes(2);
            expect(result.current.check.kind).toBe("passed");
            await advance(UNAVAILABLE_RETRY_MAX_MS * 2);
            expect(dryRunWire).toHaveBeenCalledTimes(2);
        });

        test("no retry is scheduled while a turn runs, and a delay that has passed asks as soon as the turn ends", async () => {
            dryRunWire.mockRejectedValueOnce(new Error("Failed to fetch")).mockResolvedValue(passed());
            const { rerender } = mount();
            await flush();

            rerender({ idle: false });
            await advance(UNAVAILABLE_RETRY_BASE_MS * 2);
            expect(dryRunWire).toHaveBeenCalledTimes(1);

            rerender({ idle: true });
            await flush();
            expect(dryRunWire).toHaveBeenCalledTimes(2);
        });
    });
});
