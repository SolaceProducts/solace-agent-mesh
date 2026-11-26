// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { RedirectButton, type RedirectData } from "./RedirectButton";
import { appendUrlParams, parseSafeRedirectPath } from "./redirectRoute";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const mockPost = vi.spyOn(api.webui, "post" as any);

function renderButton(data: RedirectData, chatContextValues?: Partial<ChatContextValue>) {
    return render(
        <StoryProvider chatContextValues={chatContextValues}>
            <RedirectButton data={data} />
        </StoryProvider>
    );
}

const redirectTo = (route: string): RedirectData => ({
    type: "redirect",
    route,
    label: "Go",
    context: "build a financial advisor agent",
});

describe("RedirectButton sessionStorage scoping", () => {
    beforeEach(() => {
        sessionStorage.clear();
        mockPost.mockReset();
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        mockPost.mockResolvedValue({} as any);
    });
    afterEach(() => {
        sessionStorage.clear();
    });

    it("writes pending_input for a chat-bearing /builder redirect (ChatInputArea consumes it)", async () => {
        renderButton(redirectTo("/builder"));
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toBe("build a financial advisor agent"));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBe("build a financial advisor agent");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBe(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID));
    });

    it("writes pending_input UNtargeted for a /chat redirect — nothing on /chat switches to the dest session", async () => {
        renderButton(redirectTo("/chat"));
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toBe("build a financial advisor agent"));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBe("build a financial advisor agent");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBeNull();
    });

    it("does NOT write pending_input for a /prompts redirect — nothing there consumes it, so it would leak into the next chat", async () => {
        renderButton(redirectTo("/prompts/new?mode=ai-assisted"));
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toBe("build a financial advisor agent"));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBeNull();
    });

    const createdSession = () => mockPost.mock.calls.some(c => c[0] === "/api/v1/sessions");

    it("does NOT eagerly create a session for a non-builder redirect with no artifacts (avoids an empty 'New Chat')", async () => {
        renderButton(redirectTo("/prompts/new?mode=ai-assisted"));
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toBe("build a financial advisor agent"));
        expect(createdSession()).toBe(false);
    });

    it("does NOT eagerly create a session for a /builder redirect (session created lazily on first message)", async () => {
        renderButton(redirectTo("/builder"));
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toBe("build a financial advisor agent"));
        expect(createdSession()).toBe(false);
    });

    it("stores artifact intent in sessionStorage instead of eagerly creating a session and copying", async () => {
        renderButton({ ...redirectTo("/prompts/new"), artifacts: ["a.txt"], source_session_id: "src-1" });
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS)).toBeTruthy());
        const pending = JSON.parse(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS)!);
        expect(pending.sourceSessionId).toBe("src-1");
        expect(pending.filenames).toEqual(["a.txt"]);
        expect(pending.destSessionId).toBe(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID));
        expect(createdSession()).toBe(false);
        expect(mockPost.mock.calls.some(c => String(c[0]).endsWith("/artifacts/copy"))).toBe(false);
    });

    it("validates redirect routes: allows app sections, rejects cross-origin / disallowed / malformed / prefix-spoof", () => {
        expect(parseSafeRedirectPath("/builder")).toBe("/builder");
        expect(parseSafeRedirectPath("/prompts/new?mode=ai-assisted")).toBe("/prompts/new");
        expect(parseSafeRedirectPath("/scheduled-tasks?mode=create")).toBe("/scheduled-tasks");
        expect(parseSafeRedirectPath("https://evil.example.com/builder")).toBeNull();
        expect(parseSafeRedirectPath("/admin/secrets")).toBeNull();
        expect(parseSafeRedirectPath("")).toBeNull();
        expect(parseSafeRedirectPath("/builderX")).toBeNull();
        expect(parseSafeRedirectPath("/chatbot")).toBeNull();
    });

    it("appendUrlParams appends params, choosing ? or & by existing query", () => {
        expect(appendUrlParams("/prompts/new", undefined)).toBe("/prompts/new");
        expect(appendUrlParams("/prompts/new", {})).toBe("/prompts/new");
        expect(appendUrlParams("/prompts/new", { mode: "ai-assisted" })).toBe("/prompts/new?mode=ai-assisted");
        expect(appendUrlParams("/builder?session=1", { mode: "edit" })).toBe("/builder?session=1&mode=edit");
    });

    it("reports and refuses an unsafe redirect route instead of navigating", async () => {
        const displayError = vi.fn();
        renderButton(redirectTo("https://evil.example.com/builder"), { displayError });
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(displayError).toHaveBeenCalledWith({ title: "Invalid redirect", error: "The server provided an unsafe redirect URL.", errorSource: "client" }));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toBeNull();
    });

    it("does NOT write pending_input for /scheduled-tasks but still writes context (its consumer reads pending_input ?? context)", async () => {
        renderButton(redirectTo("/scheduled-tasks"));
        fireEvent.click(screen.getByRole("button"));
        await waitFor(() => expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT)).toBe("build a financial advisor agent"));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBeNull();
    });
});
