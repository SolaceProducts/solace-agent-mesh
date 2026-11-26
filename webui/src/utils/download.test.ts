// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

describe("downloadAuthenticatedFile", () => {
    let downloadAuthenticatedFile: typeof import("./download").downloadAuthenticatedFile;
    let get: ReturnType<typeof vi.fn>;
    let click: ReturnType<typeof vi.fn>;

    beforeEach(async () => {
        vi.resetModules();
        get = vi.fn();
        vi.doMock("@/api/client", () => ({ api: { platform: { get } } }));

        click = vi.fn();
        vi.spyOn(document, "createElement").mockReturnValue({ href: "", download: "", click } as unknown as HTMLAnchorElement);
        vi.spyOn(document.body, "appendChild").mockImplementation(node => node);
        vi.spyOn(document.body, "removeChild").mockImplementation(node => node);
        globalThis.URL.createObjectURL = vi.fn(() => "blob:mock");
        globalThis.URL.revokeObjectURL = vi.fn();

        ({ downloadAuthenticatedFile } = await import("./download"));
    });

    afterEach(() => {
        vi.doUnmock("@/api/client");
        vi.restoreAllMocks();
    });

    it("fetches with fullResponse, converts to a blob, and triggers a download", async () => {
        const blob = new Blob(["spec: content"]);
        get.mockResolvedValue({ ok: true, blob: vi.fn().mockResolvedValue(blob) });

        const result = await downloadAuthenticatedFile("https://platform.example.com/api/v1/platform/connectors/c1/resource", "my-spec.yaml");

        expect(result).toBe(true);
        expect(get).toHaveBeenCalledWith("https://platform.example.com/api/v1/platform/connectors/c1/resource", { fullResponse: true });
        expect(globalThis.URL.createObjectURL).toHaveBeenCalledWith(blob);
        expect(click).toHaveBeenCalled();
    });

    it("returns false and logs when the response is not ok", async () => {
        const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        get.mockResolvedValue({ ok: false, statusText: "Forbidden", blob: vi.fn() });

        const result = await downloadAuthenticatedFile("https://platform.example.com/api/v1/platform/connectors/c1/resource", "my-spec.yaml");

        expect(result).toBe(false);
        expect(click).not.toHaveBeenCalled();
        expect(errorSpy).toHaveBeenCalled();
    });

    it("returns false and logs when the fetch throws", async () => {
        const errorSpy = vi.spyOn(console, "error").mockImplementation(() => {});
        get.mockRejectedValue(new Error("network down"));

        const result = await downloadAuthenticatedFile("https://platform.example.com/api/v1/platform/connectors/c1/resource", "my-spec.yaml");

        expect(result).toBe(false);
        expect(errorSpy).toHaveBeenCalled();
    });
});
