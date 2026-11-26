// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { beforeEach, describe, expect, test, vi } from "vitest";

describe("fetchPdfBlob", () => {
    let fetchPdfBlob: typeof import("@/api/artifacts/service").fetchPdfBlob;
    const getMock = vi.fn();

    beforeEach(async () => {
        vi.resetModules();
        getMock.mockReset();

        vi.doMock("@/api/client", () => ({
            api: { webui: { get: getMock } },
            getErrorFromResponse: vi.fn(async () => "err"),
        }));

        globalThis.URL.createObjectURL = vi.fn(() => "blob:created");

        const mod = await import("@/api/artifacts/service");
        fetchPdfBlob = mod.fetchPdfBlob;
    });

    test("blob: URLs are returned unchanged and never re-fetched (regression: base double-prepend on remote)", async () => {
        const blobUrl = "blob:https://sam.example.test/abc-123";
        const result = await fetchPdfBlob(blobUrl);

        expect(result).toBe(blobUrl);
        expect(getMock).not.toHaveBeenCalled();
    });

    test("relative API URLs are fetched verbatim via api.webui.get (base applied exactly once downstream)", async () => {
        getMock.mockResolvedValue({ ok: true, blob: async () => new Blob(["x"]) });

        const relative = "/api/v1/artifacts/s/f.pdf/versions/0";
        await fetchPdfBlob(relative);

        expect(getMock).toHaveBeenCalledWith(relative, { fullResponse: true });
    });
});
