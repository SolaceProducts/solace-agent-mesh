// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "../client";

import { createPromptShares, getPromptShares, replacePromptShares } from "./service";

let getSpy: ReturnType<typeof vi.spyOn>;
let postSpy: ReturnType<typeof vi.spyOn>;
let putSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    getSpy = vi.spyOn(api.webui, "get");
    postSpy = vi.spyOn(api.webui, "post");
    putSpy = vi.spyOn(api.webui, "put");
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe("getPromptShares", () => {
    test("calls GET on the shares collection and returns the paginated envelope", async () => {
        const envelope = {
            data: [
                {
                    id: "share-1",
                    userEmail: "editor@example.com",
                    accessLevel: "resource_editor",
                    sharedByEmail: "owner@example.com",
                    createdAt: "2026-05-29T08:00:00Z",
                    updatedAt: "2026-05-29T08:00:00Z",
                },
            ],
            meta: { pagination: { pageNumber: 1, pageSize: 1, totalPages: 1, count: 1, nextPage: null } },
        };
        getSpy.mockResolvedValue(envelope);

        const result = await getPromptShares("group-abc");

        expect(getSpy).toHaveBeenCalledWith("/api/v1/prompts/groups/group-abc/shares");
        expect(result).toEqual(envelope);
    });
});

describe("createPromptShares", () => {
    test("POSTs the shares list and returns the batch response", async () => {
        const requestBody = {
            shares: [{ userEmail: "editor@example.com", accessLevel: "resource_editor" as const }],
        };
        const responseBody = {
            created: [
                {
                    id: "share-1",
                    userEmail: "editor@example.com",
                    accessLevel: "resource_editor",
                    sharedByEmail: "owner@example.com",
                    createdAt: "2026-05-29T08:00:00Z",
                    updatedAt: "2026-05-29T08:00:00Z",
                },
            ],
            updated: [],
            totalProcessed: 1,
        };
        postSpy.mockResolvedValue(responseBody);

        const result = await createPromptShares("group-abc", requestBody);

        expect(postSpy).toHaveBeenCalledWith("/api/v1/prompts/groups/group-abc/shares", requestBody);
        expect(result).toEqual(responseBody);
    });
});

describe("replacePromptShares", () => {
    test("PUTs the shares list and resolves to undefined on 204 No Content", async () => {
        const requestBody = {
            shares: [{ userEmail: "editor@example.com", accessLevel: "resource_editor" as const }],
        };
        putSpy.mockResolvedValue(undefined);

        const result = await replacePromptShares("group-abc", requestBody);

        expect(putSpy).toHaveBeenCalledWith("/api/v1/prompts/groups/group-abc/shares", requestBody);
        expect(result).toBeUndefined();
    });
});
