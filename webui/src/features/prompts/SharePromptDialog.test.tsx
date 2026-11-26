// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { ChatContext, type ChatContextValue, ConfigContext, type ConfigContextValue } from "@/providers/contexts";

import type { PromptGroup } from "@/types/prompts";

import { SharePromptDialog } from "./SharePromptDialog";

const fakeConfig = { identityServiceType: null } as unknown as ConfigContextValue;
const addNotification = vi.fn();
const fakeChat = { addNotification } as unknown as ChatContextValue;

const fakePromptGroup: PromptGroup = {
    id: "group-abc",
    name: "My Templates",
} as PromptGroup;

const fakeSharesEnvelope = {
    data: [
        {
            id: "share-1",
            userEmail: "editor@example.com",
            accessLevel: "resource_editor",
            sharedByEmail: "owner@example.com",
            createdAt: "2026-05-29T08:00:00Z",
            updatedAt: "2026-05-29T08:00:00Z",
        },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 1, totalPages: 1, count: 1, nextPage: null } },
};

let queryClient: QueryClient;
let getSpy: ReturnType<typeof vi.spyOn>;
let postSpy: ReturnType<typeof vi.spyOn>;
let putSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    getSpy = vi.spyOn(api.webui, "get");
    postSpy = vi.spyOn(api.webui, "post");
    putSpy = vi.spyOn(api.webui, "put");
    addNotification.mockClear();
});

afterEach(() => {
    vi.restoreAllMocks();
    queryClient.clear();
});

function renderDialog(props: Partial<React.ComponentProps<typeof SharePromptDialog>> = {}) {
    return render(
        <QueryClientProvider client={queryClient}>
            <ConfigContext.Provider value={fakeConfig}>
                <ChatContext.Provider value={fakeChat}>
                    <SharePromptDialog isOpen={true} onClose={() => {}} promptGroup={fakePromptGroup} {...props} />
                </ChatContext.Provider>
            </ConfigContext.Provider>
        </QueryClientProvider>
    );
}

describe("SharePromptDialog", () => {
    test("renders the share-title text when open", () => {
        getSpy.mockResolvedValue(fakeSharesEnvelope);
        renderDialog();
        expect(screen.getByRole("heading", { name: /share prompt/i })).toBeInTheDocument();
    });

    test("displays the loaded recipient emails", async () => {
        getSpy.mockResolvedValue(fakeSharesEnvelope);
        renderDialog();
        await waitFor(() => expect(screen.getByText(/editor@example\.com/)).toBeInTheDocument());
    });

    test("on Save with a new recipient: POSTs and emits singular share notification", async () => {
        const user = userEvent.setup();
        getSpy.mockResolvedValue({ data: [], meta: fakeSharesEnvelope.meta });
        postSpy.mockResolvedValue({ created: [], updated: [], totalProcessed: 1 });

        renderDialog();
        await waitFor(() => expect(getSpy).toHaveBeenCalled());

        await user.click(screen.getByRole("button", { name: /^add$/i }));
        await user.type(screen.getByPlaceholderText(/enter email/i), "new@example.com{Enter}");
        await user.click(screen.getByRole("button", { name: /^save$/i }));

        await waitFor(() => expect(postSpy).toHaveBeenCalled());
        expect(postSpy).toHaveBeenCalledWith("/api/v1/prompts/groups/group-abc/shares", { shares: [{ userEmail: "new@example.com", accessLevel: "resource_viewer" }] });
        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("My Templates shared with 1 user", "success"));
    });

    test("on Save removing an existing recipient: PUTs and emits singular removal notification", async () => {
        const user = userEvent.setup();
        getSpy.mockResolvedValue(fakeSharesEnvelope);
        putSpy.mockResolvedValue(undefined);

        renderDialog();
        await screen.findByText("editor@example.com");

        const row = screen.getByText("editor@example.com").closest("[data-testid='share-row']") as HTMLElement;
        await user.click(row.querySelector("[data-testid='remove-share']") as HTMLButtonElement);
        await user.click(screen.getByRole("button", { name: /^save$/i }));

        await waitFor(() => expect(putSpy).toHaveBeenCalled());
        expect(putSpy).toHaveBeenCalledWith("/api/v1/prompts/groups/group-abc/shares", { shares: [] });
        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("My Templates removed for editor@example.com", "success"));
    });
});
