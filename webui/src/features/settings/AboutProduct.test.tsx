// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { AboutProduct } from "./AboutProduct";

const v2Body = {
    data: {
        products: [
            { key: "sam-gateway-go", name: "Gateway", version: "1.2.3" },
            { key: "sam-str-go", name: "Runtime", version: "4.5.6" },
        ],
        minClientVersion: null,
        apiVersion: null,
    },
};

describe("AboutProduct", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const requestedUrls = () => fetchMock.mock.calls.map(([input]) => String(input instanceof Request ? input.url : input));

    beforeEach(() => {
        fetchMock.mockImplementation(async () => new Response(JSON.stringify(v2Body), { status: 200, headers: { "Content-Type": "application/json" } }));
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    it("reads /api/v2/platform/version and lists every product", async () => {
        renderWithProviders(<AboutProduct />);

        expect(await screen.findByText("Runtime")).toBeInTheDocument();
        expect(screen.getByText("Gateway")).toBeInTheDocument();
        expect(screen.getByText("4.5.6")).toBeInTheDocument();
        expect(screen.getByText("1.2.3")).toBeInTheDocument();
        expect(requestedUrls().some(u => u.includes("/api/v2/platform/version"))).toBe(true);
    });
});
