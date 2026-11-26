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
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { AgentManagementPage } from "./AgentManagementPage";

expect.extend(matchers);

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
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

function renderManagementPage() {
    render(
        createElement(
            MemoryRouter,
            { initialEntries: ["/agents"] },
            createElement(StoryProvider, {
                userContextValues: { userHasCapability: () => true },
                chatContextValues: { platformConfigured: true },
                children: createElement("div", null, createElement(AgentManagementPage), createElement(LocationProbe)),
            })
        )
    );
}

describe("AgentManagementPage — create/connect navigation", () => {
    test("Connect External Agent navigates under /agents", async () => {
        renderManagementPage();

        await userEvent.click(await screen.findByTestId("addAgentDropdownTrigger"));
        await userEvent.click(await screen.findByTestId("addAgentDropdownConnectExternal"));

        expect(screen.getByTestId("pathname")).toHaveTextContent("/agents/external/new");
    });
});
