// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test } from "vitest";

import { BuilderContext, type BuilderContextValue } from "@/providers/contexts/BuilderContext";
import { ChatContext } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { ArtifactInfo } from "@/types";

import { ChatProvider } from "./ChatProvider";

const SESSION_ID = "session-with-working-spec";
const deck: ArtifactInfo = { filename: "deck.md", mimeType: "text/markdown", size: 10, lastModified: "2026-09-25T00:00:00Z", uri: `artifact://${SESSION_ID}/deck.md` };
const spec: ArtifactInfo = { ...deck, filename: "deck-spec.yaml", mimeType: "application/yaml", uri: `artifact://${SESSION_ID}/deck-spec.yaml`, tags: ["__working"] };

const meta = (count: number) => ({ pagination: { pageNumber: 1, pageSize: 100, count, totalPages: 1, nextPage: null } });

const server = setupServer(
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json({ data: [], meta: meta(0) })),
    http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ id: SESSION_ID, name: "Deck" })),
    http.get("*/api/v1/artifacts/:id", ({ request }) => {
        const list = new URL(request.url).searchParams.get("visibility") === "all" ? [deck, spec] : [deck];
        return HttpResponse.json({ data: list, meta: meta(list.length) });
    }),
    http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [], meta: meta(0) }))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
beforeEach(() => localStorage.clear());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const builderContextValue: BuilderContextValue = {
    builderMode: false,
    setBuilderMode: () => {},
    builderCreationState: { isBuilding: false, components: [], overallStatus: "idle" },
    setBuilderCreationState: () => {},
    selectedBuilderNode: null,
    setSelectedBuilderNode: () => {},
    builderTabContent: null,
};

function PreviewConsumer() {
    const ctx = React.useContext(ChatContext);
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <span data-testid="session-id">{ctx.sessionId || "none"}</span>
            <span data-testid="preview">{ctx.previewArtifact?.filename ?? "none"}</span>
            <button data-testid="switch-session" onClick={() => ctx.handleSwitchSession(SESSION_ID)}>
                Switch
            </button>
            <button data-testid="preview-spec" onClick={() => ctx.setPreviewArtifact(spec)}>
                Preview spec
            </button>
        </div>
    );
}

describe("ChatProvider preview of a revealed working file", () => {
    test("resolves a working file the shared list strips", async () => {
        render(
            <MemoryRouter initialEntries={["/"]}>
                <StoryProvider shouldIsolateQueryClient userContextValues={{ userHasCapability: () => false }}>
                    <BuilderContext.Provider value={builderContextValue}>
                        <ChatProvider disableBuilderBridge>
                            <PreviewConsumer />
                        </ChatProvider>
                    </BuilderContext.Provider>
                </StoryProvider>
            </MemoryRouter>
        );
        fireEvent.click(screen.getByTestId("switch-session"));
        await waitFor(() => expect(screen.getByTestId("session-id")).toHaveTextContent(SESSION_ID));

        fireEvent.click(screen.getByTestId("preview-spec"));

        await waitFor(() => expect(screen.getByTestId("preview")).toHaveTextContent("deck-spec.yaml"));
    });
});
