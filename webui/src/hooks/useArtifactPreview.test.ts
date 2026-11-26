// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types";

let versionList: number[] = [0, 1];
let contentShouldReject = false;
let versionsUrlCalls: string[] = [];

describe("useArtifactPreview — navigateToVersion", () => {
    let useArtifactPreview: typeof import("@/hooks/useArtifactPreview").useArtifactPreview;

    beforeEach(async () => {
        vi.resetModules();
        versionList = [0, 1];
        contentShouldReject = false;
        versionsUrlCalls = [];

        vi.doMock("@/api/client", () => ({
            api: {
                webui: {
                    get: vi.fn(async (url: string) => {
                        versionsUrlCalls.push(url);
                        return { data: versionList };
                    }),
                    getFullUrl: (endpoint: string) => endpoint,
                },
            },
            getErrorFromResponse: vi.fn(async () => "error"),
        }));

        vi.doMock("@/utils", async importOriginal => {
            const actual = await importOriginal<typeof import("@/utils")>();
            return {
                ...actual,
                getArtifactContent: vi.fn(async ({ version }: { version?: number }) => {
                    if (contentShouldReject) throw new Error("content fetch failed");
                    return { content: `body-v${version}`, mimeType: "text/markdown" };
                }),
            };
        });

        useArtifactPreview = (await import("@/hooks/useArtifactPreview")).useArtifactPreview;
    });

    const artifacts: ArtifactInfo[] = [{ filename: "report.md", mimeType: "text/markdown", size: 10, lastModified: "2026-06-09T00:00:00Z" }];

    function setup(setError?: (e: { title: string; error: string }) => void) {
        return renderHook(() => useArtifactPreview({ sessionId: "sess-1", artifacts, setError }));
    }

    test("switches to a version created after the preview opened (refetches stale version list)", async () => {
        const { result } = setup();

        await act(async () => {
            await result.current.openPreview("report.md");
        });
        expect(result.current.preview.currentVersion).toBe(1);
        expect(result.current.preview.availableVersions).toEqual([0, 1]);

        versionList = [0, 1, 2];

        await act(async () => {
            await result.current.navigateToVersion("report.md", 2);
        });

        await waitFor(() => expect(result.current.preview.currentVersion).toBe(2));
        expect(result.current.preview.availableVersions).toContain(2);
        expect(result.current.preview.content?.content).toBe("body-v2");
    });

    test("resolves a version even when no preview was opened first (availableVersions still null)", async () => {
        const { result } = setup();
        versionList = [0, 1, 2];

        await act(async () => {
            await result.current.navigateToVersion("report.md", 2);
        });

        await waitFor(() => expect(result.current.preview.currentVersion).toBe(2));
        expect(result.current.preview.filename).toBe("report.md");
        expect(result.current.preview.content?.content).toBe("body-v2");
    });

    test("returns null and leaves state untouched when the target truly does not exist", async () => {
        const { result } = setup();
        versionList = [0, 1];

        let returned: unknown;
        await act(async () => {
            returned = await result.current.navigateToVersion("report.md", 5);
        });

        expect(returned).toBeNull();
        expect(result.current.preview.currentVersion).toBeNull();
    });

    test("surfaces a user-facing error (not just console.warn) when the target version is missing", async () => {
        const setError = vi.fn();
        const { result } = setup(setError);
        versionList = [0, 1];

        await act(async () => {
            await result.current.navigateToVersion("report.md", 5);
        });

        expect(setError).toHaveBeenCalledTimes(1);
        expect(setError.mock.calls[0][0].title).toBe("Artifact Version Unavailable");
    });

    test("returns null on an empty version-list response", async () => {
        const { result } = setup();
        versionList = [];

        let returned: unknown;
        await act(async () => {
            returned = await result.current.navigateToVersion("report.md", 2);
        });

        expect(returned).toBeNull();
        expect(result.current.preview.currentVersion).toBeNull();
    });

    test("reports the error and returns null when the content fetch fails", async () => {
        const setError = vi.fn();
        const { result } = setup(setError);
        versionList = [0, 1, 2];
        contentShouldReject = true;

        let returned: unknown;
        await act(async () => {
            returned = await result.current.navigateToVersion("report.md", 2);
        });

        expect(returned).toBeNull();
        expect(setError).toHaveBeenCalledTimes(1);
        expect(setError.mock.calls[0][0].title).toBe("Artifact Version Preview Failed");
    });

    test("scopes the version fetch to sessionIdOverride, not the hook's session", async () => {
        const { result } = setup();
        versionList = [0];

        await act(async () => {
            await result.current.navigateToVersion("report.md", 0, "sess-OTHER");
        });

        expect(versionsUrlCalls.length).toBeGreaterThan(0);
        expect(versionsUrlCalls.every(url => url.includes("sess-OTHER"))).toBe(true);
        expect(versionsUrlCalls.some(url => url.includes("/sess-1/"))).toBe(false);
        expect(result.current.preview.currentVersion).toBe(0);
        expect(result.current.preview.content?.url).toContain("sess-OTHER");
    });

    test("openPreview honors sessionIdOverride for the version fetch", async () => {
        const { result } = setup();
        versionList = [0, 1];

        await act(async () => {
            await result.current.openPreview("report.md", "sess-OTHER");
        });

        expect(versionsUrlCalls.length).toBeGreaterThan(0);
        expect(versionsUrlCalls.every(url => url.includes("sess-OTHER"))).toBe(true);
        expect(versionsUrlCalls.some(url => url.includes("/sess-1/"))).toBe(false);
    });

    test("does not clobber the active preview when a navigation for a different file lands late", async () => {
        const { result } = setup();

        await act(async () => {
            await result.current.openPreview("report.md");
        });
        expect(result.current.preview.filename).toBe("report.md");

        await act(async () => {
            await result.current.navigateToVersion("other.md", 0);
        });

        expect(result.current.preview.filename).toBe("report.md");
        expect(result.current.preview.currentVersion).toBe(1);
    });
});
