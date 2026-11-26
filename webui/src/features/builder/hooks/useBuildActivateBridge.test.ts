// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { BuildActivateApi } from "../BuilderActions";
import type { PlatformCheck } from "../platformCheck";

import { useBuildActivateBridge } from "./useBuildActivateBridge";

function api(overrides: Partial<BuildActivateApi> = {}): BuildActivateApi {
    return {
        canBuildActivate: true,
        platformCheck: { kind: "idle" },
        checkAgain: vi.fn(),
        canAskBuilder: false,
        askBuilder: vi.fn(),
        turnRunning: false,
        isBusy: false,
        run: vi.fn().mockResolvedValue(undefined),
        failure: { kind: "none" },
        retry: vi.fn(),
        ...overrides,
    };
}

describe("useBuildActivateBridge", () => {
    test("starts idle", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        expect(result.current.buildActivate).toEqual({ can: false, busy: false });
        expect(result.current.deployFailure).toBeNull();
    });

    test("reflects the published API's can/busy flags", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ canBuildActivate: true, isBusy: false })));
        expect(result.current.buildActivate).toEqual({ can: true, busy: false });
        act(() => result.current.handleBuildActivateApiChange(api({ canBuildActivate: false, isBusy: true })));
        expect(result.current.buildActivate).toEqual({ can: false, busy: true });
    });

    test("clears flags when the API is removed (BuilderActions unmount)", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ canBuildActivate: true, isBusy: true })));
        act(() => result.current.handleBuildActivateApiChange(null));
        expect(result.current.buildActivate).toEqual({ can: false, busy: false });
    });

    test("keeps the same state object when can/busy are unchanged", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ canBuildActivate: true, isBusy: false })));
        const first = result.current.buildActivate;
        act(() => result.current.handleBuildActivateApiChange(api({ canBuildActivate: true, isBusy: false })));
        expect(result.current.buildActivate).toBe(first);
    });

    test("the platform check view says whether a turn is running, and keeps its identity while that is unchanged", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ turnRunning: true })));
        expect(result.current.platformCheck.turnRunning).toBe(true);
        const first = result.current.platformCheck;

        act(() => result.current.handleBuildActivateApiChange(api({ turnRunning: true })));
        expect(result.current.platformCheck).toBe(first);

        act(() => result.current.handleBuildActivateApiChange(api({ turnRunning: false })));
        expect(result.current.platformCheck.turnRunning).toBe(false);
    });

    test("handleBuildActivate runs the latest published run", () => {
        const runA = vi.fn().mockResolvedValue(undefined);
        const runB = vi.fn().mockResolvedValue(undefined);
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ run: runA })));
        act(() => result.current.handleBuildActivateApiChange(api({ run: runB })));
        act(() => result.current.handleBuildActivate());
        expect(runB).toHaveBeenCalledTimes(1);
        expect(runA).not.toHaveBeenCalled();
    });

    test("handleBuildActivate is a no-op when nothing is published", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        expect(() => result.current.handleBuildActivate()).not.toThrow();
    });

    test("swallows a rejected run without throwing", async () => {
        const run = vi.fn().mockRejectedValue(new Error("deploy boom"));
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ run })));
        await act(async () => {
            result.current.handleBuildActivate();
            await Promise.resolve();
        });
        expect(run).toHaveBeenCalledTimes(1);
    });

    test("the callbacks keep a stable identity across renders", () => {
        const { result, rerender } = renderHook(() => useBuildActivateBridge());
        const onChange = result.current.handleBuildActivateApiChange;
        const onRun = result.current.handleBuildActivate;
        const onRedeploy = result.current.handleRedeploy;
        rerender();
        expect(result.current.handleBuildActivateApiChange).toBe(onChange);
        expect(result.current.handleBuildActivate).toBe(onRun);
        expect(result.current.handleRedeploy).toBe(onRedeploy);
    });

    test("maps a retryable failure to canRetry:true", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "retryable", message: "deploy failed: 500" } })));
        expect(result.current.deployFailure).toEqual({ message: "deploy failed: 500", canRetry: true });
    });

    test("maps a fixing failure to canRetry:false (agent owns it)", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "fixing", message: "bad yaml" } })));
        expect(result.current.deployFailure).toEqual({ message: "bad yaml", canRetry: false });
    });

    test("maps a blocked failure to canRetry:false, but still reports it", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "blocked", message: "plan unreadable" } })));
        expect(result.current.deployFailure).toEqual({ message: "plan unreadable", canRetry: false });
    });

    test("maps a declined save to a card that is marked declined and offers Try again", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "declined", message: "Save cancelled. Try again to review and save." } })));
        expect(result.current.deployFailure).toEqual({ message: "Save cancelled. Try again to review and save.", canRetry: true, declined: true });
    });

    test("a failure replaced by a decline with the same text publishes the new state", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "retryable", message: "same" } })));
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "declined", message: "same" } })));
        expect(result.current.deployFailure).toEqual({ message: "same", canRetry: true, declined: true });
    });

    test("clears the failure when it resolves to none", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "retryable", message: "boom" } })));
        expect(result.current.deployFailure).not.toBeNull();
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "none" } })));
        expect(result.current.deployFailure).toBeNull();
    });

    test("handleRedeploy runs the latest published retry", () => {
        const retry = vi.fn();
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ failure: { kind: "retryable", message: "boom" }, retry })));
        act(() => result.current.handleRedeploy());
        expect(retry).toHaveBeenCalledTimes(1);
    });

    test("starts with no platform check", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        expect(result.current.platformCheck).toEqual({ check: { kind: "idle" }, canAskBuilder: false, turnRunning: false });
    });

    test("reflects the published platform check and whether the Builder can take it", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        const refused: PlatformCheck = { kind: "refused", problems: [{ kind: "agent", name: "A", message: "no" }], forbidden: [] };
        act(() => result.current.handleBuildActivateApiChange(api({ platformCheck: refused, canAskBuilder: true })));
        expect(result.current.platformCheck).toEqual({ check: refused, canAskBuilder: true, turnRunning: false });
    });

    test("keeps the same platform check state when the same check is published again as a new object", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        const refused = (): PlatformCheck => ({ kind: "refused", problems: [{ kind: "agent", name: "A", message: "no" }], forbidden: [] });
        act(() => result.current.handleBuildActivateApiChange(api({ platformCheck: refused() })));
        const first = result.current.platformCheck;
        act(() => result.current.handleBuildActivateApiChange(api({ platformCheck: refused() })));
        expect(result.current.platformCheck).toBe(first);
        const changed: PlatformCheck = { kind: "refused", problems: [{ kind: "agent", name: "A", message: "different" }], forbidden: [] };
        act(() => result.current.handleBuildActivateApiChange(api({ platformCheck: changed })));
        expect(result.current.platformCheck).not.toBe(first);
    });

    test("clears the platform check when the API is removed", () => {
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ platformCheck: { kind: "checking" } })));
        act(() => result.current.handleBuildActivateApiChange(null));
        expect(result.current.platformCheck.check).toEqual({ kind: "idle" });
    });

    test("handleCheckAgain and handleAskBuilder run the latest published callbacks", () => {
        const checkAgain = vi.fn();
        const askBuilder = vi.fn();
        const { result } = renderHook(() => useBuildActivateBridge());
        act(() => result.current.handleBuildActivateApiChange(api({ checkAgain, askBuilder })));
        act(() => result.current.handleCheckAgain());
        act(() => result.current.handleAskBuilder());
        expect(checkAgain).toHaveBeenCalledTimes(1);
        expect(askBuilder).toHaveBeenCalledTimes(1);
    });
});
