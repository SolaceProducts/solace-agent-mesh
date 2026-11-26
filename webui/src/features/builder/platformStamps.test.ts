// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { currentPlatformStamps, stampWritten } from "./platformStamps";

const skillId = "11111111-2222-3333-4444-555555555555";

const skillV2 = {
    id: skillId,
    type: "skill",
    origin: "custom",
    name: "order-lookup",
    label: null,
    description: "Look up orders.",
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
    bundle: { contentHash: "hash-v2" },
    agents: [],
    configSummary: null,
    createdBy: null,
    updatedBy: null,
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: null,
};

describe("platform stamps of a skill", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const paths = () => fetchMock.mock.calls.map(([input]) => new URL(String(input), "http://localhost").pathname);

    beforeEach(() => {
        fetchMock.mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ data: skillV2 }), { status: 200, headers: { "content-type": "application/json" } })));
        vi.stubGlobal("fetch", fetchMock);
    });

    afterEach(() => {
        fetchMock.mockReset();
        vi.unstubAllGlobals();
    });

    test("reads a written skill's stamp from /api/v2", async () => {
        expect(await stampWritten([{ id: skillId, kind: "skill" }])).toEqual([{ id: skillId, kind: "skill", stamp: "hash-v2" }]);
        expect(paths()).toEqual([`/api/v2/platform/skills/${skillId}`]);
    });

    test("reads a remembered skill's current stamp from /api/v2", async () => {
        const stamps = await currentPlatformStamps(new Map([[skillId, { kind: "skill", stamp: "hash-v2" }]]));

        expect(stamps.get(skillId)).toBe("hash-v2");
        expect(paths()).toEqual([`/api/v2/platform/skills/${skillId}`]);
    });
});
