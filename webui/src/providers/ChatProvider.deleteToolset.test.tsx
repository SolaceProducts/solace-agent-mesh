// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { Toolset } from "@/types";

import { ChatProvider } from "./ChatProvider";
import { useChatContext } from "./contexts";

const server = setupServer(
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.delete("*/api/v2/platform/toolsets/:id", () => new HttpResponse(null, { status: 204 })),
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
});
afterAll(() => server.close());

function Consumer() {
    const { handleDeleteCustomToolset } = useChatContext();
    return (
        <button data-testid="delete-toolset" onClick={() => handleDeleteCustomToolset({ id: "ts-1" } as Toolset)}>
            delete toolset
        </button>
    );
}

function renderWithChatProvider() {
    const router = createMemoryRouter(
        [
            {
                path: "*",
                element: (
                    <StoryProvider shouldIsolateQueryClient userContextValues={{ userHasCapability: () => true }}>
                        <ChatProvider disableBuilderBridge>
                            <Consumer />
                        </ChatProvider>
                    </StoryProvider>
                ),
            },
        ],
        { initialEntries: ["/toolsets"] }
    );
    render(<RouterProvider router={router} />);
}

describe("ChatProvider — handleDeleteCustomToolset", () => {
    test("deletes a custom toolset through /api/v2", async () => {
        const fetchSpy = vi.spyOn(globalThis, "fetch");
        const deletes = () => fetchSpy.mock.calls.filter(([, init]) => init?.method === "DELETE").map(([input]) => new URL(String(input)).pathname);

        renderWithChatProvider();
        await userEvent.click(await screen.findByTestId("delete-toolset"));

        await waitFor(() => expect(deletes()).toEqual(["/api/v2/platform/toolsets/ts-1"]));
    });
});
