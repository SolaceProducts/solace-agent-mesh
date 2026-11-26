// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { OpenFeature, OpenFeatureProvider } from "@openfeature/react-sdk";
import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { AuthContext, type AuthContextValue } from "@/providers/contexts";
import { SamFeatureProvider } from "@/providers/openfeature/SamFeatureProvider";

import { PromptsCommand } from "@/features/chat/PromptsCommand";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";
import type { PromptGroup } from "@/types";

const stubAuth: AuthContextValue = {
    isAuthenticated: true,
    useAuthorization: false,
    login: async () => {},
    logout: () => {},
    userInfo: { username: "tester" },
};

const mockFetch = vi.fn();
function makeFetchResponse(items: unknown[]) {
    const body = {
        data: items,
        meta: { pagination: { pageNumber: 1, count: items.length, pageSize: 20, nextPage: null, totalPages: 1 } },
    };
    return { ok: true, status: 200, text: async () => JSON.stringify(body) };
}

beforeEach(() => {
    mockFetch.mockResolvedValue(makeFetchResponse([]));
    vi.stubGlobal("fetch", mockFetch);
    window.HTMLElement.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
    vi.unstubAllGlobals();
});

const mockGroup: PromptGroup = {
    id: "g1",
    name: "My Prompt",
    description: "A helpful prompt",
    command: "foo",
    userId: "u1",
    isShared: false,
    isPinned: false,
    createdAt: new Date(0).toISOString(),
    updatedAt: new Date(0).toISOString(),
    productionPrompt: {
        id: "p1",
        promptText: "Hello world",
        groupId: "g1",
        userId: "u1",
        version: 1,
        createdAt: new Date(0).toISOString(),
        updatedAt: new Date(0).toISOString(),
    },
};

async function renderPrompts(props: Partial<React.ComponentProps<typeof PromptsCommand>> = {}, identityServiceType: string | null = null) {
    await OpenFeature.setProviderAndWait(new SamFeatureProvider({ prompt_sharing: true }));
    const textAreaRef = React.createRef<HTMLDivElement>();
    return render(
        <OpenFeatureProvider>
            <MockConfigProvider mockValues={{ identityServiceType }}>
                <MemoryRouter>
                    <AuthContext.Provider value={stubAuth}>
                        <PromptsCommand isOpen={true} onClose={vi.fn()} onPromptSelect={vi.fn()} textAreaRef={textAreaRef} {...props} />
                    </AuthContext.Provider>
                </MemoryRouter>
            </MockConfigProvider>
        </OpenFeatureProvider>
    );
}

describe("PromptsCommand", () => {
    test("renders nothing when isOpen is false", async () => {
        await renderPrompts({ isOpen: false });
        expect(screen.queryByTestId("promptCommand")).not.toBeInTheDocument();
    });

    test("renders the prompt list when fetch returns groups", async () => {
        mockFetch.mockResolvedValue(makeFetchResponse([mockGroup]));
        await renderPrompts();
        await screen.findByText("My Prompt");
        expect(screen.getByText("/foo")).toBeInTheDocument();
    });

    test("shows 'Create Template from Session' when user messages exist", async () => {
        mockFetch.mockResolvedValue(makeFetchResponse([]));
        await renderPrompts({
            messages: [
                {
                    isUser: true,
                    isStatusBubble: false,
                    parts: [{ kind: "text", text: "Hello" }],
                },
            ],
        });
        await screen.findByText("Create Template from Session");
    });

    test("pressing Enter selects the first prompt and calls onPromptSelect", async () => {
        mockFetch.mockResolvedValue(makeFetchResponse([mockGroup]));
        const onPromptSelect = vi.fn();
        await renderPrompts({ onPromptSelect });
        await screen.findByText("My Prompt");
        const input = screen.getByPlaceholderText("Search by shortcut or name...");
        fireEvent.keyDown(input, { key: "Enter" });
        expect(onPromptSelect).toHaveBeenCalledWith("Hello world");
    });

    test("clicking the backdrop calls onClose", async () => {
        const onClose = vi.fn();
        await renderPrompts({ onClose });
        await screen.findByTestId("promptCommand");
        const backdrop = screen.getByRole("button", { name: /close/i });
        fireEvent.click(backdrop);
        expect(onClose).toHaveBeenCalled();
    });

    test("hides the prompt owner when there is no identity service", async () => {
        mockFetch.mockResolvedValue(makeFetchResponse([{ ...mockGroup, authorName: "Alice" }]));
        await renderPrompts({}, null);
        await screen.findByText("My Prompt");
        expect(screen.queryByText("Alice")).not.toBeInTheDocument();
    });

    test("shows the prompt owner when an identity service is wired", async () => {
        mockFetch.mockResolvedValue(makeFetchResponse([{ ...mockGroup, authorName: "Alice" }]));
        await renderPrompts({}, "okta");
        await screen.findByText("My Prompt");
        expect(screen.getByText("Alice")).toBeInTheDocument();
    });

    test("shows the standard circular Spinner while prompts load", async () => {
        mockFetch.mockReturnValue(new Promise(() => {}));
        await renderPrompts();
        const popup = await screen.findByTestId("promptCommand");
        expect(popup.querySelector("svg.animate-spin")).toBeInTheDocument();
    });
});
