// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { getPaginatedMeta } from "@/testing/helpers";
import { v2ResourceOf } from "@/testing/helpers/platformResponses";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { AgentEditPage } from "./AgentEditPage";

const server = setupServer(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/skills", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/config", () => HttpResponse.json({})),
    http.get("*/api/v1/speech/config", () => HttpResponse.json({})),
    http.post("*/api/v2/platform/agents", () => HttpResponse.json(v2ResourceOf("/api/v2/platform/agents", { id: "created", name: "created" })))
);

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeAll(() => server.listen());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

function validationErrors(): Record<string, unknown>[] {
    return (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === "form:validation_error").map(call => call[1] as Record<string, unknown>);
}

function renderNewAgentPage() {
    const loaderData = { agentId: undefined, isNew: true };
    const router = createMemoryRouter(
        [
            {
                path: "/agents/new/edit",
                loader: () => loaderData,
                element: createElement(StoryProvider, {
                    chatContextValues: {
                        navigate: vi.fn(),
                        useLoaderData: (() => loaderData) as unknown as typeof import("react-router-dom").useLoaderData,
                        addNotification: vi.fn(),
                    },
                    children: createElement(AgentEditPage),
                }),
            },
            { path: "*", element: createElement("div") },
        ],
        { initialEntries: ["/agents/new/edit"] }
    );
    render(createElement(RouterProvider, { router }));
}

describe("the agent editor reports where its own pre-save gate stops people", () => {
    test("Create with no instructions reports the field the instructions panel validates under", async () => {
        renderNewAgentPage();

        await userEvent.click(await screen.findByTestId("agentSave"));

        expect(validationErrors()).toEqual([{ form: "agent", field: "systemPrompt", errorType: "too_small", path: "/" }]);
    });

    test("the toast text never reaches the event", async () => {
        renderNewAgentPage();

        await userEvent.click(await screen.findByTestId("agentSave"));

        const serialized = JSON.stringify(validationErrors());
        expect(serialized).not.toContain("Please complete");
        expect(serialized).not.toContain("Instructions");
    });

    test("reports nothing but form:validation_error", async () => {
        renderNewAgentPage();

        await userEvent.click(await screen.findByTestId("agentSave"));

        expect((trackSpy.mock.calls as unknown[][]).map(call => call[0])).toEqual(["form:validation_error"]);
    });
});
