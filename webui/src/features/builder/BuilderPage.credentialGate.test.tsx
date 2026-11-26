// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import type { BuildManifest } from "./hooks/useBuilderManifest";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const MANIFEST = {
    name: "sys",
    full_validation_passed: true,
    components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "validated" }],
} as unknown as BuildManifest;

vi.mock("./hooks/useBuilderManifest", async importOriginal => ({
    ...(await importOriginal<typeof import("./hooks/useBuilderManifest")>()),
    useBuilderManifest: () => ({ manifest: MANIFEST, isLoading: false, isSettled: true, isUnreadable: false, saveManifest: vi.fn().mockResolvedValue(true) }),
}));

const gate = new Map([["EntKeyAPI", 1]]);
const credentialsHook = vi.hoisted(() => vi.fn());
vi.mock("./hooks/useComponentsMissingCredentials", () => ({
    useComponentsMissingCredentials: credentialsHook,
}));

const received = vi.hoisted(() => ({ actions: undefined as ReadonlyMap<string, number> | undefined, chatPanel: undefined as ReadonlyMap<string, number> | undefined }));
vi.mock("./BuilderActions", () => ({
    BuilderActions: (props: { componentsMissingCreds?: ReadonlyMap<string, number> }) => {
        received.actions = props.componentsMissingCreds;
        return null;
    },
}));
vi.mock("./BuilderChatPanel", () => ({
    BuilderChatPanel: (props: { componentsMissingCreds?: ReadonlyMap<string, number> }) => {
        received.chatPanel = props.componentsMissingCreds;
        return null;
    },
}));

const { BuilderPage } = await import("./BuilderPage");

const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } })),
    http.get("*/api/v1/sessions/:id", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } }))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());
afterEach(() => server.resetHandlers());

describe("BuilderPage — credential gate wiring", () => {
    test("hands Build & Activate and the Test gate the map the credentials hook reports", async () => {
        credentialsHook.mockReturnValue(gate);
        render(
            <MemoryRouter initialEntries={["/builder"]}>
                <StoryProvider chatContextValues={{ isResponding: false } as unknown as Partial<ChatContextValue>}>
                    <BuilderPage />
                </StoryProvider>
            </MemoryRouter>
        );

        await waitFor(() => expect(received.actions).toBe(gate));
        expect(received.chatPanel).toBe(gate);
        expect(credentialsHook).toHaveBeenCalledWith(MANIFEST, expect.any(Map));
    });
});
