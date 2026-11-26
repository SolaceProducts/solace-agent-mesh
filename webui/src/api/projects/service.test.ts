// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { updateFileMetadata } from "./service";

describe("updateFileMetadata", () => {
    let patchSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        patchSpy = vi.spyOn(api.webui, "patch").mockResolvedValue({} as never);
    });
    afterEach(() => {
        patchSpy.mockRestore();
    });

    test("PATCHes a JSON {description} body — never FormData", async () => {
        await updateFileMetadata("proj-123", "notes.txt", "an updated description");

        expect(patchSpy).toHaveBeenCalledTimes(1);
        const [url, body] = patchSpy.mock.calls[0] as [string, unknown];
        expect(url).toBe("/api/v1/projects/proj-123/artifacts/notes.txt");
        expect(body).not.toBeInstanceOf(FormData);
        expect(body).toEqual({ description: "an updated description" });
    });

    test("URL-encodes the filename path segment", async () => {
        await updateFileMetadata("proj-123", "notes with spaces.txt", "d");

        const [url] = patchSpy.mock.calls[0] as [string, unknown];
        expect(url).toBe("/api/v1/projects/proj-123/artifacts/notes%20with%20spaces.txt");
    });
});
