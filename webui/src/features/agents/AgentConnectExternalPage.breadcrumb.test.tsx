// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { createElement } from "react";
import * as matchers from "@testing-library/jest-dom/matchers";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test, vi } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { AgentConnectExternalPage } from "./AgentConnectExternalPage";

expect.extend(matchers);

const server = setupServer(
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({}))
);

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

function renderConnectPage(loaderData: { remoteAgentId?: string; isEdit?: boolean }, navigate = vi.fn()) {
    const router = createMemoryRouter(
        [
            {
                path: "*",
                loader: () => loaderData,
                element: createElement(StoryProvider, {
                    chatContextValues: { navigate, addNotification: vi.fn() },
                    children: createElement(AgentConnectExternalPage),
                }),
            },
        ],
        { initialEntries: ["/agents/external/new"] }
    );
    render(createElement(RouterProvider, { router }));
    return { navigate };
}

describe("AgentConnectExternalPage — breadcrumb", () => {
    test("create mode: breadcrumb returns to Agents", async () => {
        const { navigate } = renderConnectPage({ isEdit: false });

        await userEvent.click(await screen.findByRole("button", { name: "Agents" }));
        expect(navigate).toHaveBeenCalledWith("/agents");
    });

    test("edit mode while loading: breadcrumb returns to Agents", async () => {
        server.use(
            http.get("*/api/v2/platform/remoteAgents/:id", async () => {
                await delay("infinite");
                return HttpResponse.json({});
            })
        );
        const { navigate } = renderConnectPage({ isEdit: true, remoteAgentId: "remote-1" });

        await screen.findByText("Loading agent data...");
        await userEvent.click(screen.getByRole("button", { name: "Agents" }));
        expect(navigate).toHaveBeenCalledWith("/agents");
    });
});
