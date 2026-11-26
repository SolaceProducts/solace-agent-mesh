// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { useTitleGeneration } from "./useTitleGeneration";

const generateTitleUrl = (id: string) => `/api/v1/sessions/${id}/generateTitle`;

describe("useTitleGeneration", () => {
    let postSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        vi.useFakeTimers();
        vi.spyOn(api.webui, "get").mockResolvedValue({ name: "New Chat" } as unknown as Response);
        postSpy = vi.spyOn(api.webui, "post").mockResolvedValue({} as unknown as Response);
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    it("posts (force=false) when the agent response is empty but the user message is present", async () => {
        const { result } = renderHook(() => useTitleGeneration());

        await result.current.generateTitle("sid-1", "search Atlassian for X", "");

        expect(postSpy).toHaveBeenCalledWith(generateTitleUrl("sid-1"), { userMessage: "search Atlassian for X", agentResponse: "", force: false });
    });

    it("posts (force=false) for a normal turn with both sides present", async () => {
        const { result } = renderHook(() => useTitleGeneration());

        await result.current.generateTitle("sid-2", "hello", "hi there");

        expect(postSpy).toHaveBeenCalledWith(generateTitleUrl("sid-2"), { userMessage: "hello", agentResponse: "hi there", force: false });
    });

    it("skips (force=false) when the user message is empty", async () => {
        const { result } = renderHook(() => useTitleGeneration());

        await result.current.generateTitle("sid-3", "", "");

        expect(postSpy).not.toHaveBeenCalled();
    });

    it("posts (force=true / Rename with AI) even with empty user and agent text", async () => {
        const { result } = renderHook(() => useTitleGeneration());

        await result.current.generateTitle("sid-4", "", "", "Existing Title", true);

        expect(postSpy).toHaveBeenCalledWith(generateTitleUrl("sid-4"), { userMessage: "", agentResponse: "", force: true });
    });

    it("skips for an invalid session id", async () => {
        const { result } = renderHook(() => useTitleGeneration());

        await result.current.generateTitle("null", "hello", "hi there");

        expect(postSpy).not.toHaveBeenCalled();
    });

    it("force (Rename with AI) bypasses the in-flight guard", async () => {
        const { result } = renderHook(() => useTitleGeneration());

        await result.current.generateTitle("sid-guard", "hello", "hi there");
        expect(postSpy).toHaveBeenCalledTimes(1);

        await result.current.generateTitle("sid-guard", "hello again", "hi");
        expect(postSpy).toHaveBeenCalledTimes(1);

        await result.current.generateTitle("sid-guard", "", "", "Existing Title", true);
        expect(postSpy).toHaveBeenCalledTimes(2);
        expect(postSpy).toHaveBeenLastCalledWith(generateTitleUrl("sid-guard"), { userMessage: "", agentResponse: "", force: true });
    });

    it("dispatches session-title-updated when the server reports the title already exists", async () => {
        postSpy.mockResolvedValue({ skipped: true } as unknown as Response);
        const updated = vi.fn();
        window.addEventListener("session-title-updated", updated);

        const { result } = renderHook(() => useTitleGeneration());
        await result.current.generateTitle("sid-skip", "hello", "hi there");

        expect(updated).toHaveBeenCalledTimes(1);
        expect((updated.mock.calls[0][0] as CustomEvent).detail).toEqual({ sessionId: "sid-skip" });
        window.removeEventListener("session-title-updated", updated);
    });

    it("dispatches session-title-updated once a poll sees the generated title, then releases the guard for the next trigger", async () => {
        const getSpy = vi.spyOn(api.webui, "get");
        const updated = vi.fn();
        window.addEventListener("session-title-updated", updated);

        const { result } = renderHook(() => useTitleGeneration());
        await result.current.generateTitle("sid-poll", "hello", "hi there");

        await vi.advanceTimersByTimeAsync(1500);
        expect(updated).not.toHaveBeenCalled();

        getSpy.mockResolvedValue({ name: "Quantum Chat" } as unknown as Response);
        await vi.advanceTimersByTimeAsync(3000);
        expect(updated).toHaveBeenCalledTimes(1);
        expect((updated.mock.calls[0][0] as CustomEvent).detail).toEqual({ sessionId: "sid-poll" });

        await result.current.generateTitle("sid-poll", "again", "sure");
        expect(postSpy).toHaveBeenCalledTimes(2);

        window.removeEventListener("session-title-updated", updated);
    });

    it("gives up after the poll schedule but still dispatches session-title-updated", async () => {
        const getSpy = vi.spyOn(api.webui, "get");
        getSpy.mockRejectedValueOnce(new Error("transient 500"));

        const updated = vi.fn();
        const generating = vi.fn();
        window.addEventListener("session-title-updated", updated);
        window.addEventListener("session-title-generating", generating);

        const { result } = renderHook(() => useTitleGeneration());
        await result.current.generateTitle("sid-giveup", "hello", "hi there");

        await vi.advanceTimersByTimeAsync(16500);
        expect(updated).toHaveBeenCalledTimes(1);
        const lastGenerating = generating.mock.calls.at(-1)?.[0] as CustomEvent;
        expect(lastGenerating.detail).toEqual({ sessionId: "sid-giveup", isGenerating: false });

        window.removeEventListener("session-title-updated", updated);
        window.removeEventListener("session-title-generating", generating);
    });

    it("stops cleanly and releases the guard when the POST fails", async () => {
        postSpy.mockRejectedValueOnce(new Error("backend down"));
        const updated = vi.fn();
        const generating = vi.fn();
        window.addEventListener("session-title-updated", updated);
        window.addEventListener("session-title-generating", generating);

        const { result } = renderHook(() => useTitleGeneration());
        await result.current.generateTitle("sid-postfail", "hello", "hi there");

        const lastGenerating = generating.mock.calls.at(-1)?.[0] as CustomEvent;
        expect(lastGenerating.detail).toEqual({ sessionId: "sid-postfail", isGenerating: false });
        expect(updated).not.toHaveBeenCalled();

        await result.current.generateTitle("sid-postfail", "hello", "hi there");
        expect(postSpy).toHaveBeenCalledTimes(2);

        window.removeEventListener("session-title-updated", updated);
        window.removeEventListener("session-title-generating", generating);
    });
});
