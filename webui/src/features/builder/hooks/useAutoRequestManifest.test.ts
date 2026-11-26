// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { finishBuildRequest, rewriteManifestRequest } from "./configsWithoutManifest";
import { useAutoRequestManifest } from "./useAutoRequestManifest";
import type { BuilderManifestState, BuildManifest } from "./useBuilderManifest";

const handleSubmit = vi.fn().mockResolvedValue(undefined);
const addNotification = vi.fn();
let isResponding = false;
let sessionId = "session-1";
let isCancelling = false;
let selectedAgentName = "Builder";

vi.mock("@/providers/contexts", () => ({
    useChatContext: () => ({ isResponding, handleSubmit, sessionId, isCancelling, selectedAgentName, addNotification }),
}));

const A_MANIFEST = { name: "b" } as unknown as BuildManifest;

function manifestState(manifest: BuildManifest | null, overrides: Partial<BuilderManifestState> = {}): BuilderManifestState {
    return { manifest, isLoading: false, isSettled: true, isUnreadable: false, saveManifest: vi.fn(), ...overrides };
}

type Props = { count: number; m: BuilderManifestState };

const STUCK: Props = { count: 1, m: manifestState(null) };

function render(initialProps: Props = STUCK) {
    return renderHook(({ count, m }: Props) => useAutoRequestManifest(count, m), { initialProps });
}

function runTurn(rerender: (props: Props) => void, props: Props) {
    isResponding = true;
    act(() => rerender(props));
    isResponding = false;
    act(() => rerender(props));
}

beforeEach(() => {
    handleSubmit.mockClear();
    addNotification.mockClear();
    isResponding = false;
    sessionId = "session-1";
    isCancelling = false;
    selectedAgentName = "Builder";
    vi.useFakeTimers();
});

afterEach(() => {
    vi.useRealTimers();
});

describe("useAutoRequestManifest", () => {
    test("asks the agent once when a turn ends with configs but no manifest", () => {
        const { result, rerender } = render();

        runTurn(rerender, STUCK);

        expect(handleSubmit).toHaveBeenCalledTimes(1);
        expect(handleSubmit.mock.calls[0][2]).toBe(finishBuildRequest(1));
        expect(handleSubmit.mock.calls[0][8]).toEqual({ hidden: true });
        expect(result.current.stuck).toBe(false);
    });

    test("reports the build as stuck when the request did not produce a manifest", () => {
        const { result, rerender } = render();

        runTurn(rerender, STUCK);
        runTurn(rerender, STUCK);

        expect(handleSubmit).toHaveBeenCalledTimes(1);
        expect(result.current.stuck).toBe(true);
    });

    test("reports the build as stuck when the request produced no turn at all", () => {
        const { result, rerender } = render();

        runTurn(rerender, STUCK);
        expect(result.current.stuck).toBe(false);

        act(() => {
            vi.advanceTimersByTime(20_000);
        });
        act(() => rerender(STUCK));

        expect(result.current.stuck).toBe(true);
    });

    test("stops reporting once the manifest arrives", () => {
        const arrived: Props = { count: 1, m: manifestState(A_MANIFEST) };
        const { result, rerender } = render();

        runTurn(rerender, STUCK);
        runTurn(rerender, STUCK);
        expect(result.current.stuck).toBe(true);

        act(() => rerender(arrived));

        expect(result.current.stuck).toBe(false);
    });

    test("does not carry one session's report into the next", () => {
        const { result, rerender } = render();

        runTurn(rerender, STUCK);
        runTurn(rerender, STUCK);
        expect(result.current.stuck).toBe(true);

        sessionId = "session-2";
        act(() => rerender(STUCK));

        expect(result.current.stuck).toBe(false);
    });

    test("does not ask for a manifest the artifact list merely cannot see", () => {
        const present: Props = { count: 2, m: manifestState(A_MANIFEST) };
        const { result, rerender } = render(present);

        runTurn(rerender, present);

        expect(handleSubmit).not.toHaveBeenCalled();
        expect(result.current.stuck).toBe(false);
    });

    test("does not accuse the agent when the manifest was read but would not parse", () => {
        const malformed: Props = { count: 1, m: manifestState(null, { isUnreadable: true }) };
        const { result, rerender } = render(malformed);

        runTurn(rerender, malformed);
        runTurn(rerender, malformed);

        expect(handleSubmit).not.toHaveBeenCalled();
        expect(result.current.stuck).toBe(false);
    });

    test("waits for the manifest lookup to settle before asking", () => {
        const unsettled: Props = { count: 1, m: manifestState(null, { isSettled: false }) };
        const { result, rerender } = render(unsettled);

        runTurn(rerender, unsettled);

        expect(handleSubmit).not.toHaveBeenCalled();
        expect(result.current.stuck).toBe(false);
    });

    test("still asks when the condition settles after the turn ended", () => {
        const unsettled: Props = { count: 1, m: manifestState(null, { isSettled: false }) };
        const { rerender } = render(unsettled);

        runTurn(rerender, unsettled);
        expect(handleSubmit).not.toHaveBeenCalled();

        act(() => rerender(STUCK));

        expect(handleSubmit).toHaveBeenCalledTimes(1);
    });

    test("asks for nothing until a turn has completed", () => {
        const { result, rerender } = render();

        act(() => rerender(STUCK));
        act(() => {
            vi.advanceTimersByTime(20_000);
        });

        expect(handleSubmit).not.toHaveBeenCalled();
        expect(result.current.stuck).toBe(false);
    });

    test("gives a new session its own silent attempt", () => {
        const { rerender } = render();

        runTurn(rerender, STUCK);
        sessionId = "session-2";
        runTurn(rerender, STUCK);

        expect(handleSubmit).toHaveBeenCalledTimes(2);
    });

    test("does not treat a switch away from a mid-turn session as a completed turn", () => {
        const { rerender } = render();

        isResponding = true;
        act(() => rerender(STUCK));

        sessionId = "session-2";
        isResponding = false;
        act(() => rerender(STUCK));

        expect(handleSubmit).not.toHaveBeenCalled();
    });

    test("survives its own session being renamed mid-turn", () => {
        const { rerender } = render();

        isResponding = true;
        act(() => rerender(STUCK));
        sessionId = "server-assigned";
        act(() => rerender(STUCK));
        isResponding = false;
        act(() => rerender(STUCK));

        expect(handleSubmit).toHaveBeenCalledTimes(1);
    });

    test("does not let a completed turn carry into the next session", () => {
        const empty: Props = { count: 0, m: manifestState(null) };
        const { rerender } = render();

        runTurn(rerender, STUCK);
        handleSubmit.mockClear();

        sessionId = "session-2";
        act(() => rerender(empty));
        act(() => rerender(STUCK));

        expect(handleSubmit).not.toHaveBeenCalled();
    });

    test("requestAgain sends the same request, visibly", () => {
        const { result } = render({ count: 3, m: manifestState(null) });

        act(() => result.current.requestAgain());

        expect(handleSubmit).toHaveBeenCalledTimes(1);
        expect(handleSubmit.mock.calls[0][2]).toBe(finishBuildRequest(3));
        expect(handleSubmit.mock.calls[0][8]).toBeUndefined();
    });

    test("requestAgain says so when the chat cannot accept the request", () => {
        isCancelling = true;
        const { result } = render();

        act(() => result.current.requestAgain());

        expect(handleSubmit).not.toHaveBeenCalled();
        expect(addNotification).toHaveBeenCalledTimes(1);
    });

    test("reports a manifest that could not be read, separately from a missing one", () => {
        const malformed: Props = { count: 1, m: manifestState(null, { isUnreadable: true }) };
        const { result } = render(malformed);

        expect(result.current.unreadable).toBe(true);
        expect(result.current.stuck).toBe(false);
    });

    test("says nothing about readability while the agent is mid-turn", () => {
        isResponding = true;
        const { result } = render({ count: 1, m: manifestState(null, { isUnreadable: true }) });

        expect(result.current.unreadable).toBe(false);
    });

    test("asks for a repair, not a rewrite, when the manifest is merely unreadable", () => {
        const { result } = render({ count: 0, m: manifestState(null, { isUnreadable: true }) });

        act(() => result.current.requestAgain());

        const sent = handleSubmit.mock.calls[0][2] as string;
        expect(sent).toBe(rewriteManifestRequest(false));
        expect(sent).toMatch(/exists in this session but could not be parsed/);
        expect(sent).toMatch(/repair the invalid YAML in place/);
        expect(sent).not.toMatch(/component configs? but no/);
    });

    test("does not tell the agent the canvas is empty when a manifest is on screen", () => {
        const onScreen = { name: "sys", components: [] } as unknown as BuildManifest;
        const { result } = render({ count: 0, m: manifestState(onScreen, { isUnreadable: true }) });

        act(() => result.current.requestAgain());

        const sent = handleSubmit.mock.calls[0][2] as string;
        expect(sent).not.toMatch(/canvas is empty/);
        expect(sent).toMatch(/older version/);
        expect(sent).toMatch(/repair the invalid YAML in place/);
    });

    test("requestAgain does not re-arm the silent attempt", () => {
        const { result, rerender } = render();

        runTurn(rerender, STUCK);
        act(() => result.current.requestAgain());
        handleSubmit.mockClear();

        runTurn(rerender, STUCK);

        expect(handleSubmit).not.toHaveBeenCalled();
        expect(result.current.stuck).toBe(true);
    });
});
