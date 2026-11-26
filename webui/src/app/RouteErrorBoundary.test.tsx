// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import RouteErrorBoundary from "./RouteErrorBoundary";

function Boom(): never {
    throw new Error("kaboom");
}

describe("RouteErrorBoundary", () => {
    beforeEach(() => {
        vi.spyOn(console, "error").mockImplementation(() => {});
    });
    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("renders the fallback when a route throws", async () => {
        const router = createMemoryRouter([{ path: "/", element: <Boom />, errorElement: <RouteErrorBoundary /> }]);
        render(<RouterProvider router={router} />);

        expect(await screen.findByText(/This page ran into an unexpected error/)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Reload Page" })).toBeInTheDocument();
    });
});
