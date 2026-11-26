// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import type { MessageFE } from "@/types";

import { useChatDeployTrigger } from "./useChatDeployTrigger";

function agentMsg(id: string, opts: { signal?: boolean; complete?: boolean; user?: boolean } = {}): MessageFE {
    const parts = opts.signal ? [{ kind: "data", data: { type: "request_build_activate" } }] : [{ kind: "text", text: "hi" }];
    return {
        taskId: id,
        isComplete: opts.complete ?? true,
        isUser: opts.user ?? false,
        parts,
        metadata: { messageId: id },
    } as unknown as MessageFE;
}

const SID = "session-1";

describe("useChatDeployTrigger", () => {
    test("fires onBuildActivate once when a completed message carries the signal and the build is deploy-ready", () => {
        const onBuildActivate = vi.fn();
        renderHook(() => useChatDeployTrigger({ sessionId: SID, messages: [agentMsg("t1", { signal: true })], canBuildActivate: true, saveOnlyBuild: false, isBuildActivating: false, onBuildActivate }));
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
    });

    test("does NOT fire while the build isn't deploy-ready, then fires once it becomes ready (signal held, not dropped)", () => {
        const onBuildActivate = vi.fn();
        const messages = [agentMsg("t1", { signal: true })];
        const { rerender } = renderHook((props: { can: boolean }) => useChatDeployTrigger({ sessionId: SID, messages, canBuildActivate: props.can, saveOnlyBuild: false, isBuildActivating: false, onBuildActivate }), { initialProps: { can: false } });
        expect(onBuildActivate).not.toHaveBeenCalled();
        rerender({ can: true });
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
    });

    test("consumes each signal once — re-render with the same message does not re-deploy", () => {
        const onBuildActivate = vi.fn();
        const messages = [agentMsg("t1", { signal: true })];
        const { rerender } = renderHook(() => useChatDeployTrigger({ sessionId: SID, messages, canBuildActivate: true, saveOnlyBuild: false, isBuildActivating: false, onBuildActivate }));
        rerender();
        rerender();
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
    });

    test("does not fire on a different completed message without the signal", () => {
        const onBuildActivate = vi.fn();
        renderHook(() => useChatDeployTrigger({ sessionId: SID, messages: [agentMsg("t1", { signal: false })], canBuildActivate: true, saveOnlyBuild: false, isBuildActivating: false, onBuildActivate }));
        expect(onBuildActivate).not.toHaveBeenCalled();
    });

    test("ignores the signal on an incomplete (still-streaming) message until it completes", () => {
        const onBuildActivate = vi.fn();
        const { rerender } = renderHook((props: { messages: MessageFE[] }) => useChatDeployTrigger({ sessionId: SID, messages: props.messages, canBuildActivate: true, saveOnlyBuild: false, isBuildActivating: false, onBuildActivate }), {
            initialProps: { messages: [agentMsg("t1", { signal: true, complete: false })] },
        });
        expect(onBuildActivate).not.toHaveBeenCalled();
        rerender({ messages: [agentMsg("t1", { signal: true, complete: true })] });
        expect(onBuildActivate).toHaveBeenCalledTimes(1);
    });

    test("does not fire while a deploy is already in flight", () => {
        const onBuildActivate = vi.fn();
        renderHook(() => useChatDeployTrigger({ sessionId: SID, messages: [agentMsg("t1", { signal: true })], canBuildActivate: true, saveOnlyBuild: false, isBuildActivating: true, onBuildActivate }));
        expect(onBuildActivate).not.toHaveBeenCalled();
    });

    test("save-only build (connector/skill-only): consumes the signal as a no-op, never fires deploy — the connector hang fix", () => {
        const onBuildActivate = vi.fn();
        const { rerender } = renderHook(
            (props: { can: boolean }) => useChatDeployTrigger({ sessionId: SID, messages: [agentMsg("t1", { signal: true })], canBuildActivate: props.can, saveOnlyBuild: true, isBuildActivating: false, onBuildActivate }),
            { initialProps: { can: false } }
        );
        expect(onBuildActivate).not.toHaveBeenCalled();
        rerender({ can: true });
        expect(onBuildActivate).not.toHaveBeenCalled();
    });

    test("drops a held request if the build resolves to save-only after the signal was armed (late manifest settle)", () => {
        const onBuildActivate = vi.fn();
        const { rerender } = renderHook(
            (props: { save: boolean; can: boolean }) => useChatDeployTrigger({ sessionId: SID, messages: [agentMsg("t1", { signal: true })], canBuildActivate: props.can, saveOnlyBuild: props.save, isBuildActivating: false, onBuildActivate }),
            { initialProps: { save: false, can: false } }
        );
        expect(onBuildActivate).not.toHaveBeenCalled();
        rerender({ save: true, can: true });
        expect(onBuildActivate).not.toHaveBeenCalled();
    });

    test("does NOT carry a held request across a session switch (no cross-session deploy)", () => {
        const onBuildActivate = vi.fn();
        const { rerender } = renderHook(
            (props: { sid: string; can: boolean }) => useChatDeployTrigger({ sessionId: props.sid, messages: [agentMsg("t1", { signal: true })], canBuildActivate: props.can, saveOnlyBuild: false, isBuildActivating: false, onBuildActivate }),
            {
                initialProps: { sid: "A", can: false },
            }
        );
        expect(onBuildActivate).not.toHaveBeenCalled();
        rerender({ sid: "B", can: true });
        expect(onBuildActivate).not.toHaveBeenCalled();
    });
});
