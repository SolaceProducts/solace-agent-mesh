// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { createElement } from "react";
import * as matchers from "@testing-library/jest-dom/matchers";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider, useLocation } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { Agent } from "@/types";

import { ChatProvider } from "./ChatProvider";
import { useChatContext } from "./contexts";

expect.extend(matchers);

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function LocationProbe() {
    const { pathname } = useLocation();
    return createElement("div", { "data-testid": "pathname" }, pathname);
}

function Consumer() {
    const { handleEditAgent, handleCreateAgent } = useChatContext();
    return createElement(
        "div",
        null,
        createElement("button", { "data-testid": "edit", onClick: () => handleEditAgent({ id: "agent-1" } as Agent) }, "edit"),
        createElement("button", { "data-testid": "create", onClick: () => handleCreateAgent({ id: "agent-2" } as Agent) }, "create"),
        createElement(LocationProbe)
    );
}

function renderWithRealChatProvider() {
    const router = createMemoryRouter(
        [
            {
                path: "*",
                element: createElement(StoryProvider, {
                    children: createElement(ChatProvider, { disableBuilderBridge: true, children: createElement(Consumer) }),
                }),
            },
        ],
        { initialEntries: ["/chat"] }
    );
    render(createElement(RouterProvider, { router }));
}

describe("ChatProvider — agent edit/create navigation targets", () => {
    test("handleEditAgent navigates under /agents", async () => {
        renderWithRealChatProvider();
        await userEvent.click(screen.getByTestId("edit"));
        expect(screen.getByTestId("pathname")).toHaveTextContent("/agents/agent-1/edit");
    });

    test("handleCreateAgent navigates to the Agents create route", async () => {
        renderWithRealChatProvider();
        await userEvent.click(screen.getByTestId("create"));
        expect(screen.getByTestId("pathname")).toHaveTextContent("/agents/new/edit");
    });
});
