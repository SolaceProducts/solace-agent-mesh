// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, Outlet, RouterProvider, useNavigate } from "react-router-dom";
import { afterAll, afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "./client";
import { RouteChangeTracker } from "./RouteChangeTracker";

let trackSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});

afterEach(() => {
    vi.restoreAllMocks();
});

afterAll(() => {
    productAnalyticsClient.destroy();
});

function Layout() {
    const navigate = useNavigate();
    return (
        <>
            <RouteChangeTracker />
            <button onClick={() => navigate("/connectors")}>go-connectors</button>
            <button onClick={() => navigate("/models")}>go-models</button>
            <button onClick={() => navigate("/agent-mesh")}>go-agents</button>
            <Outlet />
        </>
    );
}

const routes = [
    {
        path: "/",
        element: <Layout />,
        children: [
            { path: "agent-mesh", element: <div>agents</div> },
            { path: "agent-mesh/:id", element: <div>agent</div> },
            { path: "shared-chat/:shareId", element: <div>shared</div> },
            { path: "connectors", element: <div>connectors</div> },
            { path: "connectors/:id/tools/mcp/:toolName", element: <div>tool</div> },
            { path: "models", element: <div>models</div> },
        ],
    },
];

function renderAtPath(initialPath: string) {
    const router = createMemoryRouter(routes, { initialEntries: [initialPath] });
    render(<RouterProvider router={router} />);
}

describe("RouteChangeTracker", () => {
    test("fires page_view with a plain pathname for a param-free route", () => {
        renderAtPath("/agent-mesh");

        expect(trackSpy).toHaveBeenCalledOnce();
        expect(trackSpy).toHaveBeenCalledWith("page_view", { path: "/agent-mesh" });
    });

    test("redacts a resource id to its route pattern even though the tracker mounts above the param route", () => {
        renderAtPath("/agent-mesh/3f2a-9c-secret-uuid");

        expect(trackSpy).toHaveBeenCalledOnce();
        expect(trackSpy).toHaveBeenCalledWith("page_view", { path: "/agent-mesh/:id" });
    });

    test("redacts a share token so it never reaches analytics", () => {
        renderAtPath("/shared-chat/s3cr3t-share-token");

        expect(trackSpy).toHaveBeenCalledWith("page_view", { path: "/shared-chat/:shareId" });
    });

    test("redacts every param in a deeply nested route", () => {
        renderAtPath("/connectors/abc-123/tools/mcp/lookupThing");

        expect(trackSpy).toHaveBeenCalledWith("page_view", { path: "/connectors/:id/tools/mcp/:toolName" });
    });

    test("fires page_view again when the route changes", async () => {
        const user = userEvent.setup();
        renderAtPath("/agent-mesh");

        expect(trackSpy).toHaveBeenCalledWith("page_view", { path: "/agent-mesh" });
        trackSpy.mockClear();

        await user.click(screen.getByRole("button", { name: "go-models" }));

        expect(trackSpy).toHaveBeenCalledOnce();
        expect(trackSpy).toHaveBeenCalledWith("page_view", { path: "/models" });
    });

    test("does not fire a second time when navigating to the same route", async () => {
        const user = userEvent.setup();
        renderAtPath("/agent-mesh");

        trackSpy.mockClear();
        await user.click(screen.getByRole("button", { name: "go-agents" }));

        expect(trackSpy).not.toHaveBeenCalled();
    });

    test("fires distinct page_view events for each route in a multi-step navigation", async () => {
        const user = userEvent.setup();
        renderAtPath("/agent-mesh");

        trackSpy.mockClear();
        await user.click(screen.getByRole("button", { name: "go-connectors" }));
        await user.click(screen.getByRole("button", { name: "go-models" }));

        expect(trackSpy).toHaveBeenNthCalledWith(1, "page_view", { path: "/connectors" });
        expect(trackSpy).toHaveBeenNthCalledWith(2, "page_view", { path: "/models" });
    });
});
