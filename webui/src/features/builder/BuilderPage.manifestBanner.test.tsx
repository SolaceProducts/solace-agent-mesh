// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import type { BuildManifest } from "./hooks/useBuilderManifest";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const manifestState = {
    manifest: null as BuildManifest | null,
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

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());
afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
});

beforeEach(() => {
    manifestState.manifest = null;
    manifestState.isUnreadable = false;
    validation.validationStuck = false;
});

function renderBuilder() {
    render(
        <MemoryRouter initialEntries={["/builder"]}>
            <StoryProvider chatContextValues={{ isResponding: false } as unknown as Partial<ChatContextValue>}>
                <BuilderPage />
            </StoryProvider>
        </MemoryRouter>
    );
}

const GOOD = { name: "sys", components: [{ filename: "a.yaml", type: "agent", name: "A", origin: "session", status: "validated" }] } as unknown as BuildManifest;

describe("BuilderPage — the unreadable-manifest banner", () => {
    test("says nothing while the manifest reads fine", async () => {
        manifestState.manifest = GOOD;
        renderBuilder();
        await waitFor(() => expect(screen.queryByText(/couldn't be read/i)).toBeNull());
    });

    test("reports a manifest that stopped parsing, and names staleness rather than an empty canvas", async () => {
        manifestState.manifest = GOOD;
        manifestState.isUnreadable = true;
        renderBuilder();

        const alert = await screen.findByText(/couldn't be read/i);
        expect(alert.textContent).toMatch(/last version that parsed/);
        expect(alert.textContent).not.toMatch(/stays empty/);
    });

    test("keeps the empty-canvas wording when there is no manifest to show", async () => {
        manifestState.manifest = null;
        manifestState.isUnreadable = true;
        renderBuilder();

        const alert = await screen.findByText(/couldn't be read/i);
        expect(alert.textContent).toMatch(/stays empty/);
    });

    test("shows only the unreadable banner when validation is also stuck", async () => {
        manifestState.manifest = GOOD;
        manifestState.isUnreadable = true;
        validation.validationStuck = true;
        renderBuilder();

        await screen.findByText(/couldn't be read/i);
        expect(screen.queryByText(/Couldn't confirm full validation/i)).toBeNull();
    });

    test("still shows the validation banner on its own", async () => {
        manifestState.manifest = GOOD;
        validation.validationStuck = true;
        renderBuilder();

        await screen.findByText(/Couldn't confirm full validation/i);
    });
});
