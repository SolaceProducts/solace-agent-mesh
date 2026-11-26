// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { StoryProvider } from "@/testing/providers/StoryProvider";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const manifestState = {
    manifest: null,
    isLoading: false,
    isSettled: true,
    isUnreadable: false,
    saveManifest: vi.fn().mockResolvedValue(true),
};

vi.mock("./hooks/useBuilderManifest", async importOriginal => ({
    ...(await importOriginal<typeof import("./hooks/useBuilderManifest")>()),
    useBuilderManifest: () => manifestState,
}));

const validation = { validationStuck: false, retryValidation: vi.fn() };
vi.mock("./hooks/useAutoFullValidation", async importOriginal => ({
    ...(await importOriginal<typeof import("./hooks/useAutoFullValidation")>()),
    useAutoFullValidation: () => validation,
}));

const { BuilderPage } = await import("./BuilderPage");

const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } })),
    http.get("*/api/v1/sessions/:id", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } }))
);

let trackSpy: ReturnType<typeof vi.spyOn>;
let sessionSpy: ReturnType<typeof vi.spyOn>;

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());
beforeEach(() => {
    sessionStorage.clear();
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    sessionSpy = vi.spyOn(productAnalyticsClient, "sessionId").mockReturnValue("session-one");
});
afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
    sessionStorage.clear();
});

function openedCount(): number {
    return (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === "builder:opened").length;
}

function renderBuilder() {
    return render(
        <MemoryRouter initialEntries={["/builder"]}>
            <StoryProvider chatContextValues={{ isResponding: false } as unknown as Partial<ChatContextValue>}>
                <BuilderPage />
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("builder:opened", () => {
    test("is reported when the builder page mounts", async () => {
        renderBuilder();

        await waitFor(() => expect(trackSpy).toHaveBeenCalledWith("builder:opened", {}));
    });

    test("is not reported again when the builder is re-entered in the same session", async () => {
        renderBuilder().unmount();
        await waitFor(() => expect(openedCount()).toBe(1));

        renderBuilder();

        await waitFor(() => expect(openedCount()).toBe(1));
    });

    test("is reported again in a new session", async () => {
        renderBuilder().unmount();
        await waitFor(() => expect(openedCount()).toBe(1));
        sessionSpy.mockReturnValue("session-two");

        renderBuilder();

        await waitFor(() => expect(openedCount()).toBe(2));
    });
});
