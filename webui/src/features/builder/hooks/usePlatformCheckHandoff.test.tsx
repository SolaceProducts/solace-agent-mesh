// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { PlatformCheck, PlatformProblem } from "../platformCheck";
import { PLATFORM_CHECK_CHIP, platformCheckMessage } from "../platformCheck";

import { type AutoFixEvent, type AutoFixOutcome, initialAutoFixState } from "./autoFixMachine";
import { usePlatformCheckHandoff, type UsePlatformCheckHandoffArgs } from "./usePlatformCheckHandoff";

const problem: PlatformProblem = { kind: "agent", name: "kept", message: "model alias is unknown" };
const forbidden: PlatformProblem = { kind: "connector", name: "locked", message: "not allowed to create connectors" };
const refused = (problems: PlatformProblem[] = [problem]): PlatformCheck => ({ kind: "refused", problems, forbidden: [forbidden] });
const passed: PlatformCheck = { kind: "passed", warnings: [] };

const dispatchAutoFix = vi.fn<(event: AutoFixEvent) => AutoFixOutcome>();
const postToBuilder = vi.fn();

function argsOf(over: Partial<UsePlatformCheckHandoffArgs> = {}): UsePlatformCheckHandoffArgs {
    return { check: refused(), key: "K1", idle: true, postable: true, sessionId: "session-a", agentResponded: true, reportHeld: false, autoFixExhausted: false, dispatchAutoFix, postToBuilder, ...over };
}

function mount(initial: Partial<UsePlatformCheckHandoffArgs> = {}) {
    return renderHook((over: Partial<UsePlatformCheckHandoffArgs>) => usePlatformCheckHandoff(argsOf(over)), { initialProps: initial });
}

const dispatched = (type: AutoFixEvent["type"]) => dispatchAutoFix.mock.calls.map(([event]) => event).filter(event => event.type === type);

beforeEach(() => {
    vi.clearAllMocks();
    dispatchAutoFix.mockReturnValue({ state: initialAutoFixState, accepted: true });
});

describe("usePlatformCheckHandoff: reporting a refusal", () => {
    test("a refusal for a new key is reported once, with the platform's problems only", () => {
        const { rerender } = mount();

        expect(dispatchAutoFix).toHaveBeenCalledTimes(1);
        expect(dispatchAutoFix).toHaveBeenCalledWith({
            type: "report",
            fix: { message: platformCheckMessage([problem]), displayChip: PLATFORM_CHECK_CHIP, key: "platform-check|K1", sessionId: "session-a", errorText: platformCheckMessage([problem]) },
            responding: false,
        });
        expect(platformCheckMessage([problem])).not.toContain(forbidden.message);

        rerender({ check: refused() });
        rerender({ idle: true });
        expect(dispatchAutoFix).toHaveBeenCalledTimes(1);
    });

    test("a refusal of the whole request, which names no component, is reported too", () => {
        const wholeRequest = { kind: null, name: null, message: "build name is required" };

        mount({ check: refused([wholeRequest]) });

        expect(dispatched("report")).toEqual([expect.objectContaining({ fix: expect.objectContaining({ message: platformCheckMessage([wholeRequest]) }) })]);
    });

    test.each<[string, PlatformCheck]>([
        ["not allowed", { kind: "notAllowed", problems: [forbidden] }],
        ["unavailable", { kind: "unavailable", message: "the platform is down" }],
        ["passed", passed],
        ["checking", { kind: "checking" }],
        ["idle", { kind: "idle" }],
    ])("%s reports nothing", (_label, check) => {
        mount({ check });

        expect(dispatchAutoFix).not.toHaveBeenCalled();
    });

    test("a refusal that arrives while a turn runs waits for the turn to end, then is reported once", () => {
        const { rerender } = mount({ idle: false });
        expect(dispatchAutoFix).not.toHaveBeenCalled();

        rerender({ idle: true });
        rerender({ idle: false });
        rerender({ idle: true });

        expect(dispatched("report")).toHaveLength(1);
    });

    test("a different refusal is reported on its own", () => {
        const { rerender } = mount();

        rerender({ check: refused([{ ...problem, message: "another" }]), key: "K2" });

        expect(dispatched("report").map(event => (event as Extract<AutoFixEvent, { type: "report" }>).fix.key)).toEqual(["platform-check|K1", "platform-check|K2"]);
    });
});

describe("usePlatformCheckHandoff: a cold open", () => {
    test("a refusal found before any Builder turn ended in this mount is shown, not sent, and the ask is offered", () => {
        const { result } = mount({ agentResponded: false });

        expect(dispatchAutoFix).not.toHaveBeenCalled();
        expect(result.current.canAskBuilder).toBe(true);
    });

    test("the ask posts the message once, visibly", () => {
        const { result } = mount({ agentResponded: false });

        act(() => result.current.askBuilder());

        expect(postToBuilder).toHaveBeenCalledWith(platformCheckMessage([problem]), PLATFORM_CHECK_CHIP);
        expect(dispatchAutoFix).not.toHaveBeenCalled();
    });

    test("a refusal that is still there once a turn has ended in this mount is reported once, and the ask goes away", () => {
        const { result, rerender } = mount({ agentResponded: false });

        rerender({ agentResponded: true });
        rerender({ agentResponded: true });

        expect(dispatched("report")).toHaveLength(1);
        expect(result.current.canAskBuilder).toBe(false);
    });

    test("a refusal the user asked about is not reported again when a turn ends without changing the build", () => {
        const { result, rerender } = mount({ agentResponded: false });
        act(() => result.current.askBuilder());

        rerender({ agentResponded: true });
        rerender({ agentResponded: true, idle: false });
        rerender({ agentResponded: true, idle: true });

        expect(dispatchAutoFix).not.toHaveBeenCalled();
        expect(postToBuilder).toHaveBeenCalledTimes(1);
    });

    test("a different refusal after a turn ended is reported on its own", () => {
        const { rerender } = mount({ agentResponded: true });

        rerender({ check: refused([{ ...problem, message: "another" }]), key: "K2", agentResponded: true });

        expect(dispatched("report")).toHaveLength(2);
    });
});

describe("usePlatformCheckHandoff: a refusal another report already covers", () => {
    test("nothing is reported while a fix is in flight or the save has failed, and it is reported when that ends", () => {
        const { rerender } = mount({ reportHeld: true });
        expect(dispatchAutoFix).not.toHaveBeenCalled();

        rerender({ reportHeld: false });

        expect(dispatched("report")).toHaveLength(1);
    });
});

describe("usePlatformCheckHandoff: asking the Builder by hand", () => {
    test("a refusal reported and sent does not offer the ask", () => {
        const { result } = mount();

        expect(result.current.canAskBuilder).toBe(false);
    });

    test("once the budget is spent the ask is offered, and posts the message once, visibly", () => {
        const { result, rerender } = mount({ autoFixExhausted: true });
        expect(result.current.canAskBuilder).toBe(true);

        act(() => result.current.askBuilder());
        act(() => result.current.askBuilder());
        rerender({ autoFixExhausted: true });

        expect(postToBuilder).toHaveBeenCalledTimes(1);
        expect(postToBuilder).toHaveBeenCalledWith(platformCheckMessage([problem]), PLATFORM_CHECK_CHIP);
        expect(result.current.canAskBuilder).toBe(false);
    });

    test("asking during a turn posts nothing and marks nothing, and the ask works once the turn has ended", () => {
        const { result, rerender } = mount({ agentResponded: false });
        rerender({ agentResponded: false, idle: false });

        act(() => result.current.askBuilder());

        expect(postToBuilder).not.toHaveBeenCalled();
        expect(result.current.canAskBuilder).toBe(true);

        rerender({ agentResponded: false, idle: true });
        act(() => result.current.askBuilder());

        expect(postToBuilder).toHaveBeenCalledTimes(1);
        expect(result.current.canAskBuilder).toBe(false);
    });

    test("a refusal asked about during a turn is still reported by itself once the turn ends", () => {
        const { result, rerender } = mount({ agentResponded: false });
        rerender({ agentResponded: false, idle: false });
        act(() => result.current.askBuilder());

        rerender({ agentResponded: true, idle: true });

        expect(dispatched("report")).toHaveLength(1);
    });

    test("a post the chat would drop is not made and marks nothing, and the ask works once it can be taken", () => {
        const { result, rerender } = mount({ agentResponded: false, postable: false });

        act(() => result.current.askBuilder());

        expect(postToBuilder).not.toHaveBeenCalled();
        expect(result.current.canAskBuilder).toBe(true);

        rerender({ agentResponded: false, postable: true });
        act(() => result.current.askBuilder());

        expect(postToBuilder).toHaveBeenCalledTimes(1);
        expect(result.current.canAskBuilder).toBe(false);
    });

    test("a refusal whose post was dropped is still reported by itself once a turn ends", () => {
        const { result, rerender } = mount({ agentResponded: false, postable: false });
        act(() => result.current.askBuilder());

        rerender({ agentResponded: true, postable: true });

        expect(dispatched("report")).toHaveLength(1);
    });

    test("the key is marked only after the post went out", () => {
        postToBuilder.mockImplementationOnce(() => {
            throw new Error("not sent");
        });
        const { result } = mount({ agentResponded: false });

        expect(() => act(() => result.current.askBuilder())).toThrow("not sent");
        expect(result.current.canAskBuilder).toBe(true);

        act(() => result.current.askBuilder());
        expect(postToBuilder).toHaveBeenCalledTimes(2);
    });

    test("a report the machine did not take offers the ask", () => {
        dispatchAutoFix.mockReturnValue({ state: initialAutoFixState, accepted: false });

        const { result } = mount();

        expect(result.current.canAskBuilder).toBe(true);
    });

    test("an ask is not offered without a refusal", () => {
        const { result } = mount({ check: passed, autoFixExhausted: true });

        expect(result.current.canAskBuilder).toBe(false);
        act(() => result.current.askBuilder());
        expect(postToBuilder).not.toHaveBeenCalled();
    });

    test("the next refusal can be asked about again", () => {
        const { result, rerender } = mount({ autoFixExhausted: true });
        act(() => result.current.askBuilder());

        rerender({ check: refused([{ ...problem, message: "another" }]), key: "K2", autoFixExhausted: true });

        expect(result.current.canAskBuilder).toBe(true);
    });
});

describe("usePlatformCheckHandoff: a refusal fixed", () => {
    test("a refill the machine did not take is tried again when the fix in flight ends", () => {
        dispatchAutoFix.mockImplementation(event => ({ state: initialAutoFixState, accepted: event.type === "refusalFixed" ? false : true }));
        const { rerender } = mount({ reportHeld: false });
        rerender({ check: passed, key: "K2", reportHeld: true });
        expect(dispatched("refusalFixed")).toHaveLength(1);

        dispatchAutoFix.mockReturnValue({ state: initialAutoFixState, accepted: true });
        rerender({ check: passed, key: "K2", reportHeld: false });

        expect(dispatched("refusalFixed")).toHaveLength(2);
        rerender({ check: passed, key: "K2", reportHeld: true });
        rerender({ check: passed, key: "K2", reportHeld: false });
        expect(dispatched("refusalFixed")).toHaveLength(2);
    });

    test("a pass for another key after a refusal refills the budget once", () => {
        const { rerender } = mount();

        rerender({ check: passed, key: "K2" });
        rerender({ check: passed, key: "K2" });
        rerender({ check: { kind: "checking" }, key: "K2" });
        rerender({ check: passed, key: "K2" });

        expect(dispatched("refusalFixed")).toHaveLength(1);
    });

    test("a pass with no refusal before it refills nothing", () => {
        const { rerender } = mount({ check: passed, key: "K1" });

        rerender({ check: passed, key: "K2" });

        expect(dispatched("refusalFixed")).toHaveLength(0);
    });

    test("a pass for the very key that was refused refills nothing", () => {
        const { rerender } = mount();

        rerender({ check: passed, key: "K1" });

        expect(dispatched("refusalFixed")).toHaveLength(0);
    });

    test("a refusal seen while a turn runs still counts as the one that was fixed", () => {
        const { rerender } = mount({ idle: false });

        rerender({ check: passed, key: "K2", idle: true });

        expect(dispatched("refusalFixed")).toHaveLength(1);
        expect(dispatched("report")).toHaveLength(0);
    });

    test("a refusal that comes back after it was fixed is reported again, and the machine decides whether to send it", () => {
        const { rerender } = mount();
        rerender({ check: passed, key: "K2" });

        rerender({ check: refused(), key: "K1" });

        expect(dispatched("report")).toHaveLength(2);
    });

    test("a new session forgets what it reported and what it saw refused", () => {
        const { rerender } = mount();

        rerender({ sessionId: "session-b" });
        expect(dispatched("report")).toHaveLength(2);

        rerender({ check: passed, key: "K2", sessionId: "session-c" });
        expect(dispatched("refusalFixed")).toHaveLength(0);
    });
});
