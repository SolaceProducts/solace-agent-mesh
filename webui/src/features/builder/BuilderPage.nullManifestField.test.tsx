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

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const store = vi.hoisted(() => ({ manifestYaml: "", listedVersions: {} as Record<string, number> }));

vi.mock("@/api/artifacts", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/artifacts")>()),
    useChatArtifacts: () => ({
        data: [
            { filename: "build_manifest.yaml", mimeType: "application/vnd.sam-build-manifest+yaml", version: 0, accumulatedContent: store.manifestYaml, creationStatus: "completed" },
            ...Object.entries(store.listedVersions).map(([filename, version]) => ({ filename, mimeType: "application/vnd.sam-agent-config+yaml", version })),
        ],
        isLoading: false,
        refetch: () => Promise.resolve(),
    }),
}));
vi.mock("@/api/builder", async importOriginal => ({
    ...(await importOriginal<typeof import("@/api/builder")>()),
    getManifestContent: () => Promise.resolve(store.manifestYaml),
    useManifestContentQuery: () => ({ data: undefined }),
}));
vi.mock("./hooks/useComponentsMissingCredentials", () => ({ useComponentsMissingCredentials: () => new Map() }));
vi.mock("./BuilderChatPanel", () => ({ BuilderChatPanel: () => null }));
const mounted = vi.hoisted(() => ({ actions: 0, canvasManifestName: null as string | null }));
vi.mock("./BuilderCanvas", () => ({
    BuilderCanvas: (props: { manifest?: { name?: string } | null }) => {
        mounted.canvasManifestName = props.manifest?.name ?? null;
        return null;
    },
}));

vi.mock("./BuilderActions", () => ({
    BuilderActions: () => {
        mounted.actions += 1;
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
beforeEach(() => {
    sessionStorage.clear();
    store.listedVersions = {};
    mounted.actions = 0;
    mounted.canvasManifestName = null;
});

function renderBuilder() {
    render(
        <MemoryRouter initialEntries={["/builder"]}>
            <StoryProvider chatContextValues={{ isResponding: false, sessionId: "s1", downloadAndResolveArtifact: () => Promise.resolve(null) } as unknown as Partial<ChatContextValue>}>
                <BuilderPage />
            </StoryProvider>
        </MemoryRouter>
    );
}

const VALIDATED_WITH_NULLS = `name: sys
display_name:
description: null
full_validation_passed: true
risks: null
components:
  - filename: a.yaml
    type: agent
    name: A
    origin: session
    status: validated
    description: null
    id: null
    deployed_version: null
    tools: null
`;

const STAMP_CLEARED_BY_NULL = `name: sys
full_validation_passed: null
components:
  - filename: a.yaml
    type: agent
    name: A
    origin: session
    status: validated
`;

describe("BuilderPage — a null in an optional manifest field", () => {
    test("reads a validated manifest whose other optional fields are null: no banner, Build & Activate mounted", async () => {
        store.manifestYaml = VALIDATED_WITH_NULLS;
        renderBuilder();

        await waitFor(() => expect(mounted.actions).toBeGreaterThan(0));
        expect(screen.queryByText(/couldn't be read/i)).toBeNull();
    });

    test("reads a null full_validation_passed as not yet earned, not as an unreadable manifest", async () => {
        store.manifestYaml = STAMP_CLEARED_BY_NULL;
        renderBuilder();

        await waitFor(() => expect(mounted.canvasManifestName).toBe("sys"));
        expect(screen.queryByText(/couldn't be read/i)).toBeNull();
        expect(mounted.actions).toBe(0);
    });
});

const STAMPED_AT_ONE = `name: sys
full_validation_passed: true
full_validation_versions:
  a.yaml: 1
components:
  - filename: a.yaml
    type: agent
    name: A
    origin: session
    status: validated
`;

describe("BuilderPage — a stamp recorded before an edit", () => {
    test("keeps Build & Activate (BuilderActions) mounted: unmounting it would reset the auto-fix budget", async () => {
        store.manifestYaml = STAMPED_AT_ONE;
        store.listedVersions = { "a.yaml": 4 };
        renderBuilder();

        await waitFor(() => expect(mounted.actions).toBeGreaterThan(0));
        expect(screen.queryByText(/couldn't be read/i)).toBeNull();
    });
});
