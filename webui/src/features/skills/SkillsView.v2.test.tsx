// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { SkillsView } from "./SkillsView";

const skillV2 = (overrides: Record<string, unknown>) => ({
    id: "11111111-1111-1111-1111-111111111111",
    type: "skill",
    origin: "custom",
    name: "my-custom-skill",
    label: null,
    description: "a custom one",
    tags: [],
    discoveryStatus: "ready",
    hasReferences: false,
    hasAssets: false,
    hasTools: false,
    resourceCount: 0,
    referenceCount: 0,
    assetCount: 0,
    bundledTools: [],
    toolsets: [],
    bundle: null,
    agents: [],
    configSummary: null,
    createdBy: "alice",
    updatedBy: "alice",
    createdTime: "2026-01-01T00:00:00.000Z",
    updatedTime: "2026-01-01T00:00:00.000Z",
    ...overrides,
});

const pageOf = (data: unknown[]) => new Response(JSON.stringify({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage: null, totalPages: 1 } } }), { status: 200, headers: { "content-type": "application/json" } });

describe("SkillsView on /api/v2", () => {
    const fetchMock = vi.fn<(url: string) => Promise<Response>>();
    const requestedUrls = () => fetchMock.mock.calls.map(([url]) => String(url));

    beforeAll(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });

    beforeEach(() => {
        fetchMock.mockImplementation(async url => {
            const u = String(url);
            if (u.includes("/api/v2/platform/skills") && u.includes("origin=builtin")) return pageOf([skillV2({ id: "sam-docs", name: "sam-docs", origin: "builtin", label: "SAM docs", createdBy: null, createdTime: null, updatedTime: null })]);
            if (u.includes("/api/v2/platform/skills") && u.includes("origin=custom")) return pageOf([skillV2({})]);
            return pageOf([]);
        });
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        fetchMock.mockReset();
    });

    it("lists custom and built-in skills from /api/v2", async () => {
        const { findByText } = renderWithProviders(<SkillsView />, { initialPath: "/skills" });

        expect(await findByText("my-custom-skill")).toBeInTheDocument();
        expect(await findByText("sam-docs")).toBeInTheDocument();

        const urls = requestedUrls();
        expect(urls.some(u => u.includes("/api/v2/platform/skills?origin=custom"))).toBe(true);
        expect(urls.some(u => u.includes("/api/v2/platform/skills?origin=builtin"))).toBe(true);
    });
});
