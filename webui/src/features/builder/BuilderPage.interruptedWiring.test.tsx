// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen, waitFor } from "@testing-library/react";
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
    full_validation_passed: false,
    components: [
        { filename: "a.yaml", type: "agent", name: "AlphaAgent", origin: "session", status: "draft" },
        { filename: "b.yaml", type: "agent", name: "BetaAgent", origin: "session", status: "draft" },
    ],
} as unknown as BuildManifest;

vi.mock("./hooks/useBuilderManifest", async importOriginal => ({
    ...(await importOriginal<typeof import("./hooks/useBuilderManifest")>()),
    useBuilderManifest: () => ({ manifest: MANIFEST, isLoading: false, isSettled: true, isUnreadable: false, saveManifest: vi.fn().mockResolvedValue(true) }),
}));
vi.mock("./hooks/useComponentsMissingCredentials", () => ({ useComponentsMissingCredentials: () => new Map() }));
vi.mock("./BuilderActions", () => ({ BuilderActions: () => null }));

const seen = vi.hoisted(() => ({ reportInterrupted: undefined as undefined | ((interrupted: boolean) => void) }));
vi.mock("./BuilderChatPanel", () => ({
    BuilderChatPanel: (props: { onBuildInterruptedChange?: (interrupted: boolean) => void }) => {
        seen.reportInterrupted = props.onBuildInterruptedChange;
        return null;
    },
}));

const { BuilderPage } = await import("./BuilderPage");

const emptyPage = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json(emptyPage)),
    http.get("*/api/v1/sessions/:id", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json(emptyPage))
);
beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());
afterEach(() => server.resetHandlers());

const tabStatuses = () =>
    screen
        .getAllByRole("tab", { name: /,/ })
        .map(t => t.getAttribute("aria-label"))
        .sort((a, b) => (a ?? "").localeCompare(b ?? ""));

describe("BuilderPage carries the chat panel's interruption verdict to the canvas", () => {
    test("a verdict the panel reports turns the unfinished tabs from Draft to Paused, and withdrawing it turns them back", async () => {
        sessionStorage.clear();
        render(
            <MemoryRouter initialEntries={["/builder"]}>
                <StoryProvider chatContextValues={{ isResponding: false, sessionId: "s1" } as unknown as Partial<ChatContextValue>}>
                    <BuilderPage />
                </StoryProvider>
            </MemoryRouter>
        );
        await waitFor(() => expect(seen.reportInterrupted).toBeDefined());
        await waitFor(() => expect(tabStatuses()).toEqual(["AlphaAgent, Draft", "BetaAgent, Draft"]));

        act(() => seen.reportInterrupted!(true));
        await waitFor(() => expect(tabStatuses()).toEqual(["AlphaAgent, Paused", "BetaAgent, Paused"]));

        act(() => seen.reportInterrupted!(false));
        await waitFor(() => expect(tabStatuses()).toEqual(["AlphaAgent, Draft", "BetaAgent, Draft"]));
    });
});
