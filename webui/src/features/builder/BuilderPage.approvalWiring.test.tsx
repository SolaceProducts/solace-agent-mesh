// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, waitFor } from "@testing-library/react";
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
vi.mock("./hooks/useComponentsMissingCredentials", () => ({ useComponentsMissingCredentials: () => new Map() }));

const seen = vi.hoisted(() => ({
    actions: undefined as undefined | { autoSave?: boolean; onAutoSaveComplete?: () => void; onBuildActivateApiChange?: unknown },
    approve: undefined as undefined | (() => void),
}));
vi.mock("./BuilderActions", () => ({
    BuilderActions: (props: { autoSave?: boolean; onAutoSaveComplete?: () => void; onBuildActivateApiChange?: unknown }) => {
        seen.actions = props;
        return null;
    },
}));
vi.mock("./BuilderChatPanel", () => ({
    BuilderChatPanel: (props: { onPlanApproved?: () => void }) => {
        seen.approve = props.onPlanApproved;
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

describe("BuilderPage wiring of the plan approval", () => {
    test("approving the plan arms BuilderActions' auto-save, and its completion callback disarms it", async () => {
        sessionStorage.clear();
        render(
            <MemoryRouter initialEntries={["/builder"]}>
                <StoryProvider chatContextValues={{ isResponding: false, sessionId: "s1" } as unknown as Partial<ChatContextValue>}>
                    <BuilderPage />
                </StoryProvider>
            </MemoryRouter>
        );
        await waitFor(() => expect(seen.actions).toBeDefined());
        await waitFor(() => expect(seen.approve).toBeDefined());
        expect(sessionStorage.getItem("sam_builder_active_session")).toBe("s1");
        expect(seen.actions!.autoSave).toBe(false);
        expect(typeof seen.actions!.onBuildActivateApiChange).toBe("function");

        act(() => seen.approve!());
        await waitFor(() => expect(seen.actions!.autoSave).toBe(true));
        expect(sessionStorage.getItem("sam_builder_pending_autocreate")).toBe("s1");

        act(() => seen.actions!.onAutoSaveComplete!());
        await waitFor(() => expect(seen.actions!.autoSave).toBe(false));
        expect(sessionStorage.getItem("sam_builder_pending_autocreate")).toBeNull();
    });
});
