// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { MessageFE } from "@/types";

import { useTurnEndedAbnormally } from "./useTurnEndedAbnormally";

const user = { isUser: true, parts: [], metadata: { messageId: "u" } } as unknown as MessageFE;
const agent = (over: Record<string, unknown> = {}) => ({ isUser: false, isComplete: true, parts: [], metadata: { messageId: "a" }, ...over }) as unknown as MessageFE;

type Props = { messages: MessageFE[]; isResponding: boolean; isCancelling: boolean; currentTaskId: string | null; sessionId: string; endedAbnormallyOnRecord?: boolean };

const idle: Props = { messages: [user, agent()], isResponding: false, isCancelling: false, currentTaskId: null, sessionId: "s1" };
const running: Props = { ...idle, messages: [user], isResponding: true, currentTaskId: "t1" };

function setup(initial: Props) {
    return renderHook((p: Props) => useTurnEndedAbnormally(p), { initialProps: initial });
}

function stopTurn(rerender: (p: Props) => void, over: Partial<Props> = {}) {
    rerender({ ...running, isCancelling: true, ...over });
    rerender({ ...idle, ...over });
}

describe("useTurnEndedAbnormally: an error bubble", () => {
    test("a turn that completed normally did not end abnormally", () => {
        expect(setup(idle).result.current).toBe(false);
    });

    test("a turn that ended with an error bubble did", () => {
        expect(setup({ ...idle, messages: [user, agent({ isError: true })] }).result.current).toBe(true);
    });

    test("an error bubble followed only by the user's next message still counts", () => {
        expect(setup({ ...idle, messages: [user, agent({ isError: true }), user] }).result.current).toBe(true);
    });

    test("an old error bubble does not count once a later agent message follows it", () => {
        expect(setup({ ...idle, messages: [user, agent({ isError: true }), user, agent()] }).result.current).toBe(false);
    });
});

describe("useTurnEndedAbnormally: a Stop", () => {
    test("is remembered once the cancelled turn ends", () => {
        const { result, rerender } = setup(running);

        stopTurn(rerender);

        expect(result.current).toBe(true);
    });

    test("is not claimed while the turn is still running", () => {
        const { result, rerender } = setup(running);

        rerender({ ...running, isCancelling: true });

        expect(result.current).toBe(false);
    });

    test("is not claimed when the cancel failed and the turn then ended on its own", () => {
        const { result, rerender } = setup(running);

        rerender({ ...running, isCancelling: true });
        rerender({ ...running, isCancelling: false });
        rerender(idle);

        expect(result.current).toBe(false);
    });

    test("stays remembered when the next submission fails before any task starts", () => {
        const { result, rerender } = setup(running);
        stopTurn(rerender);

        rerender({ ...idle, isResponding: true, currentTaskId: null });
        rerender(idle);

        expect(result.current).toBe(true);
    });

    test("is dropped once a different task starts", () => {
        const { result, rerender } = setup(running);
        stopTurn(rerender);

        rerender({ ...running, messages: [user, agent(), user], currentTaskId: "t2" });
        rerender({ ...idle, currentTaskId: "t2", messages: [user, agent(), user, agent()] });

        expect(result.current).toBe(false);
    });

    test("is dropped when the session changes, so another session is not marked", () => {
        const { result, rerender } = setup(running);
        stopTurn(rerender);

        rerender({ ...idle, sessionId: "s2" });

        expect(result.current).toBe(false);
    });

    test("a Stop requested in one session is not claimed for another when the turn ends after the switch", () => {
        const { result, rerender } = setup(running);

        rerender({ ...running, isCancelling: true });
        rerender({ ...running, isCancelling: true, sessionId: "s2" });
        rerender({ ...idle, sessionId: "s2" });

        expect(result.current).toBe(false);
    });

    test("stays remembered while the same task id lingers after the cancel", () => {
        const { result, rerender } = setup(running);
        stopTurn(rerender);

        rerender({ ...idle, currentTaskId: "t1" });
        rerender({ ...idle, currentTaskId: "t1", messages: [user, agent(), agent()] });

        expect(result.current).toBe(true);
    });

    test("a Stop pressed before any task exists is not remembered", () => {
        const { result, rerender } = setup({ ...running, currentTaskId: null });

        rerender({ ...running, currentTaskId: null, isCancelling: true });
        rerender(idle);

        expect(result.current).toBe(false);
    });

    test("a cancel flag raised after the turn already ended, with the task id lingering, is not a Stop", () => {
        const { result, rerender } = setup({ ...idle, currentTaskId: "t1" });

        rerender({ ...idle, currentTaskId: "t1", isCancelling: true });
        rerender({ ...idle, currentTaskId: "t1" });

        expect(result.current).toBe(false);
    });

    test("a cancel that failed while the task id was cleared, then a normal end, is not a Stop", () => {
        const { result, rerender } = setup(running);

        rerender({ ...running, isCancelling: true });
        rerender({ ...running, isCancelling: false, currentTaskId: null });
        rerender(idle);

        expect(result.current).toBe(false);
    });

    test("a Stop stays recorded when the task id is cleared while the cancel is still pending", () => {
        const { result, rerender } = setup(running);

        rerender({ ...running, isCancelling: true });
        rerender({ ...running, isCancelling: true, currentTaskId: null });
        rerender({ ...idle, isCancelling: false });

        expect(result.current).toBe(true);
    });

    test("a Stop requested under the unadopted empty session id is dropped once the session is adopted", () => {
        const { result, rerender } = setup({ ...running, sessionId: "" });

        rerender({ ...running, sessionId: "", isCancelling: true });
        rerender({ ...running, sessionId: "s1", isCancelling: true });
        rerender({ ...idle, sessionId: "s1" });

        expect(result.current).toBe(false);
    });

    test("a session switch in the same render in which the cancelled turn ends does not mark the new session", () => {
        const { result, rerender } = setup(running);

        rerender({ ...running, isCancelling: true });
        rerender({ ...idle, sessionId: "s2" });

        expect(result.current).toBe(false);
    });

    test("a Stop that went through when the task id was already cleared is still remembered", () => {
        const { result, rerender } = setup(running);

        rerender({ ...running, isCancelling: true });
        rerender({ ...idle, currentTaskId: null });

        expect(result.current).toBe(true);
    });
});

test("no messages yet is not abnormal", () => {
    const { result } = renderHook(() => useTurnEndedAbnormally({ messages: undefined, isResponding: false, isCancelling: false, currentTaskId: null, sessionId: "s1" }));

    expect(result.current).toBe(false);
});

describe("useTurnEndedAbnormally: the gateway's record of how the latest task ended", () => {
    test.each([
        { name: "a plain message after a Stop that no longer lives in this page (a reload)", over: { endedAbnormallyOnRecord: true }, expected: true },
        { name: "a record that says the task ended normally", over: { endedAbnormallyOnRecord: false }, expected: false },
        { name: "no record", over: {}, expected: false },
        { name: "a record and an error bubble", over: { endedAbnormallyOnRecord: true, messages: [user, agent({ isError: true })] }, expected: true },
    ])("$name", ({ over, expected }) => {
        expect(setup({ ...idle, ...over }).result.current).toBe(expected);
    });

    test("is claimed on its own, with no Stop and no error in this page load", () => {
        const { result, rerender } = setup(idle);
        expect(result.current).toBe(false);

        rerender({ ...idle, endedAbnormallyOnRecord: true });

        expect(result.current).toBe(true);
    });

    test("is withdrawn when the record is, for example once the next turn starts", () => {
        const { result, rerender } = setup({ ...idle, endedAbnormallyOnRecord: true });

        rerender({ ...running, endedAbnormallyOnRecord: false });

        expect(result.current).toBe(false);
    });
});
