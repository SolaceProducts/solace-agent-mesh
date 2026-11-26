// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { MockEventSource } from "@/testing/helpers/mockEventSource";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ChatPage } from "./ChatPage";

const server = setupServer(
    http.get("*/api/v1/budget/status", () => HttpResponse.json({ ownLevel: "limit", meshLimitReached: false, periodEnd: "2099-11-01T00:00:00.000Z", capKey: "cap-a" })),
    http.get("*/api/v1/sessions/:sessionId/contextUsage", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/share/link/:sessionId", () => new HttpResponse(null, { status: 404 }))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
    localStorage.clear();
});
afterEach(() => {
    server.resetHandlers();
    vi.unstubAllGlobals();
});
afterAll(() => server.close());

function renderChatPage() {
    return renderWithProviders(<ChatPage />, {
        userContextValues: { username: null },
        chatContextValues: { sessionId: "session-1", messages: [], isResponding: false },
        configContextValues: { persistenceEnabled: false },
    });
}

describe("ChatPage budget banner", () => {
    test("shows the budget banner", async () => {
        renderChatPage();
        expect(await screen.findByText(/You've used up your usage budget/)).toBeInTheDocument();
    });
});
