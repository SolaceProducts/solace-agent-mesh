// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { useAutoGenerateTitle } from "./useAutoGenerateTitle";

const generateTitleUrl = (id: string) => `/api/v1/sessions/${id}/generateTitle`;

let sessionCounter = 0;
const uniqueSession = () => `session-${++sessionCounter}`;

const wrapperWithAutoTitle = (enabled: boolean) =>
    function Wrapper({ children }: { children: React.ReactNode }) {
        return (
            <StoryProvider configContextValues={{ persistenceEnabled: true }} flagValues={{ auto_title_generation: enabled }}>
                {children}
            </StoryProvider>
        );
    };

describe("useAutoGenerateTitle", () => {
    let postSpy: ReturnType<typeof vi.spyOn>;
    let getSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        vi.useFakeTimers();
        getSpy = vi.spyOn(api.webui, "get").mockResolvedValue({ name: "New Chat" } as unknown as Response);
        postSpy = vi.spyOn(api.webui, "post").mockResolvedValue({} as unknown as Response);
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    it("generates a title when the agent response is empty (e.g. a denied tool-approval turn)", async () => {
        const sessionId = uniqueSession();
        const { result } = renderHook(() => useAutoGenerateTitle(), { wrapper: wrapperWithAutoTitle(true) });

        await result.current.autoGenerateTitle(sessionId, "search Atlassian for X", "");

        expect(postSpy).toHaveBeenCalledWith(generateTitleUrl(sessionId), { userMessage: "search Atlassian for X", agentResponse: "", force: false });
    });

    it("generates a title for a normal turn with both sides present", async () => {
        const sessionId = uniqueSession();
        const { result } = renderHook(() => useAutoGenerateTitle(), { wrapper: wrapperWithAutoTitle(true) });

        await result.current.autoGenerateTitle(sessionId, "hello", "hi there");

        expect(postSpy).toHaveBeenCalledWith(generateTitleUrl(sessionId), { userMessage: "hello", agentResponse: "hi there", force: false });
    });

    it("skips when the user message is empty", async () => {
        const { result } = renderHook(() => useAutoGenerateTitle(), { wrapper: wrapperWithAutoTitle(true) });

        await result.current.autoGenerateTitle(uniqueSession(), "", "");

        expect(postSpy).not.toHaveBeenCalled();
    });

    it("skips when auto-title generation is disabled", async () => {
        const { result } = renderHook(() => useAutoGenerateTitle(), { wrapper: wrapperWithAutoTitle(false) });

        await result.current.autoGenerateTitle(uniqueSession(), "search Atlassian for X", "");

        expect(postSpy).not.toHaveBeenCalled();
    });

    it("generates only once per session", async () => {
        const sessionId = uniqueSession();
        const { result } = renderHook(() => useAutoGenerateTitle(), { wrapper: wrapperWithAutoTitle(true) });

        await result.current.autoGenerateTitle(sessionId, "first", "");
        await result.current.autoGenerateTitle(sessionId, "second", "");

        expect(postSpy).toHaveBeenCalledTimes(1);
    });

    it("generates a title for a background task whose agent response is empty (denied tool approval)", async () => {
        const sessionId = uniqueSession();
        getSpy.mockImplementation((url: string) => (url.endsWith("/titleData") ? Promise.resolve({ userMessage: "search Atlassian for X", agentResponse: "" } as unknown as Response) : Promise.resolve({ name: "New Chat" } as unknown as Response)));
        const { result } = renderHook(() => useAutoGenerateTitle(), { wrapper: wrapperWithAutoTitle(true) });

        await result.current.autoGenerateTitleForTask("task-1", [{ taskId: "task-1", sessionId }]);

        expect(postSpy).toHaveBeenCalledWith(generateTitleUrl(sessionId), { userMessage: "search Atlassian for X", agentResponse: "", force: false });
    });
});
