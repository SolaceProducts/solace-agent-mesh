// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { beforeEach, describe, expect, test, vi } from "vitest";

import { isAgentFixableError } from "../builderUtils";

describe("fetchArtifactAtVersion", () => {
    const webuiGet = vi.fn();
    let fetchArtifactAtVersion: typeof import("./artifactUtils").fetchArtifactAtVersion;

    beforeEach(async () => {
        vi.resetModules();
        webuiGet.mockReset();
        vi.spyOn(console, "warn").mockImplementation(() => {});
        vi.doMock("@/api/client", () => ({ api: { webui: { get: (...args: unknown[]) => webuiGet(...args) }, platform: { get: vi.fn(), post: vi.fn() } } }));
        ({ fetchArtifactAtVersion } = await import("./artifactUtils"));
    });

    test("reads exactly the version asked for, from the artifact's own URL", async () => {
        webuiGet.mockResolvedValue({ ok: true, blob: async () => ({ arrayBuffer: async () => new TextEncoder().encode("name: db").buffer }) });

        await expect(fetchArtifactAtVersion("session-1", "db.json", 3)).resolves.toBe("name: db");

        expect(webuiGet).toHaveBeenCalledWith("/api/v1/artifacts/session-1/db.json/versions/3", { fullResponse: true });
    });

    test.each([
        ["the platform has no such copy (404)", () => Promise.resolve({ ok: false, status: 404 }), 404],
        ["the platform refuses (403)", () => Promise.resolve({ ok: false, status: 403 }), 403],
        ["the platform answers 400", () => Promise.resolve({ ok: false, status: 400 }), 400],
        ["the platform fails (500)", () => Promise.resolve({ ok: false, status: 500 }), 500],
        ["the network fails", () => Promise.reject(new TypeError("Failed to fetch")), undefined],
    ])("when %s: an infrastructure error the builder is not sent to fix, and the status is logged", async (_label, answer, status) => {
        webuiGet.mockImplementation(answer);

        const failure = await fetchArtifactAtVersion("session-1", "db.json", 3).catch((err: unknown) => err);

        expect(failure).toBeInstanceOf(Error);
        expect((failure as Error).message).toContain('"db.json" at version 3');
        expect((failure as Error).message).toContain("Try again");
        expect((failure as { status?: number }).status).toBe(503);
        expect(isAgentFixableError(failure)).toBe(false);
        expect(console.warn).toHaveBeenCalledWith(expect.stringContaining("could not read a config at the version"), { filename: "db.json", version: 3, status });
    });

    test("keeps what failed as the cause, with the status it carried", async () => {
        webuiGet.mockResolvedValue({ ok: false, status: 404 });

        const failure = await fetchArtifactAtVersion("session-1", "db.json", 3).catch((err: unknown) => err);

        expect((failure as Error).cause).toMatchObject({ status: 404 });
    });

    test("puts neither the session nor the URL in the log", async () => {
        webuiGet.mockResolvedValue({ ok: false, status: 500 });

        await fetchArtifactAtVersion("session-1", "db.json", 3).catch(() => undefined);

        expect(JSON.stringify(vi.mocked(console.warn).mock.calls)).not.toMatch(/session-1|\/api\//);
    });
});
