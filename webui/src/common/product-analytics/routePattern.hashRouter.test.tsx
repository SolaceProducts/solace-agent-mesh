// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render } from "@testing-library/react";
import { createHashRouter, Outlet, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "./client";
import { RouteChangeTracker } from "./RouteChangeTracker";
import { currentRoutePattern, publishRoutePattern } from "./routePattern";

function Layout() {
    return (
        <>
            <RouteChangeTracker />
            <Outlet />
        </>
    );
}

const routes = [
    {
        path: "/",
        element: <Layout />,
        children: [{ path: "agent-mesh/workflows/:workflowName", element: <div>workflow</div> }],
    },
];

function renderAtHash(hash: string) {
    globalThis.location.hash = hash;
    render(<RouterProvider router={createHashRouter(routes)} />);
}

beforeEach(() => {
    vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    publishRoutePattern("/");
});

afterEach(() => {
    vi.restoreAllMocks();
    globalThis.location.hash = "";
});

describe("the page an error reports under the app's hash router", () => {
    test("the URL path is not the route: it stays / while the fragment carries everything", () => {
        renderAtHash("#/agent-mesh/workflows/refund-bot");

        expect(globalThis.location.pathname).toBe("/");
        expect(globalThis.location.hash).toContain("refund-bot");
    });

    test("the reported page is the router's own pattern, so the workflow's name never travels", () => {
        renderAtHash("#/agent-mesh/workflows/refund-bot");

        expect(currentRoutePattern()).toBe("/agent-mesh/workflows/:workflowName");
        expect(currentRoutePattern()).not.toContain("refund-bot");
    });

    test("without a router nothing is published, so the page is the root rather than a guess", () => {
        expect(currentRoutePattern()).toBe("/");
    });
});
